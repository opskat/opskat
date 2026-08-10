package sftp_svc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/opskat/opskat/internal/pkg/transfer"
	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestService 起一个进程内的真实 SFTP 服务端（根目录为临时目录），返回服务、
// 会话 ID 和根目录。用真服务端而不是手写 fake，是因为这些用例要验证的正是
// 权限继承、符号链接、rename 语义这些只有真实文件系统才说得清的行为。
func newTestService(t *testing.T) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()

	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn, sftp.WithServerWorkingDirectory(root))
	require.NoError(t, err)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve() }()

	client, err := sftp.NewClientPipe(clientConn, clientConn, sftpClientOptions()...)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		<-serverDone
	})

	service := NewService(nil)
	service.clients.Store("session", client)
	return service, "session", root
}

// discardProgress 供不关心进度的用例使用。onProgress 是必填的：生产端
// （internal/app/ssh/sftp.go）总会传一个真实回调，服务层不为它加 nil 兜底。
func discardProgress(transfer.Progress) {}

// collectProgress 收集进度事件，供断言整次传输的进度不变量。
func collectProgress() (func(transfer.Progress), *[]transfer.Progress) {
	var got []transfer.Progress
	return func(p transfer.Progress) { got = append(got, p) }, &got
}

// assertProgressInvariants 断言一次传输的进度事件满足：BytesTotal 恒为整次总量、
// BytesDone 单调递增且不越过总量。
func assertProgressInvariants(t *testing.T, events []transfer.Progress, wantTotal int64) {
	t.Helper()
	require.NotEmpty(t, events, "至少要有一条进度事件")
	var prev int64
	for i, p := range events {
		assert.Equal(t, wantTotal, p.BytesTotal, "第 %d 条：BytesTotal 必须是整次传输总量", i)
		assert.GreaterOrEqual(t, p.BytesDone, prev, "第 %d 条：BytesDone 必须单调递增", i)
		assert.LessOrEqual(t, p.BytesDone, p.BytesTotal, "第 %d 条：BytesDone 不得越过总量", i)
		prev = p.BytesDone
	}
}

func TestUploadPreservesExistingFilePermissions(t *testing.T) {
	service, session, root := newTestService(t)
	local := filepath.Join(t.TempDir(), "id_rsa")
	require.NoError(t, os.WriteFile(local, []byte("new-key"), 0o600))

	remote := filepath.Join(root, "id_rsa")
	require.NoError(t, os.WriteFile(remote, []byte("old-key"), 0o600))

	require.NoError(t, service.Upload(context.Background(), "t1", session, local, remote, discardProgress))

	info, err := os.Stat(remote)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "覆盖上传不得放宽已有文件的权限")
	content, err := os.ReadFile(remote) //nolint:gosec // 测试内的临时目录路径
	require.NoError(t, err)
	assert.Equal(t, "new-key", string(content))
}

func TestUploadWritesThroughSymlinkInsteadOfReplacingIt(t *testing.T) {
	// 原地覆盖的语义是"写穿到链接指向的文件"。若原子替换作用在链接本身，
	// 链接会被换成普通文件 —— sites-enabled/default 这类布局会被悄悄破坏。
	service, session, root := newTestService(t)
	local := filepath.Join(t.TempDir(), "site.conf")
	require.NoError(t, os.WriteFile(local, []byte("server {}"), 0o644))

	target := filepath.Join(root, "sites-available")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o644))
	link := filepath.Join(root, "sites-enabled")
	require.NoError(t, os.Symlink("sites-available", link))

	require.NoError(t, service.Upload(context.Background(), "t1", session, local, link, discardProgress))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "符号链接必须仍然是符号链接")
	content, err := os.ReadFile(target) //nolint:gosec // 测试内的临时目录路径
	require.NoError(t, err)
	assert.Equal(t, "server {}", string(content), "内容应写穿到链接指向的文件")
}

func TestWriteFileWritesThroughSymlinkInsteadOfReplacingIt(t *testing.T) {
	// 外部编辑回写走的是同一套原子替换。编辑一个软链过去的配置文件时，
	// 保存不能把软链本身换成普通文件。
	service, session, root := newTestService(t)
	target := filepath.Join(root, "app.conf")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o640))
	link := filepath.Join(root, "current.conf")
	require.NoError(t, os.Symlink("app.conf", link))

	require.NoError(t, service.WriteFile(session, link, []byte("edited")))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "符号链接必须仍然是符号链接")
	content, err := os.ReadFile(target) //nolint:gosec // 测试内的临时目录路径
	require.NoError(t, err)
	assert.Equal(t, "edited", string(content))

	targetInfo, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), targetInfo.Mode().Perm(), "回写不得改变被编辑文件的权限")
}

