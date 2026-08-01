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
// 列举**不自己实现**：它建模的是更下面那一层（oss_svc.Client，即 minio 适配器），
// 再把真正的 oss_svc.ListObjectsWith 跑在上面。分层与分页正是这个适配器要处理的两件事，
// 而它们的真相在 oss_svc 里；在这里照抄一份截断/取游标的逻辑，测的就只是抄件自洽——
// 抄件曾经把 items 建模成全局有序，于是"取最后一条当游标"看起来是对的，而真实客户端
// 根本不那样交货，缺陷就在绿灯下活了下来。
type fakeOSSService struct {
	assetID int64
	objects map[string]map[string]string // bucket → key → 内容
	gets    []string
	puts    []fakeOSSPut
}

// fakeOSSClient 建模 oss_svc.Client 的**真实交货顺序**，即 minio 适配器那一层。
//
// 两步，缺一不可（github.com/minio/minio-go/v7 api-list.go:120-198 的 listObjectsV2，
// v1 的 listObjects:311-375 同形；internal/service/oss_svc/client_minio.go:39-58）：
//  1. S3 按 key 序把这一层分组的结果（Contents 与 CommonPrefixes 合在一起）截到 MaxKeys；
//  2. minio 把这一页**先逐条交出 Contents、再逐条交出 CommonPrefixes**。
//
// 于是交出来的这一串在 key 上是乱序的：桶根下的 archive/ 会排在 b001…b200 后面。
// 只嵌 oss_svc.Client 而不实现其余方法：列举之外的任何调用都该在测试里当场炸开，
// 而不是拿到一个悄无声息的零值。
type fakeOSSClient struct {
	oss_svc.Client
	objects map[string]string // key → 内容
}

