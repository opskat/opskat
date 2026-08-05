package helper

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
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

	got, err := localTransfer.List(context.Background(), nil, filepath.Join(dir, "logs", "*.log"), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// c.txt 不匹配。
	if joinRel(got.Entries) != "a.log,b.log" {
		t.Fatalf("RelPaths = %v, want [a.log b.log]", relPaths(got.Entries))
	}
	if got.Entries[1].Size != 3 {
		t.Errorf("b.log Size = %d, want 3", got.Entries[1].Size)
	}
}

// 展开途中的符号链接一律跳过并报出来，glob 这一档也不例外：跟随会把用户没审阅过的路径
// 拖进传输（D19 否决跟随符号链接），而链接指向的目标可以在展开基点之外。
// SSH 端的同一条契约由 TestListSFTP_Glob 守着，本地端此前没有——把 appendLocalPath 的
// symlink 分支整个删掉，包内没有一条测试会红。
func TestLocalTransferList_GlobSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "logs")
	writeTempFile(t, filepath.Join(root, "a.log"), "aa")
	outside := filepath.Join(dir, "outside.log")
	writeTempFile(t, outside, "should not be copied")
	if err := os.Symlink(outside, filepath.Join(root, "b.log")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := localTransfer.List(context.Background(), nil, filepath.Join(root, "*.log"), false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if joinRel(got.Entries) != "a.log" {
		t.Fatalf("RelPaths = %v, want [a.log]", relPaths(got.Entries))
	}
	if strings.Join(got.SkippedSymlinks, ",") != filepath.Join(root, "b.log") {
		t.Fatalf("SkippedSymlinks = %v, want [%s]", got.SkippedSymlinks, filepath.Join(root, "b.log"))
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

// 被指名的路径跟随符号链接，递归也不例外——跳过只发生在展开途中。指名一个指向目录的链接
// （`cp -r ./current web-01:/opt/`，current → releases/v2 是部署惯例）必须展开出目标里的
// 文件，而不是把根当成一条被跳过的链接、交出零条目。
func TestLocalTransferList_NamedSymlinkedDirIsFollowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "releases", "v2")
	writeTempFile(t, filepath.Join(target, "app.bin"), "bin")
	writeTempFile(t, filepath.Join(target, "conf", "app.yml"), "yml")
	link := filepath.Join(dir, "current")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := localTransfer.List(context.Background(), nil, link, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if joinRel(got.Entries) != "app.bin,conf/app.yml" {
		t.Fatalf("RelPaths = %v, want [app.bin conf/app.yml]", relPaths(got.Entries))
	}
	if len(got.SkippedSymlinks) != 0 {
		t.Fatalf("SkippedSymlinks = %v, want none", got.SkippedSymlinks)
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

func TestLocalTransferWriteCannotFollowSymlinkOutsideApprovedScope(t *testing.T) {
	approved := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "authorized_keys")
	writeTempFile(t, outside, "original")
	if err := os.Symlink(outside, filepath.Join(approved, "file")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	ctx := WithTransferWriteScope(context.Background(), approved)
	err := localTransfer.Write(ctx, nil, filepath.Join(approved, "file"), strings.NewReader("replaced"), 8)
	if err == nil {
		t.Fatal("Write followed a symlink outside the approved destination scope")
	}
	got, readErr := os.ReadFile(outside) //nolint:gosec // 测试自己创建的临时路径
	if readErr != nil {
		t.Fatalf("read outside file: %v", readErr)
	}
	if string(got) != "original" {
		t.Fatalf("outside content = %q, want original", got)
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

func TestSSHTransfer_RecursiveUnknownPathApprovesFileAndSubtree(t *testing.T) {
	subjects := ApprovalSubjectsFor(sshTransfer, "/etc/passwd", DirReadScope)
	want := []ApprovalTarget{
		{ApprovalType: permission.GrantToolCp, Subject: "/etc/passwd"},
		{ApprovalType: permission.GrantToolCp, Subject: "/etc/passwd/"},
	}
	if !reflect.DeepEqual(subjects, want) {
		t.Fatalf("ApprovalSubjectsFor = %#v, want %#v", subjects, want)
	}
}

// TestSSHTransfer_ApprovalSubjectNarrowsGlobForListing 锁住展开方向上的收窄：List 的主体
// 不能是用户写的那个 pattern。permission 对 cp 的匹配器就是 path.Match（policy.MatchPathRule），
// 所以一条落成 "/var/log/*.log" 的 grant 会连它命中的每个文件一起授权，而 cp 的 grant 不分
// 方向 —— 用户批准的只是"列出这个目录"，换来的却是那些文件的读写。
//
// 收窄归适配器而不是调用方：只有适配器知道自己那一端哪些字符是通配语法（对象存储的前缀
// 形态 key 里它们是字面量），入口层先截一刀会把这条判断从它手里抢走。
func TestSSHTransfer_ApprovalSubjectNarrowsGlobForListing(t *testing.T) {
	for _, tt := range []struct{ name, pattern, want string }{
		{"glob narrows to the directory it expands from", "/var/log/*.log", "/var/log"},
		{"a pattern spanning segments narrows to the last literal one", "/var/*/x.log", "/var"},
		{"a plain directory is its own base", "/var/log", "/var/log"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			typ, subject := sshTransfer.ApprovalSubject(tt.pattern, DirList)
			if typ != permission.GrantToolCp {
				t.Errorf("type = %q, want %q", typ, permission.GrantToolCp)
			}
			if subject != tt.want {
				t.Errorf("subject = %q, want %q", subject, tt.want)
			}
		})
	}
}

// TestSSHTransfer_ApprovalSubjectNeverOutgrowsItsPath 咬住这个接口唯一的安全后置条件，
// 与对象存储端的同名断言（transfer_oss_test.go）是同一件事的两半：**主体落成常驻 grant
// 之后，绝不能覆盖它描述的那一条传输之外的东西。**
//
// SSH 这一端的路径**可以合法地含 glob 元字符**：`cp ./x 'web-01:/etc/*'` 写的是一个名字
// 就叫 `*` 的文件（远端 shell 没参与，引号挡住了本地展开），而递归展开同样会产出
// `a[1].log` 这种真实文件名。收窄只发生在 grant 那一步（决策 D21 更正：名字原样、规则
// 转义）——因此这条断言跨包：主体由本适配器给出，permission.NormalizeGrantPatterns 把它
// 翻成规则，policy.MatchPathRule 再按 cp 的规则语义读那条规则。两个独立来源。
//
// 方向在这里是承重的：cp 的 grant **不分方向**（一条 grant 同时授权读与写），所以一条
// 落成 "/etc/*" 的写授权会连 /etc 下每个文件的读取一起给出去。
func TestSSHTransfer_ApprovalSubjectNeverOutgrowsItsPath(t *testing.T) {
	// 一组具体路径，用来反问一条 grant"你还授权了什么"。
	probe := []string{
		"/etc", "/etc/passwd", "/etc/shadow", "/etc/cron.d",
		"/var/log/a1.log", "/var/log/app.log", "/var/log/other/deep.log",
		"/opt/app/deploy.sh",
	}
	assertGrantsNothingElse := func(t *testing.T, subject string) []string {
		t.Helper()
		patterns := permission.NormalizeGrantPatterns(
			permission.GrantToolCp, subject, permission.GrantOriginSystem)
		for _, pattern := range patterns {
			for _, other := range probe {
				if other == subject {
					continue
				}
				if policy.MatchPathRule(pattern, other) {
					t.Errorf("subject %q lands as grant %q, which also authorizes %q", subject, pattern, other)
				}
			}
		}
		return patterns
	}

	for _, tt := range []struct {
		name string
		path string
		dir  Direction
	}{
		{"a globbed destination", "/etc/*", DirWrite},
		{"a globbed source", "/var/log/*.log", DirRead},
		{"a destination whose name is a character class", "/var/log/a[1].log", DirWrite},
		{"a destination directly under the root", "/*", DirWrite},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, subject := sshTransfer.ApprovalSubject(tt.path, tt.dir)
			patterns := assertGrantsNothingElse(t, subject)
			// 往返：这条 grant 必须仍然授权它自己来自的那次传输，否则它是一条死行，
			// "始终允许"点了等于没点（决策 D21 更正记下的那次事故）。只断言"不多授权"
			// 抓不到它——一条死 grant 同样什么都不多授权。
			if len(patterns) != 1 || !policy.MatchPathRule(patterns[0], subject) {
				t.Fatalf("grants = %v, want exactly one authorizing %q itself", patterns, subject)
			}
		})
	}

	// 反过来，一条普通路径的授权照落，且它就是路径本身——否则"什么都不授权"是个廉价的
	// 通过方式，而带反斜杠的真实文件名（Linux 上合法）会落成一条谁也匹配不上的死 grant。
	for _, p := range []string{"/var/log/app.log", `/var/log/a\b.log`} {
		t.Run("a plain path grants exactly itself: "+p, func(t *testing.T) {
			_, subject := sshTransfer.ApprovalSubject(p, DirRead)
			patterns := assertGrantsNothingElse(t, subject)
			if len(patterns) != 1 || !policy.MatchPathRule(patterns[0], p) {
				t.Fatalf("grants = %v, want exactly one authorizing %q", patterns, p)
			}
		})
	}
}

// newTestSFTP 起一个进程内 SFTP 服务端（net.Pipe + sftp.NewServer）并返回连到它的客户端，
// 形态取自 internal/service/sftp_svc/sftp_test.go —— 仓内验证 SFTP 客户端代码的既有做法。
// 服务端直接服务真实文件系统，因此 t.TempDir() 的绝对路径可以直接用，
// Stat / Lstat / ReadDir / Glob 全是真实语义：手写 fake 建模不出"被指名的链接 Stat 会跟随"，
// 而那正是展开契约里最容易错的一条。
func newTestSFTP(t *testing.T) *sftp.Client {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn)
	if err != nil {
		t.Fatalf("new sftp server: %v", err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve()
	}()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatalf("new sftp client: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-served
	})
	return client
}

func TestSSHTransferReusesOneSFTPSessionPerAsset(t *testing.T) {
	root := sshTestTree(t)
	var dials atomic.Int32
	ownedCloser := &fakeCloser{}
	cache := newSFTPClientCache(func(context.Context, int64) (*sftp.Client, io.Closer, error) {
		dials.Add(1)
		return newTestSFTP(t), ownedCloser, nil
	})
	ctx := WithSFTPClientCache(context.Background(), cache)
	asset := &asset_entity.Asset{ID: 7, Type: asset_entity.AssetTypeSSH}

	listing, err := sshTransfer.List(ctx, asset, root, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, entry := range listing.Entries {
		r, _, openErr := sshTransfer.OpenRead(ctx, asset, entry.Path)
		if openErr != nil {
			t.Fatalf("OpenRead(%q): %v", entry.Path, openErr)
		}
		dst := filepath.Join(root, "copied", entry.RelPath)
		if writeErr := sshTransfer.Write(ctx, asset, dst, r, entry.Size); writeErr != nil {
			t.Fatalf("Write(%q): %v", dst, writeErr)
		}
		if closeErr := r.Close(); closeErr != nil {
			t.Fatalf("Close(%q): %v", entry.Path, closeErr)
		}
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("SFTP dials = %d, want 1 for List plus %d reads and writes on the same asset",
			got, len(listing.Entries))
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("Close cache: %v", err)
	}
	if got := ownedCloser.closed.Load(); got != 1 {
		t.Fatalf("owned SSH chain closer calls = %d, want 1", got)
	}
}

// sshTestTree 铺一棵目录树：两个 .log、一个不匹配的 .txt、一个子目录、一个名字也匹配
// *.log 的符号链接。返回 <root>/log。名字匹配通配的**目录**不放在这里：那是两端共用的
// TestTransferList_GlobMatchingDirs* 的题面，它要的是报错而不是一份条目清单。
func sshTestTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "log")
	writeTempFile(t, filepath.Join(root, "a.log"), strings.Repeat("a", 11))
	writeTempFile(t, filepath.Join(root, "b.log"), "bbb")
	writeTempFile(t, filepath.Join(root, "c.txt"), "c")
	writeTempFile(t, filepath.Join(root, "old", "x.log"), strings.Repeat("x", 22))
	if err := os.Symlink(filepath.Join(root, "a.log"), filepath.Join(root, "d.log")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "a.log"), filepath.Join(root, "old", "link.log")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	return root
}

// glob 的基点是通配前的最后一层目录；命中的符号链接同样是"展开途中"，跳过并计数。
func TestListSFTP_Glob(t *testing.T) {
	root := sshTestTree(t)

	got, err := listSFTP(newTestSFTP(t), root+"/*.log", false)
	if err != nil {
		t.Fatalf("listSFTP: %v", err)
	}
	if joinRel(got.Entries) != "a.log,b.log" {
		t.Fatalf("RelPaths = %v, want [a.log b.log]", relPaths(got.Entries))
	}
	if got.Entries[0].Path != root+"/a.log" || got.Entries[0].Size != 11 {
		t.Errorf("entry = %+v", got.Entries[0])
	}
	if strings.Join(got.SkippedSymlinks, ",") != root+"/d.log" {
		t.Fatalf("SkippedSymlinks = %v, want [%s/d.log]", got.SkippedSymlinks, root)
	}
}

// 递归的基点是源目录自身，RelPath 保留层级；展开途中的链接跳过并计数。
func TestListSFTP_RecursiveSkipsSymlinks(t *testing.T) {
	root := sshTestTree(t)

	got, err := listSFTP(newTestSFTP(t), root, true)
	if err != nil {
		t.Fatalf("listSFTP: %v", err)
	}
	if joinRel(got.Entries) != "a.log,b.log,c.txt,old/x.log" {
		t.Fatalf("RelPaths = %v", relPaths(got.Entries))
	}
	want := root + "/d.log," + root + "/old/link.log"
	if strings.Join(got.SkippedSymlinks, ",") != want {
		t.Fatalf("SkippedSymlinks = %v, want %v", got.SkippedSymlinks, want)
	}
}

func TestListSFTP_SingleFileAndDirectoryWithoutRecursive(t *testing.T) {
	root := sshTestTree(t)
	client := newTestSFTP(t)

	got, err := listSFTP(client, root+"/a.log", false)
	if err != nil {
		t.Fatalf("listSFTP: %v", err)
	}
	if joinRel(got.Entries) != "a.log" || got.Entries[0].Size != 11 {
		t.Fatalf("entries = %+v", got.Entries)
	}

	if _, err := listSFTP(client, root, false); err == nil {
		t.Fatal("expected error listing a directory without recursive")
	}
}

// 与本地端同一条契约：被指名的路径跟随符号链接，递归也不例外。两端在同一形状的输入上
// 必须给出同一份展开结果，否则 `cp -r ./current` 与 `cp -r web-01:/current` 行为分叉。
func TestListSFTP_NamedSymlinkedDirIsFollowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "releases", "v2")
	writeTempFile(t, filepath.Join(target, "app.bin"), "bin")
	writeTempFile(t, filepath.Join(target, "conf", "app.yml"), "yml")
	link := filepath.Join(dir, "current")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := listSFTP(newTestSFTP(t), link, true)
	if err != nil {
		t.Fatalf("listSFTP: %v", err)
	}
	if joinRel(got.Entries) != "app.bin,conf/app.yml" {
		t.Fatalf("RelPaths = %v, want [app.bin conf/app.yml]", relPaths(got.Entries))
	}
	if len(got.SkippedSymlinks) != 0 {
		t.Fatalf("SkippedSymlinks = %v, want none", got.SkippedSymlinks)
	}
}

