package localterm_svc

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeProc struct {
	mu      sync.Mutex
	writes  [][]byte
	resizes [][2]int
	closed  bool
	closeN  int
	readCh  chan []byte // 推送 fake 输出
}

func newFakeProc() *fakeProc { return &fakeProc{readCh: make(chan []byte, 16)} }

func (p *fakeProc) Read(b []byte) (int, error) {
	chunk, ok := <-p.readCh
	if !ok {
		return 0, io.EOF
	}
	n := copy(b, chunk)
	return n, nil
}

func (p *fakeProc) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	p.writes = append(p.writes, cp)
	return len(b), nil
}

func (p *fakeProc) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resizes = append(p.resizes, [2]int{cols, rows})
	return nil
}

func (p *fakeProc) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		p.closeN++
		close(p.readCh)
	}
	return nil
}

func withFakeStartPTY(t *testing.T, proc *fakeProc) {
	orig := startPTYFn
	startPTYFn = func(ptySpec) (ptyProcess, error) { return proc, nil }
	t.Cleanup(func() { startPTYFn = orig })
}

func TestManagerConnectAndStreamData(t *testing.T) {
	proc := newFakeProc()
	withFakeStartPTY(t, proc)

	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 7, Cols: 80, Rows: 24})
	require.NoError(t, err)

	got := make(chan []byte, 4)
	mgr.SetCallbacks(sid, func(b []byte) { got <- b }, nil)

	proc.readCh <- []byte("hello")
	select {
	case b := <-got:
		assert.Equal(t, "hello", string(b))
	case <-time.After(time.Second):
		t.Fatal("未收到 onData 回调")
	}
}

func TestSessionWriteAndResize(t *testing.T) {
	proc := newFakeProc()
	withFakeStartPTY(t, proc)
	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 1})
	require.NoError(t, err)
	mgr.SetCallbacks(sid, func([]byte) {}, nil)

	sess, ok := mgr.GetSession(sid)
	require.True(t, ok)
	require.NoError(t, sess.Write([]byte("ls\n")))
	require.NoError(t, sess.Resize(120, 40))

	proc.mu.Lock()
	defer proc.mu.Unlock()
	assert.Equal(t, [][]byte{[]byte("ls\n")}, proc.writes)
	assert.Equal(t, [][2]int{{120, 40}}, proc.resizes)
}

func TestReadEOFTriggersClosedCallback(t *testing.T) {
	proc := newFakeProc()
	withFakeStartPTY(t, proc)
	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 2})
	require.NoError(t, err)

	closed := make(chan string, 1)
	mgr.SetCallbacks(sid, func([]byte) {}, func(s string) { closed <- s })

	// 模拟 shell 退出:关闭 readCh → Read 返回 EOF。
	_ = proc.Close()

	select {
	case s := <-closed:
		assert.Equal(t, sid, s)
	case <-time.After(time.Second):
		t.Fatal("EOF 未触发 onClosed")
	}
	_, ok := mgr.GetSession(sid)
	assert.False(t, ok, "会话应已从 manager 移除")
}

func TestDisconnectClosesProc(t *testing.T) {
	proc := newFakeProc()
	withFakeStartPTY(t, proc)
	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 3})
	require.NoError(t, err)
	mgr.SetCallbacks(sid, func([]byte) {}, nil)

	mgr.Disconnect(sid)
	require.Eventually(t, func() bool {
		proc.mu.Lock()
		defer proc.mu.Unlock()
		return proc.closeN == 1
	}, time.Second, 5*time.Millisecond)
}

func withShortGracePeriod(t *testing.T, d time.Duration) {
	orig := callbackSetupGracePeriod
	callbackSetupGracePeriod = d
	t.Cleanup(func() { callbackSetupGracePeriod = orig })
}

func TestGraceTimeoutReapsSessionWithoutCallbacks(t *testing.T) {
	withShortGracePeriod(t, 50*time.Millisecond)
	proc := newFakeProc()
	withFakeStartPTY(t, proc)
	mgr := NewManager()

	sid, err := mgr.Connect(ConnectConfig{AssetID: 4})
	require.NoError(t, err)
	// 不挂 SetCallbacks → 宽限期后看门狗应回收会话。

	require.Eventually(t, func() bool {
		_, ok := mgr.GetSession(sid)
		proc.mu.Lock()
		defer proc.mu.Unlock()
		return !ok && proc.closeN == 1
	}, time.Second, 5*time.Millisecond, "宽限期内未挂回调的会话应被回收")
}

func TestGraceTimeoutDoesNotReapSessionWithCallbacks(t *testing.T) {
	withShortGracePeriod(t, 50*time.Millisecond)
	proc := newFakeProc()
	withFakeStartPTY(t, proc)
	mgr := NewManager()

	sid, err := mgr.Connect(ConnectConfig{AssetID: 5})
	require.NoError(t, err)

	got := make(chan []byte, 4)
	mgr.SetCallbacks(sid, func(b []byte) { got <- b }, nil)

	// 等待远超宽限窗口,确认看门狗不会误杀已挂回调的会话。
	time.Sleep(150 * time.Millisecond)

	_, ok := mgr.GetSession(sid)
	require.True(t, ok, "已挂回调的会话不应被看门狗回收")

	proc.mu.Lock()
	closeN := proc.closeN
	proc.mu.Unlock()
	assert.Equal(t, 0, closeN, "已挂回调的会话 PTY 不应被关闭")

	// 数据仍能正常流转。
	proc.readCh <- []byte("alive")
	select {
	case b := <-got:
		assert.Equal(t, "alive", string(b))
	case <-time.After(time.Second):
		t.Fatal("宽限期后会话不再流转数据")
	}
}
