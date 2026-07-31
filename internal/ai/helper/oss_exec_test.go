package helper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/oss_svc"
)

// ossExecCall 是 fake 记下的一次服务调用。断言"执行器把命令翻译成了哪一次调用"比断言
// 返回值更承重：DSL 的 target 与 flag 一旦被错位地喂进 oss_svc，返回的 JSON 往往照样
// 长得对，错的是**动了哪个对象**。
type ossExecCall struct {
	op          string
	assetID     int64
	bucket      string
	key         string
	dstBucket   string
	dstKey      string
	prefix      string
	maxKeys     int
	after       string
	contentType string
	size        int64
	expirySecs  int
	body        string
}

// fakeOSSExec 是内存版 oss_svc.Service。与任务 8 的 transfer fake 同一理由：
// 执行器的全部难点在"命令 → 服务调用"的翻译上，跑不起服务端就等于测不到它。
type fakeOSSExec struct {
	calls   []ossExecCall
	buckets []oss_svc.BucketItem
	objects map[string]string
	listRes *oss_svc.ListObjectsResult
	stat    *oss_svc.ObjectItem
	url     string
	err     error
}

func (f *fakeOSSExec) record(c ossExecCall) { f.calls = append(f.calls, c) }

func (f *fakeOSSExec) ops() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.op)
	}
	return out
}

func (f *fakeOSSExec) ListBuckets(_ context.Context, assetID int64) ([]oss_svc.BucketItem, error) {
	f.record(ossExecCall{op: "ListBuckets", assetID: assetID})
	return f.buckets, f.err
}

func (f *fakeOSSExec) ListObjects(_ context.Context, req *oss_svc.ListObjectsRequest) (*oss_svc.ListObjectsResult, error) {
	f.record(ossExecCall{
		op: "ListObjects", assetID: req.AssetID, bucket: req.Bucket, prefix: req.Prefix,
		maxKeys: req.MaxKeys, after: req.ContinuationToken,
	})
	if f.err != nil {
		return nil, f.err
	}
	return f.listRes, nil
}

func (f *fakeOSSExec) StatObject(_ context.Context, req *oss_svc.ObjectRequest) (*oss_svc.ObjectItem, error) {
	f.record(ossExecCall{op: "StatObject", assetID: req.AssetID, bucket: req.Bucket, key: req.Key})
	if f.err != nil {
		return nil, f.err
	}
	if f.stat != nil {
		return f.stat, nil
	}
	body, ok := f.objects[req.Bucket+"/"+req.Key]
	if !ok {
		return nil, fmt.Errorf("no such key %q", req.Key)
	}
	return &oss_svc.ObjectItem{Key: req.Key, Size: int64(len(body))}, nil
}

func (f *fakeOSSExec) RemoveObject(_ context.Context, req *oss_svc.ObjectRequest) error {
	f.record(ossExecCall{op: "RemoveObject", assetID: req.AssetID, bucket: req.Bucket, key: req.Key})
	return f.err
}

func (f *fakeOSSExec) CopyObject(_ context.Context, req *oss_svc.CopyRequest) error {
	f.record(ossExecCall{
		op: "CopyObject", assetID: req.AssetID, bucket: req.SrcBucket, key: req.SrcKey,
		dstBucket: req.DstBucket, dstKey: req.DstKey,
	})
	return f.err
}

func (f *fakeOSSExec) MoveObject(_ context.Context, req *oss_svc.CopyRequest) error {
	f.record(ossExecCall{
		op: "MoveObject", assetID: req.AssetID, bucket: req.SrcBucket, key: req.SrcKey,
		dstBucket: req.DstBucket, dstKey: req.DstKey,
	})
	return f.err
}

func (f *fakeOSSExec) GetObject(_ context.Context, assetID int64, bucket, key string) (io.ReadCloser, int64, error) {
	f.record(ossExecCall{op: "GetObject", assetID: assetID, bucket: bucket, key: key})
	if f.err != nil {
		return nil, 0, f.err
	}
	body, ok := f.objects[bucket+"/"+key]
	if !ok {
		return nil, 0, fmt.Errorf("no such key %q", key)
	}
	return io.NopCloser(strings.NewReader(body)), int64(len(body)), nil
}