// --- 两端同一个答案：非递归的 glob 命中目录 ---

// globDirTree 铺一棵"通配会命中目录"的树：一个真的 .log 文件，两个名字同样以 .log 收尾的
// 目录（各带内容）。本地端与 SFTP 端服务的是同一个真实文件系统，因此同一个绝对路径 pattern
// 可以原样喂给两端，答案能逐字节对比。
func globDirTree(t *testing.T) (root, pattern string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "logs")
	writeTempFile(t, filepath.Join(root, "a.log"), "a")
	writeTempFile(t, filepath.Join(root, "old.log", "x.log"), "xx")
	writeTempFile(t, filepath.Join(root, "sub.log", "deep", "y.log"), "yyy")
	return root, root + "/*.log"
}

// 非递归的 glob 命中目录时报错并逐个点名，而不是把它们静默丢掉。同一个 List 里"指名目录却
// 没有 recursive"那一支早就是报错，只因用户打了个 * 就换个答案是没人选过的不一致；静默略过
// 换来的是一次看起来成功、实际只传了一部分的传输（spec D19 否决静默截断）。
// 两端跑同一棵树、同一个 pattern，错误必须逐字节相同——换个端点不该换个解释。
func TestTransferList_GlobMatchingDirsWithoutRecursiveErrors(t *testing.T) {
	root, pattern := globDirTree(t)

	localRes, localErr := localTransfer.List(context.Background(), nil, pattern, false)
	sftpRes, sftpErr := listSFTP(newTestSFTP(t), pattern, false)

	for _, c := range []struct {
		endpoint string
		res      *ListResult
		err      error
	}{{"local", localRes, localErr}, {"sftp", sftpRes, sftpErr}} {
		if c.err == nil {
			t.Fatalf("%s: expected an error, got entries %v", c.endpoint, relPaths(c.res.Entries))
		}
		if c.res != nil {
			t.Errorf("%s: result = %+v, want nil：报错就不能同时交出半份清单", c.endpoint, c.res)
		}
		for _, dir := range []string{filepath.Join(root, "old.log"), filepath.Join(root, "sub.log")} {
			if !strings.Contains(c.err.Error(), dir) {
				t.Errorf("%s: error %q should name the matched directory %q", c.endpoint, c.err, dir)
			}
		}
		if !strings.Contains(c.err.Error(), "recursive") {
			t.Errorf("%s: error %q should point at recursive", c.endpoint, c.err)
		}
	}
	if localErr.Error() != sftpErr.Error() {
		t.Errorf("两端答案分叉：\n local = %q\n  sftp = %q", localErr, sftpErr)
	}
}

