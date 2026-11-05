package sftp_svc

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/pkg/sftpio/sftpiotest"
	"github.com/opskat/opskat/internal/pkg/transfer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dirTreeFiles 是目录并发用例的文件数：足够多，让"串行每个文件若干往返"的耗时
// 与并发之后的耗时在毫秒级延迟下拉开数倍差距。
const dirTreeFiles = 48

const dirTreeRTT = 10 * time.Millisecond

// writeDirTree 在 dir 下建一棵两层目录树（含一个空目录），返回 相对路径 -> 内容。
// 文件落在多层子目录里：目录若没有先于其下文件建立，传输就会失败。
func writeDirTree(t *testing.T, dir string, n int) map[string]string {
	t.Helper()
	want := make(map[string]string, n)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "empty"), 0o755))
	for i := range n {
		rel := filepath.Join(fmt.Sprintf("d%d", i%2), "sub", fmt.Sprintf("f%02d.txt", i))
		body := strings.Repeat(fmt.Sprintf("file-%d;", i), i+1)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644))
		want[rel] = body
	}
	return want
}

func treeBytes(want map[string]string) int64 {
	var total int64
	for _, body := range want {
		total += int64(len(body))
	}
	return total
}

func assertDirTree(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, "empty"))
	require.NoError(t, err, "空目录也要建立")
	assert.True(t, info.IsDir())
	got := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		require.NoError(t, err)
		body, err := os.ReadFile(p) //nolint:gosec // 测试内的临时目录路径
		require.NoError(t, err)
		got[rel] = string(body)
		return nil
	}))
	assert.Equal(t, want, got, "目标树必须与源树逐文件一致，且没有残留的临时文件")
}

// newLatencyService 用 task-5 的延迟桩（客户端每个请求延迟 rtt 送达）起一个服务。
func newLatencyService(t *testing.T, root string, rtt time.Duration) *Service {
	t.Helper()
	client, _, closeServer := newProbeClient(t, root, rtt)
	t.Cleanup(func() { _ = client.Close(); closeServer() })
	svc := NewService(nil)
	svc.clients.Store("s", client)
	return svc
}

func TestUploadDirTransfersFilesConcurrently(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "src")
	want := writeDirTree(t, localDir, dirTreeFiles)
	root := t.TempDir()
	svc := newLatencyService(t, root, dirTreeRTT)

	onProgress, got := collectProgress()
	start := time.Now()
	require.NoError(t, svc.UploadDir(context.Background(), "t1", "s", localDir, sftpiotest.RemotePath(filepath.Join(root, "dst")), onProgress))
	elapsed := time.Since(start)

	// 串行时每个新文件至少 5 个往返（Lstat、打开临时文件、写、Close、Stat 复核、Rename），
	// 每个往返都被延迟桩实打实地等满，所以串行耗时不可能低于这个下限；并发度本身由
	// TestTransferDirRunsWorkersConcurrently 确定性地断言，这里只用下限，机器再忙也不误报。
	serialFloor := time.Duration(dirTreeFiles) * 5 * dirTreeRTT
	t.Logf("UploadDir %d files @%s: %s (serial floor %s)", dirTreeFiles, dirTreeRTT, elapsed, serialFloor)
	assert.Less(t, elapsed, serialFloor, "文件之间的往返必须重叠")
	assertDirTree(t, filepath.Join(root, "dst"), want)
	assertProgressInvariants(t, *got, treeBytes(want))
}

func TestDownloadDirTransfersFilesConcurrently(t *testing.T) {
	root := t.TempDir()
	remoteDir := filepath.Join(root, "remote")
	want := writeDirTree(t, remoteDir, dirTreeFiles)
	svc := newLatencyService(t, root, dirTreeRTT)
	localDir := filepath.Join(t.TempDir(), "dst")

	onProgress, got := collectProgress()
	start := time.Now()
	require.NoError(t, svc.DownloadDir(context.Background(), "t1", "s", sftpiotest.RemotePath(remoteDir), localDir, onProgress))
	elapsed := time.Since(start)

	// 串行时每个文件至少 4 个往返（Open、WriteTo 的 fstat、读、Close）；扫描阶段的 ReadDir 另计。
	serialFloor := time.Duration(dirTreeFiles) * 4 * dirTreeRTT
	t.Logf("DownloadDir %d files @%s: %s (serial floor %s, excl. scan)", dirTreeFiles, dirTreeRTT, elapsed, serialFloor)
	assert.Less(t, elapsed, serialFloor, "文件之间的往返必须重叠")
	assertDirTree(t, localDir, want)
	assertProgressInvariants(t, *got, treeBytes(want))
}