func (f *fakeOSSExec) PutObject(
	_ context.Context, assetID int64, bucket, key string, r io.Reader, size int64, contentType string,
) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.record(ossExecCall{
		op: "PutObject", assetID: assetID, bucket: bucket, key: key,
		size: size, contentType: contentType, body: string(body),
	})
	if f.objects == nil {
		f.objects = map[string]string{}
	}
	f.objects[bucket+"/"+key] = string(body)
	return f.err
}

func (f *fakeOSSExec) PresignGet(_ context.Context, req *oss_svc.PresignRequest) (string, error) {
	f.record(ossExecCall{op: "PresignGet", assetID: req.AssetID, bucket: req.Bucket, key: req.Key, expirySecs: req.ExpirySecs})
	return f.url, f.err
}

func (f *fakeOSSExec) PresignPut(_ context.Context, req *oss_svc.PresignRequest) (string, error) {
	f.record(ossExecCall{op: "PresignPut", assetID: req.AssetID, bucket: req.Bucket, key: req.Key, expirySecs: req.ExpirySecs})
	return f.url, f.err
}

func ossExecAsset() *asset_entity.Asset {
	return &asset_entity.Asset{ID: 7, Name: "s3-prod", Type: asset_entity.AssetTypeOSS}
}

// runOSSExec 跑一条命令并把结果 JSON 解回 map，省得每个用例各写一次反序列化。
func runOSSExec(t *testing.T, f *fakeOSSExec, command string) map[string]any {
	t.Helper()
	out, err := (ossExecutor{svc: f}).run(context.Background(), ossExecAsset(), command)
	if err != nil {
		t.Fatalf("run(%q) unexpected error: %v", command, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("run(%q) = %q, which is not a JSON object: %v", command, out, err)
	}
	return got
}

func (f *fakeOSSExec) onlyCall(t *testing.T, op string) ossExecCall {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("service calls = %v, want exactly one %s", f.ops(), op)
	}
	if f.calls[0].op != op {
		t.Fatalf("service call = %q, want %q", f.calls[0].op, op)
	}
	// 每条命令都必须打在被审批的那个资产上。断言放在这里而不是逐个用例里，是因为它对
	// 全部九个操作是同一条不变式：assetID 是执行器唯一有机会搞错的"哪台机器"参数，
	// 而弄错它就是在一个从没被检查过的资产上动手。实测每个 handler 各自漏掉它时，
	// 除 bucket list 外没有任何用例会红。
	if f.calls[0].assetID != ossExecAsset().ID {
		t.Fatalf("%s ran with assetID = %d, want %d (the asset the command was approved for)",
			op, f.calls[0].assetID, ossExecAsset().ID)
	}
	return f.calls[0]
}

func TestExecOSSOnAsset_BucketList(t *testing.T) {
	f := &fakeOSSExec{buckets: []oss_svc.BucketItem{{Name: "logs", CreationDate: 1}, {Name: "backups"}}}
	got := runOSSExec(t, f, "bucket list")

	if call := f.onlyCall(t, "ListBuckets"); call.assetID != 7 {
		t.Fatalf("ListBuckets assetID = %d, want 7", call.assetID)
	}
	buckets, ok := got["buckets"].([]any)
	if !ok || len(buckets) != 2 {
		t.Fatalf("result = %v, want two buckets", got)
	}
	first, _ := buckets[0].(map[string]any)
	if first["name"] != "logs" {
		t.Fatalf("first bucket = %v, want name=logs", first)
	}
}

