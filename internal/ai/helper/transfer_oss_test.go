package helper

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/oss_svc"
)

const (
	ossTestAssetID = int64(42)
	ossTestBucket  = "mybucket"
)

// fakeOSSService 是 oss_svc.Service 的内存等价物，只实现适配器用到的那四个方法。
//
// 它刻意把**分组与分页**建模到位，因为那正是这个适配器要处理的两件事：
// 对象存储的列举是按 "/" 分层的（一层里既有对象也有公共前缀），分页游标是
// "上一页最后一条的 key" 且过滤发生在分组**之前**（oss_svc/ops.go 的 listObjectsWith
// + client_minio.go 的 toObjectItem）。少建模其中任何一条，测试都会盖不住
// 展开逻辑里最容易错的地方。
type fakeOSSService struct {
	assetID int64
	objects map[string]map[string]string // bucket → key → 内容
	gets    []string
	puts    []fakeOSSPut
}

type fakeOSSPut struct {
	bucket      string
	key         string
	content     string
	size        int64
	contentType string
}

// fakeOSSPageSize 与 oss_svc 的 defaultListMaxKeys 一致：MaxKeys<=0 时的服务端默认页大小。
const fakeOSSPageSize = 200

func (f *fakeOSSService) checkAsset(assetID int64) error {
	if assetID != f.assetID {
		return fmt.Errorf("fake oss: asset id = %d, want %d", assetID, f.assetID)
	}
	return nil
}

func (f *fakeOSSService) ListObjects(
	_ context.Context, req *oss_svc.ListObjectsRequest,
) (*oss_svc.ListObjectsResult, error) {
	if err := f.checkAsset(req.AssetID); err != nil {
		return nil, err
	}
	limit := req.MaxKeys
	if limit <= 0 {
		limit = fakeOSSPageSize
	}

	// 原始 key 先按游标过滤、再按 "/" 分组——顺序与真实服务端一致，也正因为如此，
	// 页边界恰好落在一个公共前缀上时，下一页会把同一个前缀再交出来一次。
	keys := slices.Sorted(maps.Keys(f.objects[req.Bucket]))
	items := make([]oss_svc.ObjectItem, 0, len(keys))
	lastPrefix := ""
	for _, k := range keys {
		if !strings.HasPrefix(k, req.Prefix) {
			continue
		}
		if req.ContinuationToken != "" && k <= req.ContinuationToken {
			continue
		}
		if i := strings.Index(k[len(req.Prefix):], "/"); i >= 0 {
			commonPrefix := k[:len(req.Prefix)+i+1]
			if commonPrefix == lastPrefix {
				continue
			}
			lastPrefix = commonPrefix
			items = append(items, oss_svc.ObjectItem{Key: commonPrefix, IsPrefix: true})
			continue
		}
		item := oss_svc.ObjectItem{Key: k, Size: int64(len(f.objects[req.Bucket][k]))}
		// 与 toObjectItem 一致：零字节且以 "/" 结尾的 key 是"文件夹"标记，按前缀报。
		item.IsPrefix = item.Size == 0 && strings.HasSuffix(k, "/")
		items = append(items, item)
	}

	res := &oss_svc.ListObjectsResult{Prefixes: []string{}, Objects: []oss_svc.ObjectItem{}}
	if len(items) > limit {
		res.IsTruncated = true
		res.NextContinuationToken = items[limit-1].Key
		items = items[:limit]
	}
	for _, it := range items {
		switch {
		case it.IsPrefix && it.Key == req.Prefix: // listObjectsWith 丢掉被列举的前缀自身
		case it.IsPrefix:
			res.Prefixes = append(res.Prefixes, it.Key)
		default:
			res.Objects = append(res.Objects, it)
		}
	}
	return res, nil
}