// recursive 时同一个 pattern 命中的目录被整棵下钻，基点仍是通配前的最后一层——报错只属于
// 非递归那一档，两端也在这一档上给同一份清单。
func TestTransferList_GlobMatchingDirsWithRecursiveDescends(t *testing.T) {
	_, pattern := globDirTree(t)
	const want = "a.log,old.log/x.log,sub.log/deep/y.log"

	localRes, err := localTransfer.List(context.Background(), nil, pattern, true)
	if err != nil {
		t.Fatalf("local List: %v", err)
	}
	if joinRel(localRes.Entries) != want {
		t.Errorf("local RelPaths = %v, want %q", relPaths(localRes.Entries), want)
	}

	sftpRes, err := listSFTP(newTestSFTP(t), pattern, true)
	if err != nil {
		t.Fatalf("listSFTP: %v", err)
	}
	if joinRel(sftpRes.Entries) != want {
		t.Errorf("sftp RelPaths = %v, want %q", relPaths(sftpRes.Entries), want)
	}
}

// SSH 端的流式往返：写入时按需建父目录，读回的字节与 size 一致，Close 拆连接恰好一次。
func TestSFTPWriteCreatesParentsAndReadRoundTrips(t *testing.T) {
	client := newTestSFTP(t)
	dst := filepath.Join(t.TempDir(), "new", "deep", "f.txt")

	if err := writeSFTP(context.Background(), client, dst, strings.NewReader("payload")); err != nil {
		t.Fatalf("writeSFTP: %v", err)
	}

	var teardowns int
	r, size, err := openSFTPReader(context.Background(), client, dst, func() { teardowns++ })
	if err != nil {
		t.Fatalf("openSFTPReader: %v", err)
	}
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
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if teardowns != 1 {
		t.Errorf("teardown ran %d times after Close, want 1", teardowns)
	}
}

