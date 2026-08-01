package helper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/oss_svc"
)

const (
	// ossDefaultInlineBytes / ossMaxInlineBytes 界定 `object get` 不带 --file 时读回多少
	// 字节。目的是让模型能直接读小的配置/日志对象而不必先落盘，不是让它把一个对象整个
	// 拉进上下文——超出即截断并把 truncated 标出来，提示改用 --file（spec §5）。
	ossDefaultInlineBytes = 64 << 10
	ossMaxInlineBytes     = 1 << 20

	// ossDefaultPresignExpirySecs 是 `object presign` 不带 --expiry 时用的有效期。
	//
	// 这一层自己持有默认值、并且**总是显式传给 oss_svc**，是因为返回值要报出实际生效的
	// 有效期：oss_svc.presignExpiry 的默认值未导出，传 0 就只能在 JSON 里猜一个数写上去。
	// 显式传值让 oss_svc 那条默认分支在本路径上永远不触发，报出的数就是用上的数。
	ossDefaultPresignExpirySecs = 3600
)

// ossExecService 是 exec 面用到的那一片 oss_svc.Service，*oss_svc.Service 恰好满足。
// 声明成接口的理由与传输面的 ossTransferService 相同：执行器的全部难点是"DSL → 服务
// 调用"的翻译（target 切成 bucket/key、flag 落到哪个字段、presign 走 GET 还是 PUT），
// 跑不起对象存储服务端就等于测不到它。
type ossExecService interface {
	ListBuckets(ctx context.Context, assetID int64) ([]oss_svc.BucketItem, error)
	ListObjects(ctx context.Context, req *oss_svc.ListObjectsRequest) (*oss_svc.ListObjectsResult, error)
	StatObject(ctx context.Context, req *oss_svc.ObjectRequest) (*oss_svc.ObjectItem, error)
	RemoveObject(ctx context.Context, req *oss_svc.ObjectRequest) error
	CopyObject(ctx context.Context, req *oss_svc.CopyRequest) error
	MoveObject(ctx context.Context, req *oss_svc.CopyRequest) error
	GetObject(ctx context.Context, assetID int64, bucket, key string) (io.ReadCloser, int64, error)
	PutObject(ctx context.Context, assetID int64, bucket, key string, r io.Reader, size int64, contentType string) error
	PresignGet(ctx context.Context, req *oss_svc.PresignRequest) (string, error)
	PresignPut(ctx context.Context, req *oss_svc.PresignRequest) (string, error)
}

type ossExecutor struct{ svc ossExecService }

var ossExec = ossExecutor{svc: oss_svc.New()}

// ExecOSSOnAsset is the permission-check-free execution entry point used by the unified
// exec tool; handleExec checks permission before calling it. scope is meaningless for
// object storage and is ignored — the target position names bucket and key (see
// internal/ai/skills/oss/SKILL.md).
//
// 分派到 oss_svc 而不是直连 minio：凭据解析、连接池与分页/移动语义都在 service 层写好
// 且有测试，重写一遍就是 AGENTS.md 说的第二份实现。
func ExecOSSOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	return ossExec.run(ctx, asset, command)
}

// CanonicalizeOSSCommand 把模型给的命令规范化为**规范 DSL 串**（决策 D6）：审批弹窗与
// audit_logs.command 展示的就是它，而策略与 grant 匹配用的是 PolicyStrings() 派生出的
// 两段串（OSS 一条命令可派生 1~3 条，单串承载不了）。
//
// 它同时是 exec 面唯一的输入闸门，两件事都排在权限检查之前（handleExec 的顺序是
// canonicalize → precheck → 权限检查）：
//
//   - 语法错误、未知 flag、缺必填 flag、相对 --file 都必然导致执行失败，必须在弹审批
//     对话框之前失败，否则用户先被打断批准一次（选"始终允许"还会落一条常驻 grant），
//     命令才失败；
//   - **策略串形状的输入被挡在这里**。permission.checkOSSPermission 靠"首词含 '.'"区分
//     "已派生的策略串"（cp 的端点主体、用户手写的 pattern）与"DSL 命令"，因此一条
//     `object.read mybucket/*` 从 exec 面递进去会绕过 DSL 解析器直接参与规则匹配，
//     撞上一条整桶 allow 就被判成 Allow 并落成 grant，之后才在执行器里因为解析不了而失败。
//     ParseOSSCommand 只认 bucket / object 两个 family，`object.read` 在这里就报错。
func CanonicalizeOSSCommand(_ *asset_entity.Asset, command string) (string, error) {
	c, err := resolveOSSCommand(command)
	if err != nil {
		return "", err
	}
	return c.Render()
}