func (c *fakeOSSClient) ListObjects(
	_ context.Context, _, prefix string, maxKeys int, startAfter string,
) ([]oss_svc.ObjectItem, error) {
	// 第一步：服务端按 key 序分组。startAfter 是**排他**的，且过滤发生在分组之前——
	// 因此一个公共前缀只要还有 key 大于游标，就会被再次交出来。
	type entry struct {
		item     oss_svc.ObjectItem
		rolledUp bool // 是 CommonPrefixes 里的一条，而不是 Contents 里的
	}
	var page []entry
	lastPrefix := ""
	for _, k := range slices.Sorted(maps.Keys(c.objects)) {
		if !strings.HasPrefix(k, prefix) || k <= startAfter {
			continue
		}
		if i := strings.Index(k[len(prefix):], "/"); i >= 0 {
			commonPrefix := k[:len(prefix)+i+1]
			if commonPrefix == lastPrefix {
				continue
			}
			lastPrefix = commonPrefix
			page = append(page, entry{item: oss_svc.ObjectItem{Key: commonPrefix, IsPrefix: true}, rolledUp: true})
			continue
		}
		// 被列举前缀自身那个零字节"文件夹"标记是一个货真价实的对象，落在 Contents 里；
		// 只是 toObjectItem 会按 key 形状把它标成 IsPrefix。分组归属与这个标记无关。
		item := oss_svc.ObjectItem{Key: k, Size: int64(len(c.objects[k]))}
		item.IsPrefix = item.Size == 0 && strings.HasSuffix(k, "/")
		page = append(page, entry{item: item})
	}
	// MaxKeys 管的是 Contents + CommonPrefixes 的总条数；client_minio.go 多要一条来
	// 判断"还有下一页"，因此这一页最多 maxKeys+1 条。
	page = page[:min(len(page), maxKeys+1)]

	// 第二步：minio 先逐条交出 Contents、再逐条交出 CommonPrefixes。
	out := make([]oss_svc.ObjectItem, 0, len(page))
	for _, e := range page {
		if !e.rolledUp {
			out = append(out, e.item)
		}
	}
	for _, e := range page {
		if e.rolledUp {
			out = append(out, e.item)
		}
	}
	return out, nil
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

// ListObjects 把真正的 oss_svc.ListObjectsWith 跑在 fakeOSSClient 上：分组、截断与
// 续传游标全部由生产代码决定，测试只提供桶内容与交货顺序。
func (f *fakeOSSService) ListObjects(
	ctx context.Context, req *oss_svc.ListObjectsRequest,
) (*oss_svc.ListObjectsResult, error) {
	if err := f.checkAsset(req.AssetID); err != nil {
		return nil, err
	}
	c := &fakeOSSClient{objects: f.objects[req.Bucket]}
	return oss_svc.ListObjectsWith(ctx, c, req.Bucket, req.Prefix, req.MaxKeys, req.ContinuationToken)
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
	assertExpandedExactlyOnce(t, got, objects, "logs/")
}

// 分页游标必须对得住它的 start-after 语义，而**服务端交货不是按 key 排序的**：
// minio 对每个 S3 响应先交 Contents、再交 CommonPrefixes（api-list.go:164-177），
// 而 S3 是把两者合起来按 key 序截到 MaxKeys 的。于是页边界两侧各有一种翻车方式，
// 两种都会让一次 cp -r 报成功而结果是错的。
func TestOSSTransferList_PaginatesAcrossTheServerSideItemOrder(t *testing.T) {
	// 漏传：桶根下一个 archive/ 加 b001…b200。S3 首页按 key 序是
	// [archive/, b001…b200]，minio 交出来是 [b001…b200, archive/]，
	// 于是 archive/ 落在页外而游标停在 b200——archive/ 下的 key 全都小于 b200，
	// 下一页的 startAfter 再也够不着它们，整棵子树静默消失。
	omission := make(map[string]string, fakeOSSPageSize+2)
	omission["archive/old.log"] = "x"
	omission["archive/deep/older.log"] = "x"
	for i := 1; i <= fakeOSSPageSize; i++ {
		omission[fmt.Sprintf("b%03d", i)] = "x"
	}

	// 重传：桶根下 p001/…p200/ 各一个对象，外加一个排在它们之后的对象 z.txt。
	// minio 先交 z.txt、再交 200 个公共前缀，页边界落在前缀里，游标停在 p199/——
	// 而 z.txt 本页已经交出去了且排在游标之后，下一页会把它再交一遍：
	// 条目重复、字节重复读写、条数虚高。
	duplication := make(map[string]string, fakeOSSPageSize+1)
	for i := 1; i <= fakeOSSPageSize; i++ {
		duplication[fmt.Sprintf("p%03d/o.txt", i)] = "x"
	}
	duplication["z.txt"] = "x"

	for _, tt := range []struct {
		name    string
		objects map[string]string
	}{
		{"a subtree sorting before the page boundary", omission},
		{"an object sorting after the page boundary", duplication},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adapter, _, asset := newOSSTest(tt.objects)

			got, err := adapter.List(context.Background(), asset, "/mybucket", true)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			assertExpandedExactlyOnce(t, got, tt.objects, "")
		})
	}
}

