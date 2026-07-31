package helper

import (
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func writeTempFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func relPaths(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.RelPath)
	}
	return out
}

func joinRel(entries []Entry) string { return strings.Join(relPaths(entries), ",") }

// --- 本地端点 ---

// 单个具体文件展开成一条 entry，基点是它所在目录，因此 RelPath 就是文件名——
// `cp a.txt web-01:/tmp/` 靠这条拼出 /tmp/a.txt。
func TestLocalTransferList_SingleFile(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, filepath.Join(dir, "a.txt"), "hello")

	got, err := localTransfer.List(context.Background(), nil, filepath.Join(dir, "a.txt"), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d (%v)", len(got.Entries), relPaths(got.Entries))
	}
	e := got.Entries[0]
	if e.Path != filepath.Join(dir, "a.txt") {
		t.Errorf("Path = %q", e.Path)
	}
	if e.RelPath != "a.txt" {
		t.Errorf("RelPath = %q, want %q", e.RelPath, "a.txt")
	}
	if e.Size != 5 {
		t.Errorf("Size = %d, want 5", e.Size)
	}
}

// glob 的基点是通配前的最后一层目录（spec §6.5），所以 <dir>/logs/*.log 展开出的
// RelPath 不带 logs/ 前缀。
func TestLocalTransferList_GlobBaseIsLastLiteralDir(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, filepath.Join(dir, "logs", "a.log"), "aa")
	writeTempFile(t, filepath.Join(dir, "logs", "b.log"), "bbb")
	writeTempFile(t, filepath.Join(dir, "logs", "c.txt"), "no")
	if err := os.MkdirAll(filepath.Join(dir, "logs", "sub.log"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := localTransfer.List(context.Background(), nil, filepath.Join(dir, "logs", "*.log"), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// c.txt 不匹配；sub.log 匹配但是目录，非递归时不是可传输条目。
	if joinRel(got.Entries) != "a.log,b.log" {
		t.Fatalf("RelPaths = %v, want [a.log b.log]", relPaths(got.Entries))
	}
	if got.Entries[1].Size != 3 {
		t.Errorf("b.log Size = %d, want 3", got.Entries[1].Size)
	}
}

// 递归的基点是源目录自身（spec §6.5），RelPath 因此保留子目录层级。
func TestLocalTransferList_RecursiveBaseIsSourceItself(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dist")
	writeTempFile(t, filepath.Join(src, "a.txt"), "a")
	writeTempFile(t, filepath.Join(src, "sub", "b.txt"), "bb")
	writeTempFile(t, filepath.Join(src, "sub", "deep", "c.txt"), "ccc")

	got, err := localTransfer.List(context.Background(), nil, src, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if joinRel(got.Entries) != "a.txt,sub/b.txt,sub/deep/c.txt" {
		t.Fatalf("RelPaths = %v", relPaths(got.Entries))
	}
	if len(got.SkippedSymlinks) != 0 {
		t.Errorf("SkippedSymlinks = %v, want none", got.SkippedSymlinks)
	}
}

// 递归展开不跟随符号链接：跳过并逐条报出来，否则一条指向 / 的链接就是整机 dump。
func TestLocalTransferList_RecursiveSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tree")
	writeTempFile(t, filepath.Join(src, "real.txt"), "r")
	outside := filepath.Join(dir, "outside.txt")
	writeTempFile(t, outside, "should not be copied")
	if err := os.Symlink(outside, filepath.Join(src, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(dir, filepath.Join(src, "linkdir")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}

	got, err := localTransfer.List(context.Background(), nil, src, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if joinRel(got.Entries) != "real.txt" {
		t.Fatalf("RelPaths = %v, want [real.txt]", relPaths(got.Entries))
	}
	sort.Strings(got.SkippedSymlinks)
	want := []string{filepath.Join(src, "link.txt"), filepath.Join(src, "linkdir")}
	if strings.Join(got.SkippedSymlinks, ",") != strings.Join(want, ",") {
		t.Fatalf("SkippedSymlinks = %v, want %v", got.SkippedSymlinks, want)
	}
}

// 目录不加 recursive 直接报错，而不是静默展开成零条或一条。
func TestLocalTransferList_DirectoryWithoutRecursive(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, filepath.Join(dir, "sub", "a.txt"), "a")

	_, err := localTransfer.List(context.Background(), nil, filepath.Join(dir, "sub"), false)
	if err == nil {
		t.Fatal("expected error listing a directory without recursive")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error should name the directory case, got %q", err)
	}
}

// 写入时按需创建父目录，读回的字节与大小一致。
func TestLocalTransfer_WriteCreatesParentsAndOpenReadRoundTrips(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dst := filepath.Join(dir, "new", "deep", "f.txt")

	if err := localTransfer.Write(ctx, nil, dst, strings.NewReader("payload"), 7); err != nil {
		t.Fatalf("Write: %v", err)
	}

	r, size, err := localTransfer.OpenRead(ctx, nil, dst)
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

// 本地端没有资产，正确的调用方压根不会问它要审批主体；空返回是"不适用"。
func TestLocalTransfer_ApprovalSubjectIsNotApplicable(t *testing.T) {
	for _, dir := range []Direction{DirRead, DirWrite, DirList} {
		typ, subject := localTransfer.ApprovalSubject("/tmp/a.txt", dir)
		if typ != "" || subject != "" {
			t.Errorf("dir %v: got (%q,%q), want empty", dir, typ, subject)
		}
	}
}

// --- 适配器注册表 ---

func TestTransferAdapterFor(t *testing.T) {
	got, err := TransferAdapterFor(nil)
	if err != nil {
		t.Fatalf("nil asset: %v", err)
	}
	if got != localTransfer {
		t.Errorf("nil asset should resolve to the local adapter, got %T", got)
	}

	if _, err := TransferAdapterFor(&asset_entity.Asset{Type: asset_entity.AssetTypeSSH}); err != nil {
		t.Errorf("ssh asset: %v", err)
	}

	_, err = TransferAdapterFor(&asset_entity.Asset{Type: asset_entity.AssetTypeRedis})
	if err == nil {
		t.Fatal("expected error for an asset type with no transfer adapter")
	}
	if !strings.Contains(err.Error(), asset_entity.AssetTypeRedis) {
		t.Errorf("error should name the type, got %q", err)
	}
}

// --- SSH 端点 ---

// SSH 端点三个方向都归 cp 授权，主体是远端路径——与现状逐字节一致。
func TestSSHTransfer_ApprovalSubject(t *testing.T) {
	for _, dir := range []Direction{DirRead, DirWrite, DirList} {
		typ, subject := sshTransfer.ApprovalSubject("/var/log/app.log", dir)
		if typ != permission.GrantToolCp {
			t.Errorf("dir %v: type = %q, want %q", dir, typ, permission.GrantToolCp)
		}
		if subject != "/var/log/app.log" {
			t.Errorf("dir %v: subject = %q", dir, subject)
		}
	}
}

// fakeSFTP 是 sftpFS 的内存实现：仓内没有 SFTP 测试服务器，SSH 侧的展开规则
// （glob 基点、递归基点、symlink 跳过）只能在这一层验证。
type fakeSFTP struct {
	entries map[string]os.FileMode // 路径 → 模式（含 ModeDir / ModeSymlink）
	sizes   map[string]int64
}

type fakeInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() os.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

func (f *fakeSFTP) info(p string) fakeInfo {
	return fakeInfo{name: path.Base(p), size: f.sizes[p], mode: f.entries[p]}
}

func (f *fakeSFTP) Lstat(p string) (os.FileInfo, error) {
	if _, ok := f.entries[p]; !ok {
		return nil, os.ErrNotExist
	}
	return f.info(p), nil
}

// Stat 与 Lstat 同表：这个 fake 不建模链接目标，而用到 Stat 的分支（被指名的单个路径）
// 在测试里从不指向符号链接。
func (f *fakeSFTP) Stat(p string) (os.FileInfo, error) { return f.Lstat(p) }

func (f *fakeSFTP) ReadDir(p string) ([]os.FileInfo, error) {
	if !f.entries[p].IsDir() {
		return nil, os.ErrNotExist
	}
	out := make([]os.FileInfo, 0, len(f.entries))
	for name := range f.entries {
		if path.Dir(name) == p && name != p {
			out = append(out, f.info(name))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (f *fakeSFTP) Glob(pattern string) ([]string, error) {
	out := make([]string, 0, len(f.entries))
	for name := range f.entries {
		if ok, err := path.Match(pattern, name); err != nil {
			return nil, err
		} else if ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func newFakeSFTP() *fakeSFTP {
	return &fakeSFTP{
		entries: map[string]os.FileMode{
			"/var/log":              os.ModeDir,
			"/var/log/a.log":        0,
			"/var/log/b.log":        0,
			"/var/log/c.txt":        0,
			"/var/log/old":          os.ModeDir,
			"/var/log/old/x.log":    0,
			"/var/log/current":      os.ModeSymlink,
			"/var/log/old/link.log": os.ModeSymlink,
		},
		sizes: map[string]int64{"/var/log/a.log": 11, "/var/log/old/x.log": 22},
	}
}

func TestListSFTP_Glob(t *testing.T) {
	got, err := listSFTP(newFakeSFTP(), "/var/log/*.log", false)
	if err != nil {
		t.Fatalf("listSFTP: %v", err)
	}
	if joinRel(got.Entries) != "a.log,b.log" {
		t.Fatalf("RelPaths = %v, want [a.log b.log]", relPaths(got.Entries))
	}
	if got.Entries[0].Path != "/var/log/a.log" || got.Entries[0].Size != 11 {
		t.Errorf("entry = %+v", got.Entries[0])
	}
}

func TestListSFTP_RecursiveSkipsSymlinks(t *testing.T) {
	got, err := listSFTP(newFakeSFTP(), "/var/log", true)
	if err != nil {
		t.Fatalf("listSFTP: %v", err)
	}
	if joinRel(got.Entries) != "a.log,b.log,c.txt,old/x.log" {
		t.Fatalf("RelPaths = %v", relPaths(got.Entries))
	}
	if strings.Join(got.SkippedSymlinks, ",") != "/var/log/current,/var/log/old/link.log" {
		t.Fatalf("SkippedSymlinks = %v", got.SkippedSymlinks)
	}
}

func TestListSFTP_SingleFileAndDirectoryWithoutRecursive(t *testing.T) {
	got, err := listSFTP(newFakeSFTP(), "/var/log/a.log", false)
	if err != nil {
		t.Fatalf("listSFTP: %v", err)
	}
	if joinRel(got.Entries) != "a.log" || got.Entries[0].Size != 11 {
		t.Fatalf("entries = %+v", got.Entries)
	}

	if _, err := listSFTP(newFakeSFTP(), "/var/log", false); err == nil {
		t.Fatal("expected error listing a directory without recursive")
	}
}

// OpenRead 交出的 reader 要活过调用本身，因此它自己持有连接：Close 必须把文件与连接
// 都收掉，重复 Close 不能把已关的连接再关一次。
func TestConnBoundReadCloser_ClosesOnceAndTearsDown(t *testing.T) {
	file := &countingReadCloser{Reader: bytes.NewReader([]byte("data"))}
	var teardowns int
	rc := newConnBoundReadCloser(file, func() { teardowns++ })

	if _, err := io.ReadAll(rc); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if file.closes != 1 {
		t.Errorf("file closed %d times, want 1", file.closes)
	}
	if teardowns != 1 {
		t.Errorf("teardown ran %d times, want 1", teardowns)
	}
}

type countingReadCloser struct {
	io.Reader
	closes int
}

func (c *countingReadCloser) Close() error {
	c.closes++
	return nil
}