func (f *fakeOSSService) StatObject(_ context.Context, req *oss_svc.ObjectRequest) (*oss_svc.ObjectItem, error) {
	if err := f.checkAsset(req.AssetID); err != nil {
		return nil, err
	}
	content, ok := f.objects[req.Bucket][req.Key]
	if !ok {
		return nil, fmt.Errorf("fake oss: no such key %q in bucket %q", req.Key, req.Bucket)
	}
	return &oss_svc.ObjectItem{Key: req.Key, Size: int64(len(content))}, nil
}

func (f *fakeOSSService) GetObject(
	_ context.Context, assetID int64, bucket, key string,
) (io.ReadCloser, int64, error) {
	if err := f.checkAsset(assetID); err != nil {
		return nil, 0, err
	}
	f.gets = append(f.gets, bucket+"/"+key)
	content, ok := f.objects[bucket][key]
	if !ok {
		return nil, 0, fmt.Errorf("fake oss: no such key %q in bucket %q", key, bucket)
	}
	return io.NopCloser(strings.NewReader(content)), int64(len(content)), nil
}

func (f *fakeOSSService) PutObject(
	_ context.Context, assetID int64, bucket, key string, r io.Reader, size int64, contentType string,
) error {
	if err := f.checkAsset(assetID); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.puts = append(f.puts, fakeOSSPut{
		bucket: bucket, key: key, content: string(data), size: size, contentType: contentType,
	})
	if f.objects[bucket] == nil {
		f.objects[bucket] = make(map[string]string)
	}
	f.objects[bucket][key] = string(data)
	return nil
}

// newOSSTest 起一个装着 objects（key → 内容）的单桶端点。
func newOSSTest(objects map[string]string) (ossAdapter, *fakeOSSService, *asset_entity.Asset) {
	fake := &fakeOSSService{
		assetID: ossTestAssetID,
		objects: map[string]map[string]string{ossTestBucket: objects},
	}
	asset := &asset_entity.Asset{ID: ossTestAssetID, Type: asset_entity.AssetTypeOSS}
	return ossAdapter{svc: fake}, fake, asset
}

// --- 展开 ---

// 单个具体对象展开成一条 entry，基点是它所在的前缀，因此 RelPath 就是对象名——
// `cp s3-prod:/mybucket/logs/app.log /tmp/` 靠这条拼出 /tmp/app.log。
func TestOSSTransferList_SingleObject(t *testing.T) {
	adapter, _, asset := newOSSTest(map[string]string{"logs/app.log": "hello"})

	got, err := adapter.List(context.Background(), asset, "/mybucket/logs/app.log", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %v", relPaths(got.Entries))
	}
	e := got.Entries[0]
	if e.Path != "/mybucket/logs/app.log" {
		t.Errorf("Path = %q", e.Path)
	}
	if e.RelPath != "app.log" {
		t.Errorf("RelPath = %q, want %q", e.RelPath, "app.log")
	}
	if e.Size != 5 {
		t.Errorf("Size = %d, want 5", e.Size)
	}
}

// 形态校验（spec §6.2「端点路径语法」）：剥掉前导 "/" 之后只有桶名、或以 "/" 收尾的路径
// 是**前缀**不是对象。不加 recursive 就报错，不猜——与本地端"指名目录却没有 recursive"
// 同一个答案。
func TestOSSTransferList_PrefixWithoutRecursive(t *testing.T) {
	adapter, _, asset := newOSSTest(map[string]string{"logs/app.log": "hello"})

	for _, p := range []string{"/mybucket", "/mybucket/", "/mybucket/logs/"} {
		_, err := adapter.List(context.Background(), asset, p, false)
		if err == nil {
			t.Fatalf("%q: expected an error listing a prefix without recursive", p)
		}
		if !strings.Contains(err.Error(), "prefix") || !strings.Contains(err.Error(), "recursive") {
			t.Errorf("%q: error should name the prefix case and recursive, got %q", p, err)
		}
	}
}