// resolveOSSCommand 解析并做"解析器够不着、但执行前就能判定"的那一层校验。
//
// 规范化路径与执行路径必须都调它（kafka 的 resolveKafkaCommand 同形）：统一 exec 把
// **原始**命令交给执行器、把**规范化后**的串交给权限检查，只在一边校验就等于没校验。
func resolveOSSCommand(command string) (*OSSCommand, error) {
	c, err := ParseOSSCommand(command)
	if err != nil {
		return nil, err
	}
	if err := validateOSSFileFlags(c); err != nil {
		return nil, err
	}
	return c, nil
}

// validateOSSFileFlags 校验 --file 的两条规则：解析器够不着（它只看单个 flag 的值形状），
// 但执行之前就判定得了，所以两条都在这里、也就都排在审批弹窗之前。
//
//   - **必须是绝对路径**（决策 D11）。规则不属于 DSL 的语法而属于这条入口：审批弹窗与
//     audit_logs.command 记的是这条命令串本身，而相对路径要靠一个串里看不见的工作目录
//     才能定位——批准的字符串因此指不到一个确定的文件。§3.1 的语法写的就是
//     `--file=<absolute-local-path>`，两个入口一视同仁；opsctl 的相对路径手感由 cp 保留
//     （D11 说的是 `opsctl cp ./a.txt`，那条路径在入口层展开成绝对路径后才进适配器）。
//   - **不与 --max-bytes 同时出现**。两者是 `object get` 的两种行为而不是一件事的两个
//     旋钮（spec §5）：带 --file 是整个对象流式落盘，--max-bytes 只截断内联返回的内容。
//     一起给就有一个必然被丢掉，而审批弹窗上两个都在——正是 ossFlagRules 那条"未知 flag
//     宁可报错也不静默丢弃"要防的同一件事（用户批准的和发生的不是一件事）。
func validateOSSFileFlags(c *OSSCommand) error {
	file, ok := c.Flags["file"]
	if !ok {
		return nil
	}
	if !filepath.IsAbs(file) {
		return fmt.Errorf("--file must be an absolute path, got %q: the approved command string is all that "+
			"identifies the file, and a relative path resolves against a working directory that is not part "+
			"of it, so it would land somewhere unpredictable", file)
	}
	if _, ok := c.Flags["max-bytes"]; ok {
		return fmt.Errorf("--file and --max-bytes cannot be combined: --file streams the whole object to disk, " +
			"while --max-bytes only caps the content returned inline, so one of the two would be silently " +
			"ignored; drop --max-bytes to write the file, or drop --file to read the first bytes inline")
	}
	return nil
}

func (e ossExecutor) run(ctx context.Context, asset *asset_entity.Asset, command string) (string, error) {
	c, err := resolveOSSCommand(command)
	if err != nil {
		return "", err
	}
	handle, ok := ossVerbHandlers[c.Family+" "+c.Verb]
	if !ok {
		return "", fmt.Errorf("unknown oss command %q %q; supported: %s",
			c.Family, c.Verb, strings.Join(slices.Sorted(maps.Keys(ossVerbHandlers)), ", "))
	}
	return handle(e, ctx, asset, c)
}

// ossVerbHandlers 把 DSL 的 (family, verb) 映射到执行它的方法。
//
// 写成表而不是 switch，理由与 kafkaFamilyHandlers 一致：这份 wiring 因此可以被直接断言
// （TestOSSVerbHandlers_MatchTheDSLVerbTable 拿它与 ossVerbs 对比）。两个方向的漂移都
// 只在运行期显形——少一条，命令解析通过、审批弹窗照弹、批准之后才报"unknown oss
// command"；多一条，一段谁也调不到的执行代码挂在那里。
var ossVerbHandlers = map[string]func(ossExecutor, context.Context, *asset_entity.Asset, *OSSCommand) (string, error){
	"bucket list":    ossExecutor.listBuckets,
	"object list":    ossExecutor.listObjects,
	"object stat":    ossExecutor.statObject,
	"object get":     ossExecutor.getObject,
	"object put":     ossExecutor.putObject,
	"object copy":    ossExecutor.copyObject,
	"object move":    ossExecutor.moveObject,
	"object delete":  ossExecutor.deleteObject,
	"object presign": ossExecutor.presignObject,
}