// object list 的 target 是 <bucket>[/<prefix>]，桶名与前缀必须各归各位：把整个 target
// 当成 Bucket 会去列一个不存在的桶，把它整个当成 Prefix 会列错桶。
func TestExecOSSOnAsset_ObjectListSplitsTargetAndPassesFlags(t *testing.T) {
	cases := []struct {
		command        string
		bucket, prefix string
		maxKeys        int
		after          string
	}{
		{"object list mybucket", "mybucket", "", 0, ""},
		{"object list mybucket/logs/2026/", "mybucket", "logs/2026/", 0, ""},
		{"object list mybucket/logs/ --max-keys=50 --after=logs/a.txt", "mybucket", "logs/", 50, "logs/a.txt"},
	}
	for _, c := range cases {
		f := &fakeOSSExec{listRes: &oss_svc.ListObjectsResult{
			Prefixes: []string{}, Objects: []oss_svc.ObjectItem{{Key: "logs/a.txt", Size: 3}},
		}}
		got := runOSSExec(t, f, c.command)

		call := f.onlyCall(t, "ListObjects")
		if call.bucket != c.bucket || call.prefix != c.prefix || call.maxKeys != c.maxKeys || call.after != c.after {
			t.Errorf("%q → ListObjects(bucket=%q prefix=%q maxKeys=%d after=%q), want (%q, %q, %d, %q)",
				c.command, call.bucket, call.prefix, call.maxKeys, call.after, c.bucket, c.prefix, c.maxKeys, c.after)
		}
		if _, ok := got["objects"]; !ok {
			t.Errorf("%q result = %v, want the oss_svc.ListObjectsResult shape (objects / isTruncated / nextContinuationToken)", c.command, got)
		}
	}
}

func TestExecOSSOnAsset_ObjectStat(t *testing.T) {
	f := &fakeOSSExec{stat: &oss_svc.ObjectItem{Key: "logs/a.txt", Size: 12, ContentType: "text/plain"}}
	got := runOSSExec(t, f, "object stat mybucket/logs/a.txt")

	call := f.onlyCall(t, "StatObject")
	if call.bucket != "mybucket" || call.key != "logs/a.txt" {
		t.Fatalf("StatObject(bucket=%q key=%q), want (mybucket, logs/a.txt)", call.bucket, call.key)
	}
	if got["key"] != "logs/a.txt" || got["contentType"] != "text/plain" {
		t.Fatalf("result = %v, want the oss_svc.ObjectItem verbatim", got)
	}
}

// 无 --file 的 object get 是"直接读给模型看"，与 --file 是两种行为（spec §5）。
func TestExecOSSOnAsset_ObjectGetInline(t *testing.T) {
	f := &fakeOSSExec{
		objects: map[string]string{"mybucket/conf.yml": "debug: true\n"},
		stat:    &oss_svc.ObjectItem{Key: "conf.yml", Size: 12, ContentType: "text/yaml"},
	}
	got := runOSSExec(t, f, "object get mybucket/conf.yml")

	if ops := f.ops(); !slices.Contains(ops, "GetObject") {
		t.Fatalf("service calls = %v, want a GetObject", ops)
	}
	if got["content"] != "debug: true\n" {
		t.Errorf("content = %v, want the object body", got["content"])
	}
	if got["encoding"] != "utf-8" {
		t.Errorf("encoding = %v, want utf-8 for a valid UTF-8 body", got["encoding"])
	}
	if got["truncated"] != false {
		t.Errorf("truncated = %v, want false for a body under the limit", got["truncated"])
	}
	if got["size"] != float64(12) {
		t.Errorf("size = %v, want 12 (the full object size)", got["size"])
	}
	if got["contentType"] != "text/yaml" {
		t.Errorf("contentType = %v, want text/yaml", got["contentType"])
	}
	if _, ok := got["file"]; ok {
		t.Errorf("result = %v, want no file field when --file was not given", got)
	}
}

// 截断必须**报出来**：一个看起来完整、实际只有开头 N 字节的配置文件，比一个报错糟得多
// ——模型会照着半截内容改。
//
// 两条边界用例（读满上限一个字节都不多 / 只多一个字节）不是凑数：`len(data) > maxBytes`
// 写成 `>=` 时，上面那条"多读一个字节"的探测仍然工作，整包却照样全绿（实测该变异存活）
// ——错的只有恰好读满的那一个对象，它会被标成 truncated，模型据此白跑一次 --file，
// 或者干脆认为自己没读全而放弃。截断标志的全部价值就在这个边界上。
func TestExecOSSOnAsset_ObjectGetInlineTruncates(t *testing.T) {
	const body = "0123456789"
	cases := []struct {
		name        string
		maxBytes    int
		wantContent string
		wantTrunc   bool
	}{
		{"under the limit", 20, body, false},
		{"exactly at the limit", len(body), body, false},
		{"one byte over the limit", len(body) - 1, "012345678", true},
		{"well over the limit", 4, "0123", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeOSSExec{objects: map[string]string{"mybucket/app.log": body}}
			got := runOSSExec(t, f, fmt.Sprintf("object get mybucket/app.log --max-bytes=%d", c.maxBytes))

			if got["content"] != c.wantContent {
				t.Errorf("content = %v, want %q", got["content"], c.wantContent)
			}
			if got["truncated"] != c.wantTrunc {
				t.Errorf("truncated = %v, want %v for --max-bytes=%d over a %d-byte object",
					got["truncated"], c.wantTrunc, c.maxBytes, len(body))
			}
			if got["size"] != float64(len(body)) {
				t.Errorf("size = %v, want %d (the full object size, not the returned slice)", got["size"], len(body))
			}
		})
	}
}