// 桶名缺失一律报错；桶段也不做通配展开（那要先列举账号下的全部桶，是另一个操作、
// 另一条策略串），且必须**说清楚是这件事**——否则用户只会拿到一句对着字面桶名 "*" 的
// "桶不存在"，看不出通配没被展开。
func TestOSSTransferList_RejectsMalformedBucket(t *testing.T) {
	adapter, _, asset := newOSSTest(map[string]string{"logs/app.log": "hello"})

	for _, p := range []string{"", "/"} {
		if _, err := adapter.List(context.Background(), asset, p, true); err == nil {
			t.Errorf("%q: expected an error, got none", p)
		}
	}

	_, err := adapter.List(context.Background(), asset, "/*/logs/app.log", true)
	if err == nil || !strings.Contains(err.Error(), "glob") {
		t.Errorf("a glob in the bucket segment should be rejected as such, got %v", err)
	}
}

// 递归展开按前缀拉全，基点是被指名的前缀本身（spec §6.5），RelPath 因此保留层级；
// 零字节的"文件夹"标记不是可传输条目，与本地端不产出目录条目一致。
func TestOSSTransferList_RecursivePrefix(t *testing.T) {
	objects := map[string]string{
		"logs/":              "",
		"logs/a.log":         "aa",
		"logs/sub/":          "",
		"logs/sub/b.log":     "bbb",
		"logs/sub/deep/c.go": "cccc",
		"other/x.log":        "no",
	}

	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{"prefix", "/mybucket/logs/", "a.log,sub/b.log,sub/deep/c.go"},
		{"bucket root", "/mybucket", "logs/a.log,logs/sub/b.log,logs/sub/deep/c.go,other/x.log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, _, asset := newOSSTest(objects)

			got, err := adapter.List(context.Background(), asset, tt.pattern, true)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if joinRel(got.Entries) != tt.want {
				t.Fatalf("RelPaths = %v, want %v", relPaths(got.Entries), tt.want)
			}
			if got.Entries[0].Path != "/mybucket/logs/a.log" {
				t.Errorf("Path = %q, want %q", got.Entries[0].Path, "/mybucket/logs/a.log")
			}
			if got.Entries[0].Size != 2 {
				t.Errorf("Size = %d, want 2", got.Entries[0].Size)
			}
		})
	}
}