func (e ossExecutor) listBuckets(ctx context.Context, asset *asset_entity.Asset, _ *OSSCommand) (string, error) {
	buckets, err := e.svc.ListBuckets(ctx, asset.ID)
	if err != nil {
		return "", err
	}
	return marshalOSSResult(ctx, "bucket list", map[string]any{"buckets": buckets})
}

func (e ossExecutor) listObjects(ctx context.Context, asset *asset_entity.Asset, c *OSSCommand) (string, error) {
	bucket, prefix := cutOSSPath(c.Target)
	maxKeys, err := ossIntFlag(c, "max-keys")
	if err != nil {
		return "", err
	}
	// --after 就是续传游标：结果里的 nextContinuationToken 原样填回来即可翻下一页。
	res, err := e.svc.ListObjects(ctx, &oss_svc.ListObjectsRequest{
		AssetID: asset.ID, Bucket: bucket, Prefix: prefix,
		MaxKeys: maxKeys, ContinuationToken: c.Flags["after"],
	})
	if err != nil {
		return "", err
	}
	return marshalOSSResult(ctx, "object list", res)
}

func (e ossExecutor) statObject(ctx context.Context, asset *asset_entity.Asset, c *OSSCommand) (string, error) {
	bucket, key := cutOSSPath(c.Target)
	item, err := e.svc.StatObject(ctx, &oss_svc.ObjectRequest{AssetID: asset.ID, Bucket: bucket, Key: key})
	if err != nil {
		return "", err
	}
	return marshalOSSResult(ctx, "object stat", item)
}

// getObject 有两种行为，由 --file 的有无决定，不是同一件事的两种输出（spec §5）：
// 带 --file 是一次传输，字节落盘；不带是把（小的）对象内容直接读给模型看。
func (e ossExecutor) getObject(ctx context.Context, asset *asset_entity.Asset, c *OSSCommand) (string, error) {
	bucket, key := cutOSSPath(c.Target)
	if file := c.Flags["file"]; file != "" {
		return e.downloadObject(ctx, asset, bucket, key, file)
	}
	return e.readObjectInline(ctx, asset, bucket, key, c)
}

// downloadObject 走传输接缝的本地端（localAdapter.Write：按需建父目录 + 流式落盘），
// 不自己写第二份"打开并写一个本地文件"。
//
// 落盘之前先过 gateExecLocalWrite：这条命令与 cp 是同一个原语（往本机任意路径写文件），
// 而 `object.read *` 是内置默认策略里就有的一条，因此它同样能在一个弹框都不弹的情况下
// 写穿本机。门禁排在 GetObject 之前——被拒的下载不该先把对象读出来。
//
// OSS 那一端直接用 oss_svc.GetObject，而不是 ossAdapter.OpenRead：后者只是同一个调用
// 外加一次 <bucket>/<key> 路径再解析（DSL 已经解析过了），而它多出来的"尾随 / 是前缀
// 不是对象"那条规则会让 `object get mybucket/logs/ --file=…` 失败在一个合法对象上——
// 零字节的 `<prefix>/` 目录标记是本产品自己创建的（oss_svc.CreateFolder），同一条命令
// 不带 --file 读得出来，带上就读不出来，是同一件事的两个答案。
func (e ossExecutor) downloadObject(
	ctx context.Context, asset *asset_entity.Asset, bucket, key, file string,
) (string, error) {
	if err := gateExecLocalWrite(ctx, file,
		fmt.Sprintf("object get %s/%s --file=%s", bucket, key, file)); err != nil {
		return "", err
	}

	reader, size, err := e.svc.GetObject(ctx, asset.ID, bucket, key)
	if err != nil {
		return "", err
	}
	defer closeOSSStream(ctx, reader, key)

	if err := localTransfer.Write(ctx, nil, file, reader, size); err != nil {
		return "", err
	}
	return marshalOSSResult(ctx, "object get", map[string]any{
		"bucket": bucket, "key": key, "size": size, "file": file,
	})
}