// assertExpandedExactlyOnce：展开出的条目集合必须与桶内容一一对应——一条不少（漏了就是
// 一次报成功却少传的 cp），一条不多（多了就是同一个对象被读写两遍）。
func assertExpandedExactlyOnce(t *testing.T, got *ListResult, objects map[string]string, base string) {
	t.Helper()
	seen := make(map[string]int, len(got.Entries))
	for _, e := range got.Entries {
		seen[e.RelPath]++
	}
	for key := range objects {
		if strings.HasSuffix(key, "/") { // 零字节"文件夹"标记不是可传输条目
			continue
		}
		if n := seen[strings.TrimPrefix(key, base)]; n != 1 {
			t.Errorf("%q expanded %d times, want exactly 1", key, n)
		}
		delete(seen, strings.TrimPrefix(key, base))
	}
	for rel, n := range seen {
		t.Errorf("expanded %q (%d times), which is not in the bucket", rel, n)
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
		// 主体是这条链上的**名字**：它先被拿去撞规则，之后才由
		// permission.NormalizeGrantPatterns 翻成规则落库（转义在那一步，决策 D21 更正）。
		// 这里一个反斜杠都不该有——名字侧转义会让规则匹配不上自己，"始终允许"于是失效。
		// 覆盖范围由 TestOSSTransfer_ApprovalSubjectNeverOutgrowsItsPath 端到端咬住。
		{"a key with a glob metacharacter", "/mybucket/dist/a[1].js", DirRead, `object.read mybucket/dist/a[1].js`},
		{"a destination key with a glob metacharacter", "/mybucket/secrets*", DirWrite, `object.write mybucket/secrets*`},
		{"list a prefix with a backslash in it", `/mybucket/a\b/`, DirList, `object.list mybucket/a\b/`},
		// 用户指名的是一个**前缀**，哪怕它带字面量元字符：globBase 会在第一个元字符处
		// 按整段截断、基点塌成空串，主体就成了 `object.list mybucket/` —— 指名一个前缀
		// 却换来整桶列举的常驻授权。前缀形态原样交出才是准确的范围。
		{"list a literal prefix that contains a metacharacter", "/mybucket/logs[1]/", DirList,
			"object.list mybucket/logs[1]/"},
		{"list a literal prefix whose first segment is all metacharacters", "/mybucket/[a]/b/", DirList,
			"object.list mybucket/[a]/b/"},
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

// 主体落成常驻 grant 之后，绝不能覆盖它描述的那一条传输之外的东西。
//
// 这不是形式检查，而是这个接口唯一的安全后置条件，而且它**跨包**：主体是名字，
// permission.NormalizeGrantPatterns 把它翻成规则（§4.3 的前缀丢弃 + D21 的转义），
// policy.MatchOSSRule 再按 §3.4 读那条规则。按 D5 的规则语义，桶名后为空——光桶名或以
// "/" 收尾——意味着整桶任意深度（两种写法等价），桶段里的 "*" 是跨桶通配。目的端路径
// 不经过 List，因此形态错误的目的地直接到这里：`cp ./a.txt s3:/mybucket` 一旦被点
// "始终允许"，换来的绝不能是一条整桶可写的常驻授权。
//
// 归一化按**来源**决定要不要收窄，而适配器给出的主体是 GrantOriginSystem —— 传
// GrantOriginUser 就等于宣称这是用户手写的 pattern，D20 与 D21 双双失效。
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
		patterns := permission.NormalizeGrantPatterns(
			asset_entity.AssetTypeOSS, subject, permission.GrantOriginSystem)
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
	// 对象"落成的 grant 当规则读时比这个对象宽（决策 D21）。递归展开本来就会合法产出
	// 这种 key（`dist/*` 命中 `dist/a[1].js`），所以拒绝它们不是选项：落库时必须收窄。
	//
	// **往返是这条用例的重点**，也是 c0de1b2c 弄坏、本次修回的那件事：转义曾经写在
	// ApprovalSubject 里，于是名字与规则两侧都带反斜杠，path.Match 对不上自己——
	// "始终允许"点了等于没点，每次重新弹框，还多落一条什么都不授权的死行。
	// 只断言"不多授权"是抓不到那个 bug 的：一条死 grant 同样什么都不多授权。
	t.Run("a key with glob metacharacters grants exactly that key again", func(t *testing.T) {
		for _, p := range []string{"/mybucket/dist/a[1].js", "/mybucket/secrets*", `/mybucket/a\b.txt`} {
			for _, dir := range []Direction{DirRead, DirWrite} {
				_, subject := ossTransfer.ApprovalSubject(p, dir)
				patterns := assertGrantsNothingElse(t, subject)
				if len(patterns) != 1 {
					t.Fatalf("subject %q lands as %d grants (%q), want exactly one", subject, len(patterns), patterns)
				}
				// 下一次同一个请求：主体逐字节相同，必须命中刚落下的那条 grant。
				if !policy.MatchOSSRule(patterns[0], subject) {
					t.Errorf("grant %q does not authorize the very transfer %q it came from; "+
						`this is a dead row and "always allow" never takes effect`, patterns[0], subject)
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
		// 字面量元字符的前缀是 globBase 会截断的那一类：截断后基点塌成空串，
		// 主体变成 `object.list mybucket/`，一次针对某个前缀的递归 cp 换来整桶列举授权。
		// probe 里的 `object.list mybucket/secrets/` 会抓住它。
		{"listing a literal prefix that contains a metacharacter", "/mybucket/logs[1]/",
			"object.list mybucket/logs[1]/sub/deep/"},
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

// TestOSSTransfer_ValidateDestination 锁住目的端形态校验的**次序**价值：形态错误的
// 目的地必须在审批之前就被拦下。
//
// 这些路径的主体（ossSubjectResource 的畸形回退）是一条谁也授权不了的惰性串，
// 用户批准它之后 Write 必然报同一个错——那次弹窗纯属白打断。
func TestOSSTransfer_ValidateDestination(t *testing.T) {
	for _, p := range []string{
		"/mybucket",       // 光桶名：是桶不是对象
		"mybucket",        // 同上，且没有前导 "/"
		"/mybucket/",      // 尾随 "/"：是前缀不是对象
		"/mybucket/logs/", // 同上，深一层
		"",                // 空串：连桶都没有
		"/",               // 只有一个斜杠
		"/*/logs/app.log", // 桶段带通配：指不到任何真实的桶
	} {
		if err := ossTransfer.ValidateDestination(p); err == nil {
			t.Errorf("ValidateDestination(%q) = nil, want an error before the approval dialog", p)
		}
	}
	for _, p := range []string{
		"/mybucket/app.log",
		"mybucket/logs/app.log",
		"/mybucket/My Report.pdf",
		`/mybucket/dist/a[1].js`,
	} {
		if err := ossTransfer.ValidateDestination(p); err != nil {
			t.Errorf("ValidateDestination(%q) = %v, want nil", p, err)
		}
	}
}

// TestOSSTransfer_PaddedPathIsNotAnAuthorizableSubject 咬住一条**规则语言表达不了**的
// 形态：resource 段带前导/尾随空白。
//
// policy.splitOSSRule 把规则与命令两侧的 resource 都 TrimSpace 之后再比，所以
// `mybucket/app.log ` 与 `mybucket/app.log` 在匹配器眼里是同一个资源——而在 S3 眼里
// 是两个对象，Write 落的是带空格的那一个。放行的后果正是 §3.3 那类"批准一件事、拿到
// 另一件"：一条精确到单个对象的 allow 规则 `object.write mybucket/app.log` 会放行往
// `mybucket/app.log ` 的写入。尾随空白还能整条绕过 D20 的前缀丢弃——
// `mybucket/logs/ ` 不以 "/" 收尾，却在匹配器里就是 `logs/` 这个递归前缀。
//
// exec 面早已按同一条理由在 PolicyStrings() 里拒绝这种 target（错误信息就写着
// "a padded name cannot be authorized at all"）；§6.2 要求两条入口的授权互相复用，
// 因此 cp 面必须给出同一个答案，且要在形态关口上给，不能等到主体已经生成。
func TestOSSTransfer_PaddedPathIsNotAnAuthorizableSubject(t *testing.T) {
	padded := []string{
		"/mybucket/app.log ",  // 尾随空白：与 mybucket/app.log 在匹配器里同义
		"/mybucket/logs/ ",    // 尾随空白骗过 D20 的 HasSuffix(resource, "/")
		"/mybucket/ ",         // 同上，桶级：匹配器读出来是整桶
		"/ mybucket/app.log",  // 前导空白落在桶段
		"/mybucket/app.log\t", // 制表符同样被 TrimSpace 吃掉
	}
	for _, p := range padded {
		if err := ossTransfer.ValidateDestination(p); err == nil {
			t.Errorf("ValidateDestination(%q) = nil, want an error: 这个 key 没有任何规则能授权它", p)
		}
		// 形态被拒之后主体必须退回惰性串——绝不能交出一条匹配器会 trim 成别的资源的主体。
		for _, dir := range []Direction{DirRead, DirWrite, DirList} {
			_, subject := ossTransfer.ApprovalSubject(p, dir)
			for _, victim := range []string{
				"object.write mybucket/app.log", "object.read mybucket/app.log",
				"object.write mybucket/logs/deep/secret.env",
				"object.list mybucket/logs/deep/", "object.write mybucket/anything",
			} {
				if patterns := permission.NormalizeGrantPatterns(
					asset_entity.AssetTypeOSS, subject, permission.GrantOriginSystem,
				); len(patterns) > 0 && policy.MatchOSSRule(patterns[0], victim) {
					t.Errorf("padded path %q lands as grant %q, which authorizes %q",
						p, patterns[0], victim)
				}
			}
		}
	}
	// 内部空白是合法的 key 字符，必须照旧放行（决策 D4）。
	if err := ossTransfer.ValidateDestination("/mybucket/My Report.pdf"); err != nil {
		t.Errorf("ValidateDestination on an interior space = %v, want nil", err)
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
