package helper

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/oss_svc"
)

// ossTransferService 是传输面用到的那一小片 oss_svc.Service。
// 声明成接口只为了让展开与读写能在内存里被验证——对象存储的分层列举与分页游标
// 是这个适配器的全部难点，跑不起服务端就等于测不到它们。
type ossTransferService interface {
	ListObjects(ctx context.Context, req *oss_svc.ListObjectsRequest) (*oss_svc.ListObjectsResult, error)
	StatObject(ctx context.Context, req *oss_svc.ObjectRequest) (*oss_svc.ObjectItem, error)
	GetObject(ctx context.Context, assetID int64, bucket, key string) (io.ReadCloser, int64, error)
	PutObject(ctx context.Context, assetID int64, bucket, key string, r io.Reader, size int64, contentType string) error
}

// ossAdapter 是对象存储端点，路径写成 <asset>:/<bucket>/<key>（spec §6.2）。
type ossAdapter struct{ svc ossTransferService }

var ossTransfer TransferAdapter = ossAdapter{svc: oss_svc.New()}

func init() {
	RegisterTransferAdapter(asset_entity.AssetTypeOSS, ossTransfer)
}

// List 展开一个 OSS 端点。三种形态与本地/SSH 端一一对应：
// 具体对象 → 一条；前缀（光桶名或以 "/" 收尾）→ 递归拉全；通配 → 按 path.Match 过滤。
//
// 形态校验在这里而不在共享的端点解析器里：解析器是类型无关的，只认 <asset>:<path>；
// "剥掉前导 / 之后必须是 <bucket>/<key>" 是对象存储自己的语法（spec §6.2「端点路径语法」）。
func (a ossAdapter) List(
	ctx context.Context, asset *asset_entity.Asset, pattern string, recursive bool,
) (*ListResult, error) {
	bucket, key, err := splitOSSEndpointPath(pattern)
	if err != nil {
		return nil, err
	}

	if hasGlobMeta(key) {
		return a.listGlob(ctx, asset.ID, bucket, key, pattern, recursive)
	}

	if key == "" || strings.HasSuffix(key, "/") {
		// 与本地端"指名一个目录却没有 recursive"同一个答案：报错，不猜。前缀不是对象，
		// 既读不出字节，也不该被当成一次单文件传输的两端之一。
		if !recursive {
			return nil, fmt.Errorf(
				"%q is a prefix, not an object (<bucket>/<key>); set recursive to transfer everything under it", pattern)
		}
		res := &ListResult{}
		if err := a.walkPrefix(ctx, asset.ID, bucket, key, func(obj oss_svc.ObjectItem) error {
			res.Entries = append(res.Entries, ossEntry(bucket, obj, key))
			return nil
		}); err != nil {
			return nil, err
		}
		return res, nil
	}

	// 被指名的单个对象先 Stat：它同时取到 Size 并把"对象不存在"挡在审批之前——
	// 批准之后必然失败的传输不该先打断用户一次。
	item, err := a.svc.StatObject(ctx, &oss_svc.ObjectRequest{AssetID: asset.ID, Bucket: bucket, Key: key})
	if err != nil {
		return nil, err
	}
	return &ListResult{Entries: []Entry{{
		Path: ossEndpointPath(bucket, key), RelPath: path.Base(key), Size: item.Size,
	}}}, nil
}