// readObjectInline 按 --max-bytes 截断读回对象内容。
//
// 先 Stat 再 Get：contentType 只有 Stat 给得出，而它正是模型判断"这东西该怎么读"的依据。
// 两次调用请求的是同一项权限（object.read <bucket>/<key>），不产生第二次审批。
func (e ossExecutor) readObjectInline(
	ctx context.Context, asset *asset_entity.Asset, bucket, key string, c *OSSCommand,
) (string, error) {
	maxBytes, err := ossIntFlag(c, "max-bytes")
	if err != nil {
		return "", err
	}
	if maxBytes <= 0 {
		maxBytes = ossDefaultInlineBytes
	}
	if maxBytes > ossMaxInlineBytes {
		maxBytes = ossMaxInlineBytes
	}

	item, err := e.svc.StatObject(ctx, &oss_svc.ObjectRequest{AssetID: asset.ID, Bucket: bucket, Key: key})
	if err != nil {
		return "", err
	}
	reader, size, err := e.svc.GetObject(ctx, asset.ID, bucket, key)
	if err != nil {
		return "", err
	}
	defer closeOSSStream(ctx, reader, key)

	// 多读一个字节就知道后面还有没有——比拿 size 去比更可靠：size 是元数据，
	// 对象在两次调用之间被换掉时它会说谎，而这一个字节说的是这次真读到的内容。
	data, err := io.ReadAll(io.LimitReader(reader, int64(maxBytes)+1))
	if err != nil {
		return "", err
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}

	result := map[string]any{
		"bucket": bucket, "key": key, "size": size, "contentType": item.ContentType,
		"truncated": truncated,
	}
	// 非 UTF-8 内容改用 base64：裸字节塞进 JSON 字符串会被 encoding/json 用 U+FFFD
	// 替换掉，模型拿到的是坏数据而不是"读不了"。截断本身也可能把一个多字节字符切成
	// 两半，那时同样落到 base64——诚实的编码，好过一个尾巴上带替换字符的字符串。
	if utf8.Valid(data) {
		result["encoding"] = "utf-8"
		result["content"] = string(data)
	} else {
		result["encoding"] = "base64"
		result["content"] = base64.StdEncoding.EncodeToString(data)
	}
	return marshalOSSResult(ctx, "object get", result)
}

// putObject 读本地文件走传输接缝的本地端（localAdapter.OpenRead：打开 + 取 size），
// 写对象走 oss_svc.PutObject。
//
// 写这一端不用 ossAdapter.Write 是因为 TransferAdapter.Write 的签名里没有 content type
// ——cp 的两端只有路径，猜一个 MIME 会盖掉服务端的判定，所以适配器一律传空串，而
// `object put --content-type=` 就是显式指定它的那个入口。适配器的 Write 本身也只是
// 这同一个 oss_svc.PutObject 调用，没有第二份实现被绕开。
func (e ossExecutor) putObject(ctx context.Context, asset *asset_entity.Asset, c *OSSCommand) (string, error) {
	bucket, key := cutOSSPath(c.Target)
	file := c.Flags["file"] // ossFlagRules 里是必填，validateOSSFlags 已经拦过空值

	reader, size, err := localTransfer.OpenRead(ctx, nil, file)
	if err != nil {
		return "", err
	}
	defer closeOSSStream(ctx, reader, file)

	if err := e.svc.PutObject(ctx, asset.ID, bucket, key, reader, size, c.Flags["content-type"]); err != nil {
		return "", err
	}
	return marshalOSSResult(ctx, "object put", map[string]any{"bucket": bucket, "key": key, "size": size})
}

func (e ossExecutor) copyObject(ctx context.Context, asset *asset_entity.Asset, c *OSSCommand) (string, error) {
	req := ossCopyRequest(asset, c)
	if err := e.svc.CopyObject(ctx, req); err != nil {
		return "", err
	}
	return marshalOSSResult(ctx, "object copy", ossCopyResult(c))
}

// moveObject 复用 oss_svc.MoveObject（copy 成功才删源），不重写一遍那条顺序。
func (e ossExecutor) moveObject(ctx context.Context, asset *asset_entity.Asset, c *OSSCommand) (string, error) {
	req := ossCopyRequest(asset, c)
	if err := e.svc.MoveObject(ctx, req); err != nil {
		return "", err
	}
	return marshalOSSResult(ctx, "object move", ossCopyResult(c))
}