// transferDir 同时在传 dirTransferWorkers 个文件，且不超过这个数：每个 send 都要等到这么多个
// 同时在传才放行，并发度不足时卡到超时失败，与机器快慢无关。
func TestTransferDirRunsWorkersConcurrently(t *testing.T) {
	entries := make([]dirEntry, 0, 1+dirTransferWorkers*3)
	entries = append(entries, dirEntry{src: "d", rel: "d", isDir: true})
	for i := range dirTransferWorkers * 3 {
		name := fmt.Sprintf("d/f%02d", i)
		entries = append(entries, dirEntry{src: name, rel: name, size: 1})
	}
	var inFlight, peak, sent atomic.Int32
	full := make(chan struct{})
	var fullOnce sync.Once
	send := func(ctx context.Context, _ dirEntry) error {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		if n == dirTransferWorkers {
			fullOnce.Do(func() { close(full) })
		}
		select {
		case <-full:
			sent.Add(1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, transferDir(ctx, entries, func(dirEntry) error { return nil }, send))
	assert.EqualValues(t, dirTransferWorkers, peak.Load())
	assert.EqualValues(t, dirTransferWorkers*3, sent.Load())
}

func TestDirTransferOfEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	svc := newLatencyService(t, root, 0)
	src := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.Mkdir(src, 0o755))

	remote := filepath.Join(root, "up")
	require.NoError(t, svc.UploadDir(context.Background(), "t1", "s", src, sftpiotest.RemotePath(remote), discardProgress))
	assertEmptyDir(t, remote)

	local := filepath.Join(t.TempDir(), "down")
	require.NoError(t, svc.DownloadDir(context.Background(), "t2", "s", sftpiotest.RemotePath(remote), local, discardProgress))
	assertEmptyDir(t, local)
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// countFiles 数出 dir 下的普通文件（含临时文件）。
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	require.NoError(t, filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	}))
	return n
}

func TestUploadDirCancelStopsAllWorkers(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "src")
	writeDirTree(t, localDir, dirTreeFiles)
	root := t.TempDir()
	svc := newLatencyService(t, root, dirTreeRTT)
	dst := filepath.Join(root, "dst")

	canceled := false
	onProgress := func(transfer.Progress) {
		if !canceled {
			canceled = true
			svc.Cancel("t1")
		}
	}
	err := svc.UploadDir(context.Background(), "t1", "s", localDir, sftpiotest.RemotePath(dst), onProgress)
	require.ErrorIs(t, err, context.Canceled)

	// 返回时所有 worker 都已退出：之后远端不再出现新文件，也不留临时文件。
	after := countFiles(t, dst)
	time.Sleep(10 * dirTreeRTT)
	assert.Equal(t, after, countFiles(t, dst), "UploadDir 返回后不得还有 worker 在写")
	assert.Less(t, after, dirTreeFiles, "取消后不得把剩余文件传完")
	require.NoError(t, filepath.WalkDir(dst, func(p string, _ fs.DirEntry, err error) error {
		assert.NotContains(t, filepath.Base(p), ".opskat-part-", "取消的文件必须清理临时文件")
		return err
	}))
}

func TestDownloadDirCancelStopsAllWorkers(t *testing.T) {
	root := t.TempDir()
	remoteDir := filepath.Join(root, "remote")
	writeDirTree(t, remoteDir, dirTreeFiles)
	svc := newLatencyService(t, root, dirTreeRTT)
	localDir := filepath.Join(t.TempDir(), "dst")

	canceled := false
	onProgress := func(transfer.Progress) {
		if !canceled {
			canceled = true
			svc.Cancel("t1")
		}
	}
	err := svc.DownloadDir(context.Background(), "t1", "s", sftpiotest.RemotePath(remoteDir), localDir, onProgress)
	require.ErrorIs(t, err, context.Canceled)

	after := countFiles(t, localDir)
	time.Sleep(10 * dirTreeRTT)
	assert.Equal(t, after, countFiles(t, localDir), "DownloadDir 返回后不得还有 worker 在写")
	assert.Less(t, after, dirTreeFiles, "取消后不得把剩余文件传完")
	require.NoError(t, filepath.WalkDir(localDir, func(p string, _ fs.DirEntry, err error) error {
		assert.NotContains(t, filepath.Base(p), ".opskat-part-", "取消的文件必须清理临时文件")
		return err
	}))
}

func skipIfPermissionsIgnored(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("需要能让 chmod 000 生效的环境")
	}
}

func TestUploadDirReturnsFileError(t *testing.T) {
	skipIfPermissionsIgnored(t)
	localDir := filepath.Join(t.TempDir(), "src")
	writeDirTree(t, localDir, dirTreeFiles)
	unreadable := filepath.Join(localDir, "d0", "sub", "f04.txt")
	require.NoError(t, os.Chmod(unreadable, 0))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })
	root := t.TempDir()
	svc := newLatencyService(t, root, dirTreeRTT)

	err := svc.UploadDir(context.Background(), "t1", "s", localDir, sftpiotest.RemotePath(filepath.Join(root, "dst")), discardProgress)
	require.ErrorIs(t, err, fs.ErrPermission, "失败文件的错误必须原样返回，而不是其余 worker 被取消的 context.Canceled")
	assert.Less(t, countFiles(t, filepath.Join(root, "dst")), dirTreeFiles-1, "一个文件失败后其余文件应停止")
}

func TestDownloadDirReturnsFileError(t *testing.T) {
	skipIfPermissionsIgnored(t)
	root := t.TempDir()
	remoteDir := filepath.Join(root, "remote")
	writeDirTree(t, remoteDir, dirTreeFiles)
	unreadable := filepath.Join(remoteDir, "d0", "sub", "f04.txt")
	require.NoError(t, os.Chmod(unreadable, 0))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })
	svc := newLatencyService(t, root, dirTreeRTT)
	localDir := filepath.Join(t.TempDir(), "dst")

	err := svc.DownloadDir(context.Background(), "t1", "s", sftpiotest.RemotePath(remoteDir), localDir, discardProgress)
	require.ErrorIs(t, err, fs.ErrPermission, "失败文件的错误必须原样返回，而不是其余 worker 被取消的 context.Canceled")
	assert.Less(t, countFiles(t, localDir), dirTreeFiles-1, "一个文件失败后其余文件应停止")
}