// glob 在客户端按 path.Match 过滤，因此 '*' 不跨 "/"：dist/*.js 命中 dist 这一层的 .js，
// 不命中 dist/sub/ 下的同名后缀。基点是通配前的最后一层前缀。
func TestOSSTransferList_GlobDoesNotCrossSlash(t *testing.T) {
	adapter, _, asset := newOSSTest(map[string]string{
		"dist/a.js":     "a",
		"dist/b.css":    "bb",
		"dist/sub/c.js": "ccc",
	})

	got, err := adapter.List(context.Background(), asset, "/mybucket/dist/*.js", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if joinRel(got.Entries) != "a.js" {
		t.Fatalf("RelPaths = %v, want [a.js]", relPaths(got.Entries))
	}
	if got.Entries[0].Path != "/mybucket/dist/a.js" {
		t.Errorf("Path = %q", got.Entries[0].Path)
	}
}

// 通配命中前缀时：递归就下钻（基点仍是通配前的最后一层，所以 RelPath 带上前缀名），
// 非递归则报错并点名那些前缀——静默略过就是一次看起来成功、实际只传了一部分的传输。
func TestOSSTransferList_GlobMatchingPrefix(t *testing.T) {
	objects := map[string]string{
		"dist/a.js":          "a",
		"dist/sub/c.js":      "ccc",
		"dist/sub/deep/d.js": "dddd",
	}

	adapter, _, asset := newOSSTest(objects)
	got, err := adapter.List(context.Background(), asset, "/mybucket/dist/*", true)
	if err != nil {
		t.Fatalf("List recursive: %v", err)
	}
	if joinRel(got.Entries) != "a.js,sub/c.js,sub/deep/d.js" {
		t.Fatalf("RelPaths = %v", relPaths(got.Entries))
	}

	_, err = adapter.List(context.Background(), asset, "/mybucket/dist/*", false)
	if err == nil {
		t.Fatal("expected an error: the pattern matches a prefix and recursive is off")
	}
	if !strings.Contains(err.Error(), "dist/sub") || !strings.Contains(err.Error(), "recursive") {
		t.Errorf("error should name the matched prefix and recursive, got %q", err)
	}
}

// 一条都没匹配上不是错误——"什么都没匹配上"由调用方决定怎么报。
func TestOSSTransferList_GlobMatchesNothing(t *testing.T) {
	adapter, _, asset := newOSSTest(map[string]string{"dist/a.js": "a"})

	got, err := adapter.List(context.Background(), asset, "/mybucket/dist/*.map", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("entries = %v, want none", relPaths(got.Entries))
	}
}

// 展开必须把前缀下的每一页都拉全，且**恰好**一次。
//
// 页边界落在公共前缀上时服务端会把它再交出来一次（游标是"上一页最后一条 key"，
// 而过滤发生在按 "/" 分组之前），照单全收就会把整棵子树展开两遍：审批弹窗上出现
// 重复条目，同一个对象被传两次。
func TestOSSTransferList_PaginatesWithoutRepeatingPrefixAtPageBoundary(t *testing.T) {
	objects := make(map[string]string, fakeOSSPageSize+10)
	for i := range fakeOSSPageSize - 1 { // logs/000.txt … logs/198.txt，本层第 1..199 条
		objects[fmt.Sprintf("logs/%03d.txt", i)] = "x"
	}
	// 本层第 200 条（即页边界那一条）是个公共前缀。
	objects["logs/199dir/x.txt"] = "x"
	objects["logs/199dir/y.txt"] = "x"
	for i := 200; i < 205; i++ { // 第二页的内容
		objects[fmt.Sprintf("logs/%03d.txt", i)] = "x"
	}
	adapter, _, asset := newOSSTest(objects)

	got, err := adapter.List(context.Background(), asset, "/mybucket/logs/", true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Entries) != len(objects) {
		t.Fatalf("expanded %d entries, want %d", len(got.Entries), len(objects))
	}
	seen := make(map[string]int, len(got.Entries))
	for _, e := range got.Entries {
		seen[e.RelPath]++
	}
	for _, rel := range []string{"000.txt", "199dir/x.txt", "204.txt"} {
		if seen[rel] != 1 {
			t.Errorf("RelPath %q appeared %d times, want 1", rel, seen[rel])
		}
	}
}

// --- 流式读写 ---

// 对象 key 是平的，写入不需要建父目录；size 原样透传（-1 交给服务端走分片）。
func TestOSSTransfer_WriteAndOpenReadRoundTrip(t *testing.T) {
	ctx := context.Background()
	adapter, fake, asset := newOSSTest(map[string]string{})

	if err := adapter.Write(ctx, asset, "/mybucket/new/deep/f.txt", strings.NewReader("payload"), -1); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(fake.puts) != 1 {
		t.Fatalf("puts = %+v, want exactly one", fake.puts)
	}
	put := fake.puts[0]
	if put.bucket != ossTestBucket || put.key != "new/deep/f.txt" {
		t.Errorf("put target = %q/%q", put.bucket, put.key)
	}
	if put.content != "payload" {
		t.Errorf("put content = %q", put.content)
	}
	if put.size != -1 {
		t.Errorf("put size = %d, want -1 passed through", put.size)
	}

	r, size, err := adapter.OpenRead(ctx, asset, "/mybucket/new/deep/f.txt")
	if err != nil {
		t.Fatalf("OpenRead: %v", err)
	}
	defer func() { _ = r.Close() }()
	if size != 7 {
		t.Errorf("size = %d, want 7", size)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("content = %q", data)
	}
}

// 读写两端都必须指名一个对象：桶名单独一个、或以 "/" 收尾的路径是前缀，桶段带通配的
// 路径根本没指名哪个桶。形态错的路径必须在**够到服务端之前**就失败——空 key 的读注定
// 失败（"批准之后必然失败"的调用），而以 "/" 收尾的写会凭空造出一个零字节的"文件夹"标记。
//
// key 段里的元字符**不**在此列：`dist/*` 展开出来的 `dist/a[1].js` 是一个货真价实的
// 对象 key（path.Match 只把模式侧的元字符当通配），拒了它就是递归展开传不动自己列出来的东西。
func TestOSSTransfer_ReadWriteRequireAnObjectPath(t *testing.T) {
	ctx := context.Background()

	for _, p := range []string{"", "/", "/mybucket", "/mybucket/", "/mybucket/logs/", "/*/logs/app.log"} {
		adapter, fake, asset := newOSSTest(map[string]string{"logs/app.log": "hello"})

		if _, _, err := adapter.OpenRead(ctx, asset, p); err == nil {
			t.Errorf("OpenRead(%q): expected an error", p)
		}
		if err := adapter.Write(ctx, asset, p, strings.NewReader("x"), 1); err == nil {
			t.Errorf("Write(%q): expected an error", p)
		}
		if len(fake.gets) != 0 || len(fake.puts) != 0 {
			t.Errorf("%q reached the service: gets=%v puts=%+v, want none", p, fake.gets, fake.puts)
		}
	}
}

// --- 审批主体 ---

// 三个方向各自的主体（spec §6.2 的表格）。读写是具体对象，列举是前缀形态——
// 因此通配的基点被归一成前缀，且恒以 "/" 收尾（§3.4 里"以 / 收尾"= 该前缀下任意深度）。
func TestOSSTransfer_ApprovalSubject(t *testing.T) {
	tests := []struct {
		name string
		path string
		dir  Direction
		want string
	}{
		{"read an object", "/mybucket/logs/app.log", DirRead, "object.read mybucket/logs/app.log"},
		{"write an object", "/mybucket/logs/app.log", DirWrite, "object.write mybucket/logs/app.log"},
		{"key with interior spaces", "/mybucket/My Report.pdf", DirRead, "object.read mybucket/My Report.pdf"},
		{"list a prefix", "/mybucket/logs/", DirList, "object.list mybucket/logs/"},
		{"list a prefix without its trailing slash", "/mybucket/logs", DirList, "object.list mybucket/logs/"},
		{"list a glob is listing its base", "/mybucket/dist/*.js", DirList, "object.list mybucket/dist/"},
		{"list a whole bucket", "/mybucket", DirList, "object.list mybucket/"},
		// key 段里的 `* ? [ \` 是字面量，而主体会原样成为规则（决策 D21）：不转义的话
		// `dist/a[1].js` 这一个对象换来的授权覆盖 `dist/a1.js`。范围本身由
		// TestOSSTransfer_ApprovalSubjectNeverOutgrowsItsPath 咬住，这里只锁主体的形状。
		{"a key with a glob metacharacter", "/mybucket/dist/a[1].js", DirRead, `object.read mybucket/dist/a\[1].js`},
		{"a destination key with a glob metacharacter", "/mybucket/secrets*", DirWrite, `object.write mybucket/secrets\*`},
		// 列举方向**不**转义：ossListPrefix 交出来的 key 段恒是前缀（空串或以 "/" 收尾），
		// 而规则侧对这种 key 走的是 strings.HasPrefix 字面比较而不是 path.Match
		// （policy.matchOSSResource），那条路径上没有转义语法。转义过的前缀落成 grant
		// 之后一条真实的 key 都盖不住——范围本身由
		// TestOSSTransfer_ApprovalSubjectNeverOutgrowsItsPath 的 listing 子用例咬住。
		{"list a prefix with a backslash in it", `/mybucket/a\b/`, DirList, `object.list mybucket/a\b/`},
		// 目的地写成一个桶名是形态错误，Write 会报错；但主体是在那之前生成的。它既不能是
		// "object.write mybucket" 也不能是 "object.write mybucket/"——按 D5 这两种写法
		// 等价，都是**整桶可写**。资源退回原样路径，切不出桶名，因此谁也授权不了；
		// 覆盖范围本身由 TestOSSTransfer_ApprovalSubjectNeverOutgrowsItsPath 咬住。
		{"a keyless destination names no object", "/mybucket", DirWrite, "object.write /mybucket"},
		{"a glob in the bucket segment names no bucket", "/*/logs/app.log", DirRead, "object.read /*/logs/app.log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, subject := ossTransfer.ApprovalSubject(tt.path, tt.dir)
			if typ != asset_entity.AssetTypeOSS {
				t.Errorf("type = %q, want %q", typ, asset_entity.AssetTypeOSS)
			}
			if subject != tt.want {
				t.Errorf("subject = %q, want %q", subject, tt.want)
			}
			// 主体要能被策略规则匹配：MatchOSSRule 对 "*" 只在命令切得出
			// "<action> <resource>" 两段时才放行，切不出两段的串对任何规则
			// （allow 与 deny）都失配——deny 一条都匹配不上就是静默的 fail-open。
			if !policy.MatchOSSRule("*", subject) {
				t.Errorf("subject %q does not split into an <action> <resource> policy string", subject)
			}
		})
	}
}