func TestUploadRejectsNamedPipeTargetInsteadOfReplacingIt(t *testing.T) {
	// SFTP 的写请求带绝对偏移量，服务端对管道做 pwrite 会返回 illegal seek ——
	// 命名管道根本无法经 SFTP 写入。必须明确报错：既不能把管道 rename 成普通文件，
	// 也不能因为上游 ReadFrom 吞掉写错误而静默报告成功。
	service, session, root := newTestService(t)
	local := filepath.Join(t.TempDir(), "payload")
	require.NoError(t, os.WriteFile(local, []byte("hello"), 0o644))

	fifo := filepath.Join(root, "pipe")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	err := service.Upload(context.Background(), "t1", session, local, fifo, discardProgress)

	require.Error(t, err, "SFTP 无法写入命名管道，不能报告成功")
	assert.Contains(t, err.Error(), "命名管道")
	info, err := os.Lstat(fifo)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeNamedPipe, "命名管道必须原样保留，不能被换成普通文件")
}

// shortWriteClient 模拟"ReadFrom 报告写完了，实际落盘字节数不足"的服务端 ——
// 即上游 ReadFrom 吞掉最后一片写入错误时，普通文件上会呈现的样子。
type shortWriteClient struct {
	remoteAtomicClient
	actualSize int64
	renamed    bool
}

type nopWriter struct{}

func (w nopWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w nopWriter) ReadFrom(r io.Reader) (int64, error) {
	n, _ := io.Copy(io.Discard, r)
	return n, nil // 谎称全部写入成功
}
func (w nopWriter) Truncate(int64) error { return nil }
func (w nopWriter) Close() error         { return nil }

func (c *shortWriteClient) OpenFile(string, int) (remoteAtomicWriter, error) {
	return nopWriter{}, nil
}
func (c *shortWriteClient) Lstat(string) (os.FileInfo, error) {
	return nil, os.ErrNotExist // 目标不存在，走"新建 + rename"路径
}
func (c *shortWriteClient) Stat(string) (os.FileInfo, error) {
	return shortFileInfo{size: c.actualSize}, nil
}
func (c *shortWriteClient) Rename(string, string) error { c.renamed = true; return nil }
func (c *shortWriteClient) Remove(string) error         { return nil }

type shortFileInfo struct {
	os.FileInfo
	size int64
}

func (i shortFileInfo) Size() int64       { return i.size }
func (i shortFileInfo) Mode() os.FileMode { return 0o644 }
func (i shortFileInfo) IsDir() bool       { return false }
func (i shortFileInfo) Sys() any          { return nil }

func TestUploadFailsWhenRemoteSizeDoesNotMatch(t *testing.T) {
	client := &shortWriteClient{actualSize: 3}
	tracker := transfer.NewTracker("t1", 1, 5, discardProgress)
	svc := NewService(nil)

	err := svc.uploadFile(context.Background(), tracker, client,
		bytes.NewReader([]byte("hello")), "/srv/app.conf", "app.conf", 5)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "大小校验失败")
	assert.False(t, client.renamed, "字节没写全时不得把临时文件切换成目标文件")
}

// failingCopyClient 在写到第 failAfter 个字节后让写入失败，模拟并发写中途出错。
type failingCopyClient struct {
	remoteCopyClient
	files     map[string][]byte
	failAfter int
	removed   []string
}

type failingWriter struct {
	client *failingCopyClient
	path   string
	limit  int
}

func (w *failingWriter) Write(b []byte) (int, error) {
	cur := w.client.files[w.path]
	room := w.limit - len(cur)
	if room <= 0 {
		return 0, errors.New("disk quota exceeded")
	}
	if len(b) > room {
		w.client.files[w.path] = append(cur, b[:room]...)
		return room, errors.New("disk quota exceeded")
	}
	w.client.files[w.path] = append(cur, b...)
	return len(b), nil
}

func (w *failingWriter) Close() error { return nil }