func ossCopyRequest(asset *asset_entity.Asset, c *OSSCommand) *oss_svc.CopyRequest {
	srcBucket, srcKey := cutOSSPath(c.Target)
	dstBucket, dstKey := cutOSSPath(c.Flags["to"])
	return &oss_svc.CopyRequest{
		AssetID: asset.ID, SrcBucket: srcBucket, SrcKey: srcKey, DstBucket: dstBucket, DstKey: dstKey,
	}
}

// ossCopyResult 回显两端的原始写法（`<bucket>/<key>`），与审批弹窗上那条命令一致。
func ossCopyResult(c *OSSCommand) map[string]any {
	return map[string]any{"status": "ok", "src": c.Target, "dst": c.Flags["to"]}
}

func (e ossExecutor) deleteObject(ctx context.Context, asset *asset_entity.Asset, c *OSSCommand) (string, error) {
	bucket, key := cutOSSPath(c.Target)
	if err := e.svc.RemoveObject(ctx, &oss_svc.ObjectRequest{AssetID: asset.ID, Bucket: bucket, Key: key}); err != nil {
		return "", err
	}
	return marshalOSSResult(ctx, "object delete", map[string]any{"status": "ok", "bucket": bucket, "key": key})
}

// presignObject 按 --method 选 GET 还是 PUT。
//
// 合法值只认 ossPresignMethod 一处真相（policyActions 出于同样的理由也调它）：这个 flag
// 决定的是派生出 object.presign.read 还是 object.presign.write，而 presign PUT 是默认
// 策略里唯一被地板拒绝的操作（§4.4）。认不出的值若回落成 get，策略判定与实际签发的
// URL 就分了岔——用一次读授权换来了写能力。
func (e ossExecutor) presignObject(ctx context.Context, asset *asset_entity.Asset, c *OSSCommand) (string, error) {
	method := c.Flags["method"]
	if method == "" {
		method = "get"
	}
	if err := ossPresignMethod(method); err != nil {
		return "", fmt.Errorf("invalid value for --method: %w", err)
	}
	expiry, err := ossIntFlag(c, "expiry")
	if err != nil {
		return "", err
	}
	if expiry <= 0 {
		expiry = ossDefaultPresignExpirySecs
	}

	bucket, key := cutOSSPath(c.Target)
	req := &oss_svc.PresignRequest{AssetID: asset.ID, Bucket: bucket, Key: key, ExpirySecs: expiry}
	presign := e.svc.PresignGet
	if method == "put" {
		presign = e.svc.PresignPut
	}
	url, err := presign(ctx, req)
	if err != nil {
		return "", err
	}
	// 返回给调用方的 URL 是完整可用的；落库那一层由 internal/pkg/auditredact 把签名
	// 查询参数换成 <redacted>（决策 D10）。
	return marshalOSSResult(ctx, "object presign", map[string]any{
		"url": url, "method": method, "expiresIn": expiry,
	})
}

// ossIntFlag 取一个整数 flag，未给时返回 0（由下游按各自的默认值处理）。
//
// 形状已由 validateOSSFlags 的 kafkaInt 校验过，这里仍然返回 error 而不是忽略：
// kafkaInt 认的是 int64，而这些字段是 int，32 位平台上 `--max-bytes=9999999999`
// 过得了那一关、在这里才溢出。
func ossIntFlag(c *OSSCommand, name string) (int, error) {
	raw, ok := c.Flags[name]
	if !ok {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid value for --%s: %w", name, err)
	}
	return n, nil
}

// closeOSSStream 关掉一次传输的源端句柄——下行时是对象流，上行时是本地文件。
// 读端的 Close 失败不影响已经读到的字节，所以只记一条 Warn；写端不是这样，
// 那一侧由 localTransfer.Write / oss_svc.PutObject 各自负责。
func closeOSSStream(ctx context.Context, r io.Closer, name string) {
	if err := r.Close(); err != nil && !IsExpectedCloseErr(err) {
		logger.Ctx(ctx).Warn("close oss transfer stream", zap.String("name", name), zap.Error(err))
	}
}

// marshalOSSResult 把执行结果序列化成返回给模型的 JSON 串（与 marshalEtcdResult 同形）。
func marshalOSSResult(ctx context.Context, op string, result any) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		logger.Ctx(ctx).Error("marshal oss result", zap.String("op", op), zap.Error(err))
		return "", fmt.Errorf("failed to marshal oss result: %w", err)
	}
	return string(data), nil
}
