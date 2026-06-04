package external_edit_svc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/service/sftp_svc"

	"github.com/stretchr/testify/require"
)

// blockingRemote 包装一个 RemoteFileService，让首次 Stat 阻塞在 gate 上。
// 它用来把 restore 阶段异步派生的 checkRemoteConflictAfterRestore goroutine
// 稳定地“钉”在执行中（该 goroutine 随后会经 bindSessionTransport 写 manifest），
// 从而确定性地验证 Close 是否会等待后台 goroutine 收尾，而不是把写盘竞争留到 Close 返回之后。
type blockingRemote struct {
	inner       RemoteFileService
	gate        chan struct{}
	releaseOnce sync.Once
	enteredOnce sync.Once
	entered     chan struct{}
}

func newBlockingRemote(inner RemoteFileService) *blockingRemote {
	return &blockingRemote{
		inner:   inner,
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}
}

func (b *blockingRemote) release() {
	b.releaseOnce.Do(func() { close(b.gate) })
}

func (b *blockingRemote) Stat(sessionID, remotePath string) (*sftp_svc.RemoteFileInfo, error) {
	b.enteredOnce.Do(func() { close(b.entered) })
	<-b.gate
	return b.inner.Stat(sessionID, remotePath)
}

func (b *blockingRemote) ReadFile(sessionID, remotePath string) ([]byte, *sftp_svc.RemoteFileInfo, error) {
	<-b.gate
	return b.inner.ReadFile(sessionID, remotePath)
}

func (b *blockingRemote) WriteFile(sessionID, remotePath string, data []byte) error {
	return b.inner.WriteFile(sessionID, remotePath, data)
}

// TestExternalEditCloseWaitsForRestoreConflictGoroutine 锁定 Close 的生命周期契约：
// Start 在 restore 时会为活跃会话异步派生 checkRemoteConflictAfterRestore，
// 该 goroutine 会经 bindSessionTransport 写 storage/manifest.json。
// 如果 Close 不等待这些后台 goroutine 收尾，它们会在 Close 返回后继续写盘，
// 与 t.TempDir 的 RemoveAll 清理竞争，产生 CI 上的 “directory not empty” 失败（PR #140）。
func TestExternalEditCloseWaitsForRestoreConflictGoroutine(t *testing.T) {
	h := newRebindHarness(t, func(int64) []string { return []string{"ssh-b"} })
	h.openSession(t, "ssh-b", "/srv/app/demo.txt", "/srv/app/demo.txt", []byte("base\n"))
	require.NoError(t, h.svc.Close())

	remote := newBlockingRemote(h.remote)
	defer remote.release() // 即使断言失败也释放阻塞的 goroutine，避免泄漏

	cfg := &bootstrap.AppConfig{
		ExternalEditDefaultEditorID: "system-text",
		ExternalEditWorkspaceRoot:   h.manifest,
	}
	reopened, err := NewService(Options{
		DataDir:        h.manifest,
		ConfigProvider: func() *bootstrap.AppConfig { return cfg },
		ConfigSaver:    func(next *bootstrap.AppConfig) error { *cfg = *next; return nil },
		Remote:         remote,
		FindSessions:   func(int64) []string { return []string{"ssh-b"} },
		Assets:         rebindAssetFinder{},
		Audit:          h.audit,
		Emit:           func(Event) {},
		Launch:         launcherFunc(func(string, []string) error { return nil }),
		Now:            func() time.Time { return h.now },
	})
	require.NoError(t, err)
	require.NoError(t, reopened.Start(context.Background()))

	// 等待 restore 派生的后台 goroutine 真正进入远程 Stat（在执行中、随后会写盘）。
	select {
	case <-remote.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("restore 冲突检测 goroutine 始终没有进入远程 Stat")
	}

	closeReturned := make(chan struct{})
	go func() {
		_ = reopened.Close()
		close(closeReturned)
	}()

	// Close 必须等待仍在执行的后台 goroutine 收尾。
	select {
	case <-closeReturned:
		t.Fatal("后台 goroutine 仍在执行时 Close 就返回了（生命周期未 join 后台任务）")
	case <-time.After(200 * time.Millisecond):
	}

	remote.release()

	select {
	case <-closeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("后台任务收尾后 Close 仍未返回")
	}
}