func (c *failingCopyClient) Open(p string) (io.ReadCloser, error) {
	body, ok := c.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (c *failingCopyClient) Create(p string) (io.WriteCloser, error) {
	c.files[p] = nil
	return &failingWriter{client: c, path: p, limit: c.failAfter}, nil
}

func (c *failingCopyClient) Stat(p string) (os.FileInfo, error) {
	body, ok := c.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return shortFileInfo{size: int64(len(body))}, nil
}

func (c *failingCopyClient) Remove(p string) error {
	c.removed = append(c.removed, p)
	delete(c.files, p)
	return nil
}

func (c *failingCopyClient) Chmod(string, os.FileMode) error { return nil }

func TestCopyRemoteFileRemovesPartialDestinationOnFailure(t *testing.T) {
	// 客户端开了并发写后，中途失败会在目标文件里留下空洞且长度看似正常。
	// 粘贴失败必须把半成品删掉 —— 留一个同名、看起来完整实则损坏的文件最危险。
	client := &failingCopyClient{
		files:     map[string][]byte{"/src/app.bin": bytes.Repeat([]byte("x"), 4096)},
		failAfter: 1024,
	}

	err := copyRemoteFile(context.Background(), client, client, "/src/app.bin", "/dst/app.bin")

	require.Error(t, err)
	assert.Contains(t, client.removed, "/dst/app.bin", "失败的粘贴不得留下半成品")
	assert.NotContains(t, client.files, "/dst/app.bin")
}

func TestUploadFallsBackToInPlaceWhenDirectoryNotWritable(t *testing.T) {
	// 只有文件写权限、没有父目录写权限时建不了临时文件。
	// 旧实现直接打开目标文件就能写，这里必须退回该路径而不是硬失败。
	if os.Geteuid() == 0 {
		t.Skip("root 绕过目录权限检查，该用例无法复现")
	}
	service, session, root := newTestService(t)
	local := filepath.Join(t.TempDir(), "nginx.conf")
	require.NoError(t, os.WriteFile(local, []byte("worker_processes 4;"), 0o644))

	dir := filepath.Join(root, "nginx")
	require.NoError(t, os.Mkdir(dir, 0o755))
	remote := filepath.Join(dir, "nginx.conf")
	require.NoError(t, os.WriteFile(remote, []byte("worker_processes 1;"), 0o644))
	require.NoError(t, os.Chmod(dir, 0o555)) // 目录不可写，文件仍可写
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	require.NoError(t, service.Upload(context.Background(), "t1", session, local, remote, discardProgress))

	content, err := os.ReadFile(remote) //nolint:gosec // 测试内的临时目录路径
	require.NoError(t, err)
	assert.Equal(t, "worker_processes 4;", string(content))
}

func TestUploadDirProgressNeverExceedsTotal(t *testing.T) {
	// 回归 #272：UploadDir 曾把当前文件大小当作 bytesTotal 传下去，
	// 传到第 3 个文件时前端会渲染出 250%。
	service, session, root := newTestService(t)
	localDir := filepath.Join(t.TempDir(), "src")
	require.NoError(t, os.Mkdir(localDir, 0o755))
	const files, size = 3, 40 * 1024
	for i := range files {
		body := make([]byte, size)
		require.NoError(t, os.WriteFile(filepath.Join(localDir, fmt.Sprintf("f%d.bin", i)), body, 0o644))
	}

	onProgress, got := collectProgress()
	require.NoError(t, service.UploadDir(context.Background(), "t1", session, localDir, filepath.Join(root, "dst"), onProgress))

	assertProgressInvariants(t, *got, files*size)
	for i := range files {
		info, err := os.Stat(filepath.Join(root, "dst", fmt.Sprintf("f%d.bin", i)))
		require.NoError(t, err)
		assert.Equal(t, int64(size), info.Size())
	}
}

func TestDownloadDirProgressNeverRestartsPerFile(t *testing.T) {
	// 回归 #272：downloadFileAtomic 没有整次总量这个参数，每个文件都把
	// BytesTotal 算成"已完成 + 当前文件"，进度条每传完一个文件就跑满一次 100%。
	service, session, root := newTestService(t)
	remoteDir := filepath.Join(root, "remote")
	require.NoError(t, os.Mkdir(remoteDir, 0o755))
	const files, size = 3, 40 * 1024
	for i := range files {
		require.NoError(t, os.WriteFile(filepath.Join(remoteDir, fmt.Sprintf("f%d.bin", i)), make([]byte, size), 0o644))
	}
	localDir := filepath.Join(t.TempDir(), "dst")

	onProgress, got := collectProgress()
	require.NoError(t, service.DownloadDir(context.Background(), "t1", session, remoteDir, localDir, onProgress))

	assertProgressInvariants(t, *got, files*size)
	for i := range files {
		info, err := os.Stat(filepath.Join(localDir, fmt.Sprintf("f%d.bin", i)))
		require.NoError(t, err)
		assert.Equal(t, int64(size), info.Size())
	}
}

func TestDownloadPreservesExistingLocalPermissions(t *testing.T) {
	// 临时文件硬编码 0644 再 rename 覆盖，会把本地 0600 的私钥放宽成世界可读。
	service, session, root := newTestService(t)
	remote := filepath.Join(root, "id_rsa")
	require.NoError(t, os.WriteFile(remote, []byte("remote-key"), 0o600))
	local := filepath.Join(t.TempDir(), "id_rsa")
	require.NoError(t, os.WriteFile(local, []byte("old"), 0o600))

	require.NoError(t, service.Download(context.Background(), "t1", session, remote, local, discardProgress))

	info, err := os.Stat(local)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "覆盖下载不得放宽已有文件的权限")
	content, err := os.ReadFile(local) //nolint:gosec // 测试内的临时目录路径
	require.NoError(t, err)
	assert.Equal(t, "remote-key", string(content))
}