// --max-bytes 有硬上限：模型给一个大数不该把整个对象拉进上下文。
func TestExecOSSOnAsset_ObjectGetInlineCapsMaxBytes(t *testing.T) {
	body := strings.Repeat("x", ossMaxInlineBytes+4096)
	f := &fakeOSSExec{objects: map[string]string{"mybucket/big.bin": body}}
	got := runOSSExec(t, f, fmt.Sprintf("object get mybucket/big.bin --max-bytes=%d", len(body)))

	content, _ := got["content"].(string)
	if len(content) != ossMaxInlineBytes {
		t.Errorf("len(content) = %d, want the hard cap %d", len(content), ossMaxInlineBytes)
	}
	if got["truncated"] != true {
		t.Errorf("truncated = %v, want true once the cap kicks in", got["truncated"])
	}
}

// 非 UTF-8 的对象改用 base64 并标明 encoding：把裸字节塞进 JSON 字符串会被
// encoding/json 用 U+FFFD 替换掉，模型拿到的是坏数据而不是"读不了"。
func TestExecOSSOnAsset_ObjectGetInlineBase64(t *testing.T) {
	raw := string([]byte{0x00, 0xff, 0xfe, 0x41})
	f := &fakeOSSExec{objects: map[string]string{"mybucket/blob.bin": raw}}
	got := runOSSExec(t, f, "object get mybucket/blob.bin")

	if got["encoding"] != "base64" {
		t.Fatalf("encoding = %v, want base64 for a non-UTF-8 body", got["encoding"])
	}
	if got["content"] != base64.StdEncoding.EncodeToString([]byte(raw)) {
		t.Fatalf("content = %v, want the base64 of the raw bytes", got["content"])
	}
}

// --file 的落盘走传输接缝的本地端（localAdapter.Write），因此父目录按需创建——
// 这正是不自己写第二份"打开本地文件"实现换来的东西。
func TestExecOSSOnAsset_ObjectGetToFile(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "nested", "deeper", "conf.yml")
	f := &fakeOSSExec{objects: map[string]string{"mybucket/conf.yml": "debug: true\n"}}

	got := runOSSExec(t, f, "object get mybucket/conf.yml --file="+dst)

	data, err := os.ReadFile(dst) //nolint:gosec // 测试自己造的临时路径
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != "debug: true\n" {
		t.Errorf("downloaded file = %q, want the object body", data)
	}
	if got["file"] != dst {
		t.Errorf("file = %v, want %q", got["file"], dst)
	}
	if _, ok := got["content"]; ok {
		t.Errorf("result = %v, want no inline content when --file was given", got)
	}
}