// 打开失败时连接必须照样拆掉：OpenRead 是先 dial 再打开的，漏一次 teardown 就是每次读
// 失败漏一条 SSH 连接。
func TestSFTPOpenReaderTearsDownWhenOpenFails(t *testing.T) {
	client := newTestSFTP(t)
	var teardowns int

	r, size, err := openSFTPReader(
		context.Background(), client, filepath.Join(t.TempDir(), "missing.txt"), func() { teardowns++ },
	)
	if err == nil {
		t.Fatal("expected an error opening a missing remote file")
	}
	if r != nil || size != 0 {
		t.Errorf("got reader=%v size=%d, want nil/0", r, size)
	}
	if teardowns != 1 {
		t.Fatalf("teardown ran %d times, want 1", teardowns)
	}
}

// OpenRead 交出的 reader 要活过调用本身，因此它自己持有连接：Close 必须把文件与连接
// 都收掉，重复 Close 不能把已关的连接再关一次。
func TestConnBoundReadCloser_ClosesOnceAndTearsDown(t *testing.T) {
	file := &countingReadCloser{Reader: bytes.NewReader([]byte("data"))}
	var teardowns int
	rc := newConnBoundReadCloser(context.Background(), file, func() { teardowns++ })

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

// ctx 取消时 closeOnCancel 会把连接拆掉，阻塞在 reader 上的 Read 因此拿到 net.ErrClosed。
// 与 ssh_helper.go 的 ExecuteWithSFTP 一条规矩：取消路径交出 ctx.Err()，
// 不把底层的 "use of closed network connection" 暴露给上层；非取消错误原样透传。
func TestConnBoundReadCloser_ReadReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc := newConnBoundReadCloser(ctx, errReadCloser{err: net.ErrClosed}, func() {})
	defer func() { _ = rc.Close() }()

	if _, err := rc.Read(make([]byte, 4)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Read before cancel = %v, want the underlying error", err)
	}
	cancel()
	if _, err := rc.Read(make([]byte, 4)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read after cancel = %v, want context.Canceled", err)
	}
}

type errReadCloser struct{ err error }

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error             { return nil }

type countingReadCloser struct {
	io.Reader
	closes int
}

func (c *countingReadCloser) Close() error {
	c.closes++
	return nil
}