// listGlob 按前缀把 globBase 之下的对象拉全，再在客户端按 path.Match 过滤——
// 对象存储的列举只认前缀，服务端没有 glob。
//
// '*' 不跨 "/"，所以"通配命中了一个目录"在这里表现为**命中了某个 key 的祖先前缀**
// （对象存储没有目录实体）。递归时下钻它，非递归时报错并点名——静默略过就是一次
// 看起来成功、实际只传了一部分的传输，而同一个 List 里"指名前缀却没有 recursive"
// 那一支早就是报错了。
func (a ossAdapter) listGlob(
	ctx context.Context, assetID int64, bucket, keyPattern, pattern string, recursive bool,
) (*ListResult, error) {
	match := func(name string) (bool, error) {
		ok, err := path.Match(keyPattern, name)
		if err != nil {
			return false, fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		return ok, nil
	}

	base := globBase(keyPattern)
	res := &ListResult{}
	var matchedPrefixes []string
	seen := make(map[string]bool)

	err := a.walkPrefix(ctx, assetID, bucket, ossListPrefix(keyPattern), func(obj oss_svc.ObjectItem) error {
		matched, err := match(obj.Key)
		if err != nil {
			return err
		}
		if !matched {
			ancestor, err := matchedOSSAncestor(match, obj.Key)
			if err != nil {
				return err
			}
			if ancestor == "" {
				return nil
			}
			if !recursive {
				if !seen[ancestor] {
					seen[ancestor] = true
					matchedPrefixes = append(matchedPrefixes, ancestor)
				}
				return nil
			}
		}
		res.Entries = append(res.Entries, ossEntry(bucket, obj, base))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(matchedPrefixes) > 0 {
		return nil, fmt.Errorf("pattern %q matches prefixes (%s), which are not objects; set recursive to transfer everything under them",
			pattern, strings.Join(matchedPrefixes, ", "))
	}
	return res, nil
}

// walkPrefix 把 prefix 之下**任意深度**的对象逐页拉全，每个对象交给 visit 恰好一次。
//
// 两件事必须在这里做对：
//   - 列举是分层的（一次调用只看一层，下层作为公共前缀单独返回），所以下钻在这里；
//   - 分页游标是"上一页最后一条的 key"，而服务端先按游标过滤原始 key、再按 "/" 分组
//     （oss_svc/ops.go 的 listObjectsWith）。页边界恰好落在一个公共前缀上时，下一页会把
//     同一个前缀再交出来一次，照单全收就是把那棵子树展开两遍：审批弹窗里出现重复条目、
//     同一个对象被传两遍。按游标丢掉 <= 它的条目即可，这正是游标本身的 start-after 语义。
//
// 页大小不指定，用服务端默认值：分页策略只该有 oss_svc 一处真相。展开顺序是
// "本层对象、再逐个子树"，服务端每一页内按 key 字典序——同一份桶内容展开出的顺序固定，
// 审批弹窗里的条目顺序不会抖动（与 walkSFTP 排序是同一条理由）。
func (a ossAdapter) walkPrefix(
	ctx context.Context, assetID int64, bucket, prefix string, visit func(oss_svc.ObjectItem) error,
) error {
	token := ""
	for {
		res, err := a.svc.ListObjects(ctx, &oss_svc.ListObjectsRequest{
			AssetID: assetID, Bucket: bucket, Prefix: prefix, ContinuationToken: token,
		})
		if err != nil {
			return err
		}
		for _, obj := range res.Objects {
			if obj.Key <= token {
				continue
			}
			if err := visit(obj); err != nil {
				return err
			}
		}
		for _, sub := range res.Prefixes {
			if sub <= token {
				continue
			}
			if err := a.walkPrefix(ctx, assetID, bucket, sub, visit); err != nil {
				return err
			}
		}
		if !res.IsTruncated {
			return nil
		}
		token = res.NextContinuationToken
	}
}

// matchedOSSAncestor 返回 key 最浅的一个匹配通配的祖先前缀，没有则返回空串。
// 取最浅的那个是为了让下钻的基点与本地端一致：本地是"glob 命中目录 → 从那个目录整棵走"。
func matchedOSSAncestor(match func(string) (bool, error), key string) (string, error) {
	for i, c := range key {
		if c != '/' {
			continue
		}
		ancestor := key[:i]
		ok, err := match(ancestor)
		if err != nil {
			return "", err
		}
		if ok {
			return ancestor, nil
		}
	}
	return "", nil
}

func (a ossAdapter) OpenRead(
	ctx context.Context, asset *asset_entity.Asset, p string,
) (io.ReadCloser, int64, error) {
	bucket, key, err := splitOSSObjectPath(p)
	if err != nil {
		return nil, 0, err
	}
	return a.svc.GetObject(ctx, asset.ID, bucket, key)
}

// Write 流式写入一个对象。对象 key 是平的，没有要创建的中间目录；size 原样透传——
// 未知（-1）时 minio 走分片上传。内容类型交给服务端判定：cp 的两端只有路径，
// 猜一个 MIME 出来会盖掉服务端的判定（`object put --content-type=` 才是显式指定的入口）。
func (a ossAdapter) Write(
	ctx context.Context, asset *asset_entity.Asset, p string, r io.Reader, size int64,
) error {
	bucket, key, err := splitOSSObjectPath(p)
	if err != nil {
		return err
	}
	return a.svc.PutObject(ctx, asset.ID, bucket, key, r, size, "")
}

// ValidateDestination 只做形态校验：目的路径必须指名一个具体对象。
//
// 复用 splitOSSObjectPath —— 与 Write 用的是同一条规则，也就是同一份真相；另写一遍
// `<bucket>/<key>` 的判定只会与它漂移。这一关的价值全在**次序**上：`cp ./a.txt s3:/mybucket`
// 的主体（ossSubjectResource 的畸形回退）是一条谁也授权不了的惰性串，用户批准它之后
// Write 必然报同一个错，于是这次审批纯属白打断。放在审批之前，用户看到的是他真正需要
// 看到的那句话：目的地得写成 <bucket>/<key>。
func (ossAdapter) ValidateDestination(p string) error {
	_, _, err := splitOSSObjectPath(p)
	return err
}

// ApprovalSubject 交出 §3.3 的策略串 `<action> <bucket>/<key>`，与 exec 跑
// `object get` / `object put` / `object list` 时派生的串逐字节相同——两条入口的授权
// 因此互相复用（spec §6.2「逐端点审批」）。类型取 asset_entity.AssetTypeOSS，
// 即 permission 那张表里 oss 的登记名：它同时是权限检查的分派键与 grant 的 tool_name。
func (ossAdapter) ApprovalSubject(p string, dir Direction) (string, string) {
	action := "object.read"
	switch dir {
	case DirWrite:
		action = "object.write"
	case DirList:
		action = "object.list"
	}
	// 余下的是 DirRead。新增方向时必须在上面补一条 case，否则它会被当成读。
	return asset_entity.AssetTypeOSS, action + " " + ossSubjectResource(p, dir)
}

// ossSubjectResource 给出审批主体的资源段，它有一条硬后置条件：
// **这个资源当规则读时，绝不能覆盖这一端点之外的东西。**
//
// 主体是这条链上的**名字**：它先被 policy.MatchOSSRule 拿去撞策略与已落库的 grant，
// 之后才由 permission.NormalizeGrantPatterns 翻成规则落库（那一步做 §4.3 的前缀丢弃与
// D21 的转义）。所以这里交出的是原始 bucket/key，一个反斜杠都不加——名字侧转义会让规则
// 匹配不上自己（决策 D21 更正）。收窄是那一步的事，本函数只负责不说谎。
//
// 而按 policy.MatchOSSRule 的规则语义，桶段后为空（光桶名**或**以 "/" 收尾，D5 说这两种
// 写法等价）就是整桶任意深度，桶段里的 "*" 是跨桶通配。目的端路径不经过 List，所以形态
// 错误的目的地直接到这里（ValidateDestination 会在审批之前拦下它们，但主体是在那之前
// 生成的）。因此形态合法时资源是 <bucket>/<key>；形态不合法时退回**原样路径并强制带前导
// "/"**——这样的资源切出来的桶段是空串，既匹配不上任何桶范围的规则，自己当规则也匹配不上
// 任何真实资源，而用户在弹窗里看到的仍是他自己写的那个路径。这个接口不能报错，但它绝不能
// 因此交出一个比实际操作更宽的主体。
func ossSubjectResource(p string, dir Direction) string {
	if dir == DirList {
		if bucket, key, err := splitOSSEndpointPath(p); err == nil {
			return bucket + "/" + ossSubjectPrefix(key)
		}
	} else if bucket, key, err := splitOSSObjectPath(p); err == nil {
		return bucket + "/" + key
	}
	return "/" + strings.TrimPrefix(p, "/")
}

// ossSubjectPrefix 给出列举方向要授权的那个前缀。
//
// 用户**指名的就是一个前缀**（空串或以 "/" 收尾）时原样交出，不走 globBase：
// globBase 在第一个元字符处按整段截断，而 `logs[1]/` 这种字面量前缀的第一段就带元字符，
// 于是基点塌成空串，主体变成 `object.list mybucket/` —— 指名一个前缀却换来整桶列举的
// 常驻授权。前缀形态的规则 key 由 policy.matchOSSResource 走 strings.HasPrefix 字面
// 比较，元字符在那条路径上不是语法，所以原样交出既准确又不会越权。
//
// 其余形态（真正的通配、以及指名到某一层的 key）才归一成 ossListPrefix 的前缀——
// 那是 List 真的会去枚举的基点。
func ossSubjectPrefix(key string) string {
	if key == "" || strings.HasSuffix(key, "/") {
		return key
	}
	return ossListPrefix(key)
}

// cutOSSPath 把端点路径切成 (bucket, key)：剥掉前导 "/" 再按第一个 "/" 切。
// 端点写法是 <asset>:/<bucket>/<key>，而调用方拼出来的目的路径（<dst 基点> + RelPath）
// 不一定带前导 "/"，两条来路共用这一份切分。
func cutOSSPath(p string) (bucket, key string) {
	bucket, key, _ = strings.Cut(strings.TrimPrefix(p, "/"), "/")
	return bucket, key
}

// splitOSSEndpointPath 是任何形态的端点路径都要过的一关：桶名必须有，且不能带通配。
// 桶段不展开——那要先列举账号下的全部桶，是另一个操作、另一条策略串；报错好过
// 拿一个字面上带 "*" 的桶名去撞一句"桶不存在"。
func splitOSSEndpointPath(p string) (bucket, key string, err error) {
	bucket, key = cutOSSPath(p)
	if bucket == "" {
		return "", "", fmt.Errorf("oss path %q must name a bucket: write it as <asset>:/<bucket>/<key>", p)
	}
	if hasGlobMeta(bucket) {
		return "", "", fmt.Errorf("oss path %q cannot use a glob pattern in the bucket segment %q", p, bucket)
	}
	return bucket, key, nil
}

// splitOSSObjectPath 另外要求路径指名一个具体对象——读写的两端都必须是对象。
func splitOSSObjectPath(p string) (bucket, key string, err error) {
	bucket, key, err = splitOSSEndpointPath(p)
	if err != nil {
		return "", "", err
	}
	if key == "" || strings.HasSuffix(key, "/") {
		return "", "", fmt.Errorf(
			"oss path %q must name an object as <bucket>/<key>: a bare bucket name or a trailing \"/\" is a prefix, not an object", p)
	}
	return bucket, key, nil
}

// ossListPrefix 返回展开一个端点路径时实际要列举的前缀：通配取通配前的最后一层
// （globBase），其余原样，非空时补上尾随 "/"。
func ossListPrefix(key string) string {
	if hasGlobMeta(key) {
		key = globBase(key)
	}
	if key == "" || strings.HasSuffix(key, "/") {
		return key
	}
	return key + "/"
}

// ossEndpointPath 把 (bucket, key) 拼回端点路径形态，即用户自己写的那一种。
func ossEndpointPath(bucket, key string) string {
	return "/" + bucket + "/" + key
}

func ossEntry(bucket string, obj oss_svc.ObjectItem, base string) Entry {
	return Entry{Path: ossEndpointPath(bucket, obj.Key), RelPath: relTo(base, obj.Key), Size: obj.Size}
}