func TestExecOSSOnAsset_ObjectPutFromFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "conf.yml")
	if err := os.WriteFile(src, []byte("debug: true\n"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	f := &fakeOSSExec{}

	got := runOSSExec(t, f, "object put mybucket/conf.yml --file="+src+" --content-type=text/yaml")

	call := f.onlyCall(t, "PutObject")
	if call.bucket != "mybucket" || call.key != "conf.yml" {
		t.Errorf("PutObject(bucket=%q key=%q), want (mybucket, conf.yml)", call.bucket, call.key)
	}
	if call.body != "debug: true\n" {
		t.Errorf("PutObject body = %q, want the local file content", call.body)
	}
	if call.size != 12 {
		t.Errorf("PutObject size = %d, want 12 (the local file size, so the backend does not have to buffer)", call.size)
	}
	if call.contentType != "text/yaml" {
		t.Errorf("PutObject contentType = %q, want text/yaml from --content-type", call.contentType)
	}
	if got["size"] != float64(12) {
		t.Errorf("result size = %v, want 12", got["size"])
	}
}

func TestExecOSSOnAsset_ObjectCopyAndMove(t *testing.T) {
	cases := []struct{ command, op string }{
		{"object copy mybucket/a.txt --to=other/b.txt", "CopyObject"},
		{"object move mybucket/a.txt --to=other/b.txt", "MoveObject"},
	}
	for _, c := range cases {
		f := &fakeOSSExec{}
		got := runOSSExec(t, f, c.command)

		call := f.onlyCall(t, c.op)
		if call.bucket != "mybucket" || call.key != "a.txt" || call.dstBucket != "other" || call.dstKey != "b.txt" {
			t.Errorf("%q → %s(src=%q/%q dst=%q/%q), want (mybucket/a.txt → other/b.txt)",
				c.command, c.op, call.bucket, call.key, call.dstBucket, call.dstKey)
		}
		if got["src"] != "mybucket/a.txt" || got["dst"] != "other/b.txt" {
			t.Errorf("%q result = %v, want src/dst echoed back", c.command, got)
		}
	}
}

func TestExecOSSOnAsset_ObjectDelete(t *testing.T) {
	f := &fakeOSSExec{}
	got := runOSSExec(t, f, "object delete mybucket/logs/old.log")

	call := f.onlyCall(t, "RemoveObject")
	if call.bucket != "mybucket" || call.key != "logs/old.log" {
		t.Fatalf("RemoveObject(bucket=%q key=%q), want (mybucket, logs/old.log)", call.bucket, call.key)
	}
	if got["status"] != "ok" || got["key"] != "logs/old.log" {
		t.Fatalf("result = %v, want status ok and the deleted key", got)
	}
}

// presign 的 --method 决定调 PresignGet 还是 PresignPut，而 presign PUT 是默认策略里
// **唯一**被地板拒绝的操作（§4.4）。判定滑到另一边，就等于用一次读授权换来了写能力。
func TestExecOSSOnAsset_ObjectPresignMethodPicksTheOperation(t *testing.T) {
	cases := []struct {
		command string
		op      string
		method  string
		expiry  int
	}{
		{"object presign mybucket/a.txt", "PresignGet", "get", ossDefaultPresignExpirySecs},
		{"object presign mybucket/a.txt --method=get --expiry=600", "PresignGet", "get", 600},
		{"object presign mybucket/a.txt --method=put --expiry=600", "PresignPut", "put", 600},
	}
	for _, c := range cases {
		f := &fakeOSSExec{url: "https://example.com/signed"}
		got := runOSSExec(t, f, c.command)

		call := f.onlyCall(t, c.op)
		if call.bucket != "mybucket" || call.key != "a.txt" {
			t.Errorf("%q → %s(bucket=%q key=%q), want (mybucket, a.txt)", c.command, c.op, call.bucket, call.key)
		}
		if call.expirySecs != c.expiry {
			t.Errorf("%q → %s expirySecs = %d, want %d", c.command, c.op, call.expirySecs, c.expiry)
		}
		if got["method"] != c.method {
			t.Errorf("%q result method = %v, want %q", c.command, got["method"], c.method)
		}
		if got["expiresIn"] != float64(c.expiry) {
			t.Errorf("%q result expiresIn = %v, want %d — it must report the expiry actually used",
				c.command, got["expiresIn"], c.expiry)
		}
		if got["url"] != "https://example.com/signed" {
			t.Errorf("%q result url = %v, want the presigned URL verbatim", c.command, got["url"])
		}
	}
}

// 错误原样上抛（spec §5 最后一条）：吞掉它会让模型看到一条 JSON 成功。
func TestExecOSSOnAsset_ServiceErrorsAreNotSwallowed(t *testing.T) {
	f := &fakeOSSExec{err: fmt.Errorf("the bucket does not exist")}
	if _, err := (ossExecutor{svc: f}).run(context.Background(), ossExecAsset(), "object delete mybucket/a.txt"); err == nil {
		t.Fatal("expected the service error to propagate")
	}
}

// --file 必须是绝对路径（决策 D11），且必须在**规范化**这一步就被拒——canonicalize 排在
// handleExec 的权限检查之前，把这条检查留到执行器里，用户会先被弹一次审批
// （选"始终允许"还落一条常驻 grant），命令才因为路径不对而失败。
func TestCanonicalizeOSSCommand_RejectsRelativeFileBeforeApproval(t *testing.T) {
	// 换到一个临时工作目录再跑：这条测试断言的正是"相对路径解析到哪里不可预期"，
	// 而守卫一旦被改坏，`--file=./out/conf.yml` 就会在**包目录里**建出文件来。
	t.Chdir(t.TempDir())
	for _, command := range []string{
		"object put mybucket/a.txt --file=conf.yml",
		"object get mybucket/a.txt --file=./out/conf.yml",
	} {
		if _, err := CanonicalizeOSSCommand(ossExecAsset(), command); err == nil {
			t.Errorf("CanonicalizeOSSCommand(%q) = nil error, want a rejection of the relative --file", command)
		}
		f := &fakeOSSExec{objects: map[string]string{"mybucket/a.txt": "x"}}
		if _, err := (ossExecutor{svc: f}).run(context.Background(), ossExecAsset(), command); err == nil {
			t.Errorf("run(%q) = nil error, want the same rejection", command)
		}
		if len(f.calls) != 0 {
			t.Errorf("run(%q) reached the service (%v); a rejected command must not touch the backend", command, f.ops())
		}
	}
}

// --file 与 --max-bytes 是 object get 的两种行为，不是一件事的两个旋钮（spec §5）：
// 带 --file 是整个对象流式落盘，--max-bytes 只截断内联返回的内容。一起给必有一个被丢掉，
// 而审批弹窗上两个都在——与 ossFlagRules 拒绝未知 flag 是同一条理由（用户批准的和发生的
// 不是一件事）。所以在规范化这一步就报错，而不是执行时静默按 --file 那一支走。
func TestCanonicalizeOSSCommand_RejectsFileWithMaxBytes(t *testing.T) {
	const command = "object get mybucket/app.log --file=/tmp/app.log --max-bytes=4"
	if _, err := CanonicalizeOSSCommand(ossExecAsset(), command); err == nil {
		t.Errorf("CanonicalizeOSSCommand(%q) = nil error, want a rejection: one of the two flags "+
			"would be silently ignored after the user approved a command showing both", command)
	}
	f := &fakeOSSExec{objects: map[string]string{"mybucket/app.log": "0123456789"}}
	if _, err := (ossExecutor{svc: f}).run(context.Background(), ossExecAsset(), command); err == nil {
		t.Errorf("run(%q) = nil error, want the same rejection", command)
	}
	if len(f.calls) != 0 {
		t.Errorf("run(%q) reached the service (%v); a rejected command must not touch the backend", command, f.ops())
	}
}

// 规范化返回的是**规范 DSL 串**（决策 D6）：它同时是审批弹窗与 audit_logs.command 的
// 展示态，所以 flag 顺序与 bucket 根的写法都要归一，否则同一条操作会以两种字面量落库。
func TestCanonicalizeOSSCommand_RendersCanonicalDSL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"object presign mybucket/a.txt --method=get --expiry=600", "object presign mybucket/a.txt --expiry=600 --method=get"},
		{"object list mybucket", "object list mybucket/"},
		{"object move mybucket/a --to=mybucket/b", "object move mybucket/a --to=mybucket/b"},
	}
	for _, c := range cases {
		got, err := CanonicalizeOSSCommand(ossExecAsset(), c.in)
		if err != nil {
			t.Errorf("CanonicalizeOSSCommand(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("CanonicalizeOSSCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 策略串形状的输入必须在规范化这一步就被拒。checkOSSPermission 靠"首词含 '.'"把
// 已派生的策略串与 DSL 命令区分开（cp 端点送进来的就是策略串），所以从 exec 面递一条
// 策略串形状的串进去，会绕过 DSL 解析器直接参与规则匹配——`object.read mybucket/*`
// 能撞上一条整桶 allow 规则并被判成 Allow，之后才在执行器里因为解析不了而失败。
// handleExec 的顺序是 canonicalize → precheck → 权限检查，这一步就是那道闸门。
func TestCanonicalizeOSSCommand_RejectsPolicyStringShapedInput(t *testing.T) {
	for _, command := range []string{
		"object.read mybucket/*",
		"object.delete mybucket/",
		"bucket.list *",
	} {
		if _, err := CanonicalizeOSSCommand(ossExecAsset(), command); err == nil {
			t.Errorf("CanonicalizeOSSCommand(%q) = nil error, want a rejection: a policy-string-shaped "+
				"command must never reach the rule matcher through the exec face", command)
		}
	}
}

// 派发表与 DSL 的 verb 表必须一一对应。少一条：命令解析通过、审批弹窗照常弹、批准之后
// 才报"unknown oss command"。多一条：一个 DSL 根本产不出的 (family, verb) 挂着一段
// 谁也不会调用的执行代码。两个方向都只在运行期才显形，所以在这里钉死。
func TestOSSVerbHandlers_MatchTheDSLVerbTable(t *testing.T) {
	want := map[string]bool{}
	for family, verbs := range ossVerbs {
		for verb := range verbs {
			want[family+" "+verb] = true
		}
	}
	got := map[string]bool{}
	for key := range ossVerbHandlers {
		got[key] = true
	}
	if !maps.Equal(got, want) {
		t.Fatalf("ossVerbHandlers keys = %v, want %v (from ossVerbs)",
			slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
}

// TestOSSSkillDoc_FlagReferenceMatchesFlagRules 把 SKILL.md 的 "## Flag reference" 一节
// 与 ossFlagRules 逐条对齐，与 kafka 侧的同名测试是同一条理由（见
// kafka_skill_doc_test.go 的注释）：文档少写一个 flag，那个参数在统一 exec 下对模型
// 不可发现；多写一个，模型会写出被 validateOSSFlags 拒掉的命令；必填标注错了，
// 模型要么漏参数（批准后失败）要么以为可选项是必填。
//
// 解析器直接复用 kafka 侧那一份（parseKafkaFlagReference / formatKafkaFlagSet）：它读的
// 是 Markdown 的行形状，与哪个 DSL 无关，抄一份只会让两侧的文档格式各自漂移。
func TestOSSSkillDoc_FlagReferenceMatchesFlagRules(t *testing.T) {
	body, ok := skills.Get(asset_entity.AssetTypeOSS)
	if !ok {
		t.Fatal("oss SKILL.md is not embedded")
	}

	documented := parseKafkaFlagReference(t, body)
	if len(documented) == 0 {
		t.Fatal(`no entries parsed from the "## Flag reference" section; each line must read ` +
			"\"- `<family> <verb>`: `--flag`, **`--required-flag`**, …\"")
	}

	// 期望值直接由 ossFlagRules 生成，不手抄。无 flag 的 verb 期望"不出现在文档里"，
	// 这样文档给一个空 verb 编出 flag 也会被抓到。
	want := map[string]map[string]bool{}
	for family, verbs := range ossFlagRules {
		for verb, spec := range verbs {
			if len(spec.required)+len(spec.optional) == 0 {
				continue
			}
			flags := map[string]bool{}
			for name := range spec.optional {
				flags[name] = false
			}
			for name := range spec.required {
				flags[name] = true
			}
			want[family+" "+verb] = flags
		}
	}

	for _, key := range slices.Sorted(maps.Keys(want)) {
		got, listed := documented[key]
		if !listed {
			t.Errorf("oss SKILL.md: %q takes flags (%s) but is missing from the flag reference; "+
				"a flag that is not documented is unreachable to the model", key, formatKafkaFlagSet(want[key]))
			continue
		}
		if !maps.Equal(got, want[key]) {
			t.Errorf("oss SKILL.md: flag reference for %q = %s, want %s (** ** marks required)",
				key, formatKafkaFlagSet(got), formatKafkaFlagSet(want[key]))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(documented)) {
		if _, ok := want[key]; !ok {
			t.Errorf("oss SKILL.md: flag reference documents %q, which either takes no flags or does not exist; "+
				"validateOSSFlags would reject every flag shown for it", key)
		}
	}
}