func TestDownloadLeavesNoTempFileBehindOnCancel(t *testing.T) {
	service, session, root := newTestService(t)
	remote := filepath.Join(root, "big.bin")
	require.NoError(t, os.WriteFile(remote, make([]byte, 256*1024), 0o644))
	localDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := service.Download(ctx, "t1", session, remote, filepath.Join(localDir, "big.bin"), discardProgress)
	require.Error(t, err)

	entries, err := os.ReadDir(localDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "取消后不得留下 .opskat-part-* 临时文件")
}

// --- 不支持 PosixRename 的服务端：进程内服务端支持该扩展，只能用 fake 驱动回退路径 ---

// noPosixRenameClient 模拟不支持 posix-rename@openssh.com 的服务端。
// renameErrs 按 Rename 调用次序注入错误：第 1 次是备份（目标 → .bak），
// 第 2 次是切换（.part → 目标），第 3 次是恢复（.bak → 目标）。
type noPosixRenameClient struct {
	remoteAtomicClient
	renameErrs []error
	renames    [][2]string
	removed    []string
}

func (c *noPosixRenameClient) PosixRename(string, string) error {
	return &sftp.StatusError{Code: uint32(sftp.ErrSSHFxOpUnsupported)}
}

func (c *noPosixRenameClient) Rename(oldname, newname string) error {
	c.renames = append(c.renames, [2]string{oldname, newname})
	if len(c.renames) <= len(c.renameErrs) {
		return c.renameErrs[len(c.renames)-1]
	}
	return nil
}

func (c *noPosixRenameClient) Remove(p string) error {
	c.removed = append(c.removed, p)
	return nil
}

func (c *noPosixRenameClient) Chmod(string, os.FileMode) error { return nil }

func TestCommitRemoteTempFileReportsRestoreFailureAndCleansBackup(t *testing.T) {
	// PosixRename 不被支持 → 走"备份 → 切换 → 尽力恢复"。
	// 若切换和恢复都失败，目标路径已经没了，必须把恢复失败也报出来，
	// 并清理掉那个用户无从得知的 .opskat-bak-* 文件。
	swapErr := errors.New("quota exceeded")
	restoreErr := errors.New("read-only file system")
	client := &noPosixRenameClient{renameErrs: []error{nil, swapErr, restoreErr}}

	err := commitRemoteTempFile(client, "/etc/.app.conf.opskat-part-1", "/etc/app.conf", 0o644, true)

	require.Error(t, err)
	assert.ErrorIs(t, err, swapErr)
	assert.Contains(t, err.Error(), "恢复原文件失败", "恢复失败必须让用户看见")
	assert.Contains(t, err.Error(), restoreErr.Error())

	require.Len(t, client.renames, 3)
	backupPath := client.renames[0][1]
	assert.NotContains(t, client.removed, backupPath, "恢复失败时备份是原文件仅存的一份，删掉就是静默的数据丢失")
	assert.Contains(t, err.Error(), backupPath, "必须把备份路径告诉用户，否则原文件停在无从得知的隐藏名字下")
}

func TestCommitRemoteTempFileRestoresOriginalOnSwapFailure(t *testing.T) {
	swapErr := errors.New("quota exceeded")
	client := &noPosixRenameClient{renameErrs: []error{nil, swapErr, nil}}

	err := commitRemoteTempFile(client, "/etc/.app.conf.opskat-part-1", "/etc/app.conf", 0o644, true)

	require.Error(t, err)
	assert.ErrorIs(t, err, swapErr)
	assert.Contains(t, err.Error(), "已恢复原文件")

	require.Len(t, client.renames, 3)
	backupPath := client.renames[0][1]
	assert.Equal(t, [2]string{backupPath, "/etc/app.conf"}, client.renames[2], "第三次 rename 必须是把备份恢复回目标路径")
}