// 主体当规则读时，绝不能覆盖它描述的那一条传输之外的东西。
//
// 这不是形式检查，而是这个接口唯一的安全后置条件。主体串会**原样落成常驻 grant**：
// permission.ossGrantPatterns 对已经是策略串形状的输入不做 §4.3 的前缀丢弃（那条丢弃只
// 管从 DSL 派生出来的串），于是 ApprovalSubject 交出什么就落库什么。而同一个串当规则读时，
// 桶名后为空——光桶名或以 "/" 收尾——意味着整桶任意深度（决策 D5，两种写法等价），
// key 段里的 "*" 是通配。目的端路径不经过 List，因此形态错误的目的地直接到这里：
// `cp ./a.txt s3:/mybucket` 一旦被点"始终允许"，换来的就是一条整桶可写的常驻授权。
//
// 参与比对的是两个独立来源：本适配器给出的主体，与 policy.MatchOSSRule 的规则语义。
func TestOSSTransfer_ApprovalSubjectNeverOutgrowsItsPath(t *testing.T) {
	// 一组形态良好的策略串，用来反问一条 grant"你还授权了什么"。
	probe := []string{
		"object.read mybucket/other.txt",
		"object.write mybucket/other.txt",
		"object.read mybucket/secrets/key.pem",
		"object.write mybucket/secrets/key.pem",
		"object.read mybucket/logs/other/deep.log",
		"object.write mybucket/logs/other/deep.log",
		"object.list mybucket/secrets/",
		// 与下面那组带元字符的 key 配套：这些是**别的**对象，只有把主体当通配读才会命中。
		"object.read mybucket/dist/a1.js",
		"object.write mybucket/dist/a1.js",
		"object.read mybucket/secretsFOO",
		"object.write mybucket/secretsFOO",
		"object.read otherbucket/logs/app.log",
		"object.write otherbucket/logs/app.log",
		"object.list otherbucket/logs/",
	}
	assertGrantsNothingElse := func(t *testing.T, subject string) []string {
		t.Helper()
		patterns := permission.NormalizeGrantPatterns(asset_entity.AssetTypeOSS, subject)
		for _, pattern := range patterns {
			for _, other := range probe {
				if policy.MatchOSSRule(pattern, other) {
					t.Errorf("subject %q lands as grant %q, which also authorizes %q", subject, pattern, other)
				}
			}
		}
		return patterns
	}

	// 形态错误的路径读写不出任何字节（OpenRead / Write 都报错），所以它的主体一条授权
	// 都不该换来。这些路径全都到不了 List，只能是调用方直接拼出来的目的地。
	for _, tt := range []struct {
		name string
		path string
		dir  Direction
	}{
		{"a bare bucket destination", "/mybucket", DirWrite},
		{"a trailing-slash destination", "/mybucket/logs/", DirWrite},
		{"a glob in the destination bucket segment", "/*/logs/app.log", DirWrite},
		{"a bare bucket source", "/mybucket", DirRead},
		{"a glob in the listed bucket segment", "/*/logs/", DirList},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, subject := ossTransfer.ApprovalSubject(tt.path, tt.dir)
			assertGrantsNothingElse(t, subject)
		})
	}

	// 反过来，指名一个对象时授权照落——否则"什么都不授权"是个廉价的通过方式。
	t.Run("a named object still grants exactly itself", func(t *testing.T) {
		_, subject := ossTransfer.ApprovalSubject("/mybucket/logs/app.log", DirWrite)
		patterns := assertGrantsNothingElse(t, subject)
		if len(patterns) != 1 || !policy.MatchOSSRule(patterns[0], "object.write mybucket/logs/app.log") {
			t.Fatalf("grants = %v, want exactly one authorizing the transferred object", patterns)
		}
	})

	// S3 的 key 允许字面量 `* ? [`，而 §3.4 的规则语法没有转义约定——于是"批准传这一个
	// 对象"派生出的主体当规则读时比这个对象宽（决策 D21）。递归展开本来就会合法产出这种
	// key（`dist/*` 命中 `dist/a[1].js`），所以拒绝它们不是选项：主体必须自己收窄。
	t.Run("a key with glob metacharacters grants only that key", func(t *testing.T) {
		for _, p := range []string{"/mybucket/dist/a[1].js", "/mybucket/secrets*"} {
			for _, dir := range []Direction{DirRead, DirWrite} {
				_, subject := ossTransfer.ApprovalSubject(p, dir)
				if patterns := assertGrantsNothingElse(t, subject); len(patterns) == 0 {
					t.Fatalf("subject %q lands as no grant at all; the assertion above is vacuous", subject)
				}
			}
		}
	})

	// 展开授权的范围本来就是一个前缀（D18），它落成的 grant 覆盖该前缀下任意深度是对的，
	// 但不能越出那个前缀。
	//
	// 带字面量元字符的前缀走的是同一条断言，而**不是** D21 的转义：以 "/" 收尾的规则 key
	// 由 policy.matchOSSResource 用 strings.HasPrefix 字面比较，那条路径上没有转义语法，
	// 转义过的前缀谁也匹配不上——"始终允许"于是变成一条什么都不授权的死 grant。
	for _, tt := range []struct{ name, path, covers string }{
		{"listing a prefix grants that prefix and no other", "/mybucket/logs/",
			"object.list mybucket/logs/sub/deep/"},
		{"listing a prefix with a backslash in it", `/mybucket/a\b/`,
			`object.list mybucket/a\b/sub/deep/`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, subject := ossTransfer.ApprovalSubject(tt.path, DirList)
			patterns := assertGrantsNothingElse(t, subject)
			if len(patterns) != 1 || !policy.MatchOSSRule(patterns[0], tt.covers) {
				t.Fatalf("grants = %v, want exactly one covering the enumerated prefix %q", patterns, tt.covers)
			}
		})
	}
}

// oss 资产必须能在注册表里查到传输适配器，否则 cp 的九种组合里带 oss 的那五种全都够不着。
func TestOSSTransferAdapterIsRegistered(t *testing.T) {
	got, err := TransferAdapterFor(&asset_entity.Asset{Type: asset_entity.AssetTypeOSS})
	if err != nil {
		t.Fatalf("TransferAdapterFor(oss): %v", err)
	}
	if got != ossTransfer {
		t.Errorf("registered adapter = %T, want the oss adapter", got)
	}
}
