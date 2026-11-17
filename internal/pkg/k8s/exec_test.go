package k8s

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/k8s/k8stest"
)

// countingDial 把任意目标地址转到 target，并记录拨号目标与连接关闭次数。
type countingDial struct {
	target string
	mu     sync.Mutex
	dialed []string
	closed atomic.Int32
}

func (d *countingDial) dial(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.dialed = append(d.dialed, address)
	d.mu.Unlock()
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, network, d.target)
	if err != nil {
		return nil, err
	}
	return &closeCountingConn{Conn: conn, closed: &d.closed}, nil
}

func (d *countingDial) addresses() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dialed...)
}

type closeCountingConn struct {
	net.Conn
	closed *atomic.Int32
	once   sync.Once
}

func (c *closeCountingConn) Close() error {
	c.once.Do(func() { c.closed.Add(1) })
	return c.Conn.Close()
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// 不可解析的 API 地址：只有经注入的拨号器才能连到假端点。
const unreachableAPI = "https://k8s.invalid:6443"

func TestExecRunsOverSPDYThroughCustomDial(t *testing.T) {
	sizes := make(chan k8stest.TerminalSize, 1)
	srv := k8stest.NewExecServer(t, true, func(s *k8stest.ExecSession) int {
		select {
		case size := <-s.Resizes:
			sizes <- size
		case <-time.After(5 * time.Second):
		}
		return k8stest.EchoUntilExit(s)
	})
	d := &countingDial{target: srv.Host()}
	stdinR, stdinW := io.Pipe()
	var stdout syncBuffer
	resize := make(chan TerminalSize, 1)
	resize <- TerminalSize{Width: 120, Height: 40}
	var connected atomic.Bool

	go func() {
		_, _ = stdinW.Write([]byte("hello\n"))
		_, _ = stdinW.Write([]byte("exit\n"))
	}()
	err := Exec(context.Background(), k8stest.Kubeconfig(unreachableAPI, srv.CAPEM(), "example.com"), ExecRequest{
		Namespace: "ns1", Pod: "web-0", Container: "app", Command: []string{"/bin/sh"},
		Stdin: stdinR, Stdout: &stdout, Sizes: resize,
		OnConnected: func() { connected.Store(true) },
	}, WithDial(d.dial))
	require.NoError(t, err)

	assert.True(t, connected.Load(), "OnConnected runs once the upgrade succeeds")
	assert.Contains(t, d.addresses(), "k8s.invalid:6443", "the upgrade connection must be dialed through the asset dial")
	assert.Contains(t, stdout.String(), "hello")
	select {
	case got := <-sizes:
		assert.Equal(t, k8stest.TerminalSize{Width: 120, Height: 40}, got)
	default:
		t.Fatal("terminal size never reached the container")
	}

	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	r := reqs[0]
	assert.Equal(t, http.MethodPost, r.Method)
	assert.Equal(t, "/api/v1/namespaces/ns1/pods/web-0/exec", r.URL.Path)
	q := r.URL.Query()
	assert.Equal(t, "app", q.Get("container"))
	assert.Equal(t, []string{"/bin/sh"}, q["command"])
	assert.Equal(t, "true", q.Get("stdin"))
	assert.Equal(t, "true", q.Get("stdout"))
	assert.Equal(t, "true", q.Get("tty"))
	assert.NotEqual(t, "true", q.Get("stderr"), "a TTY session merges stderr into stdout")
	assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"), "kubeconfig credentials are sent on the upgrade request")
}

func TestExecPassesEveryCommandWord(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, func(*k8stest.ExecSession) int { return 0 })
	d := &countingDial{target: srv.Host()}
	err := Exec(context.Background(), k8stest.Kubeconfig("http://k8s.invalid:6443", nil, ""), ExecRequest{
		Namespace: "ns1", Pod: "web-0", Container: "app", Command: []string{"python3", "-i"},
		Stdin: bytes.NewReader(nil), Stdout: io.Discard, Sizes: make(chan TerminalSize),
	}, WithDial(d.dial))
	require.NoError(t, err)
	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, []string{"python3", "-i"}, reqs[0].URL.Query()["command"])
}

// 没配 CA 时按系统根证书校验：自签证书必须被拒绝，而不是退化成跳过校验。
func TestExecVerifiesServerCertificateWithoutCA(t *testing.T) {
	srv := k8stest.NewExecServer(t, true, nil)
	d := &countingDial{target: srv.Host()}
	var connected atomic.Bool
	err := Exec(context.Background(), k8stest.Kubeconfig(unreachableAPI, nil, ""), ExecRequest{
		Namespace: "ns1", Pod: "web-0", Container: "app", Command: []string{"/bin/sh"},
		Stdin: bytes.NewReader(nil), Stdout: io.Discard, Sizes: make(chan TerminalSize),
		OnConnected: func() { connected.Store(true) },
	}, WithDial(d.dial))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certificate")
	assert.False(t, connected.Load())
	assert.Empty(t, srv.Requests())
}

func TestExecCancelReleasesConnection(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, nil)
	d := &countingDial{target: srv.Host()}
	stdinR, stdinW := io.Pipe()
	defer func() { _ = stdinW.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connected := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- Exec(ctx, k8stest.Kubeconfig("http://k8s.invalid:6443", nil, ""), ExecRequest{
			Namespace: "ns1", Pod: "web-0", Container: "app", Command: []string{"/bin/sh"},
			Stdin: stdinR, Stdout: io.Discard, Sizes: make(chan TerminalSize),
			OnConnected: func() { close(connected) },
		}, WithDial(d.dial))
	}()
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("exec never connected")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the session")
	}
	assert.True(t, srv.WaitEnded(5*time.Second), "the server side sees the connection go away")
	assert.Eventually(t, func() bool { return d.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond,
		"the dialed connection is closed")
}

func TestExecReportsExitCode(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, func(*k8stest.ExecSession) int { return 3 })
	d := &countingDial{target: srv.Host()}
	err := Exec(context.Background(), k8stest.Kubeconfig("http://k8s.invalid:6443", nil, ""), ExecRequest{
		Namespace: "ns1", Pod: "web-0", Container: "app", Command: []string{"/bin/sh"},
		Stdin: bytes.NewReader(nil), Stdout: io.Discard, Sizes: make(chan TerminalSize),
	}, WithDial(d.dial))
	code, ok := ExitCode(err)
	require.True(t, ok, "err = %v", err)
	assert.Equal(t, 3, code)

	_, ok = ExitCode(context.Canceled)
	assert.False(t, ok)
}

func TestExecReturnsClusterReasonWhenUpgradeIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `pods "web-0" is forbidden: cannot create resource "pods/exec"`)
	}))
	t.Cleanup(srv.Close)
	d := &countingDial{target: srv.Listener.Addr().String()}
	var connected atomic.Bool
	err := Exec(context.Background(), k8stest.Kubeconfig("http://k8s.invalid:6443", nil, ""), ExecRequest{
		Namespace: "ns1", Pod: "web-0", Container: "app", Command: []string{"/bin/sh"},
		Stdin: bytes.NewReader(nil), Stdout: io.Discard, Sizes: make(chan TerminalSize),
		OnConnected: func() { connected.Store(true) },
	}, WithDial(d.dial))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `cannot create resource "pods/exec"`)
	assert.False(t, connected.Load())
}

// 会话命令经包装层启动：用户的 argv 作为独立参数交给包装脚本，从不拼进脚本文本（不被 shell 再解释一遍）。
func TestShellCommandKeepsUserArgvAsSeparateArguments(t *testing.T) {
	argv := []string{"python3", "-c", "print('a b')", ";", "$HOME"}
	cmd := ShellCommand("TAG1", argv)
	require.Greater(t, len(cmd), len(argv)+3)
	assert.Equal(t, []string{"/bin/sh", "-c"}, cmd[:2])
	assert.Equal(t, append([]string{"TAG1"}, argv...), cmd[len(cmd)-len(argv)-1:])
	for _, word := range []string{"TAG1", "print('a b')", "$HOME"} {
		assert.NotContains(t, cmd[2], word, "the script never embeds caller-supplied words")
	}
	assert.NotEqual(t, cmd[:3], EndShellCommand("TAG1")[:3], "ending a shell runs a different script")
}

// 结束 shell 是对同一容器的一次非交互 exec（不带 stdin、不开 TTY，不在用户终端里留下任何东西）。
func TestEndShellRunsNonInteractiveExecInTheContainer(t *testing.T) {
	srv := k8stest.NewExecServer(t, true, nil)
	var ran atomic.Bool
	srv.RunNonTTY = func(*k8stest.ExecSession) int { ran.Store(true); return 0 }
	d := &countingDial{target: srv.Host()}

	err := EndShell(context.Background(), k8stest.Kubeconfig(unreachableAPI, srv.CAPEM(), "example.com"),
		ExecTarget{Namespace: "ns1", Pod: "web-0", Container: "app"}, "TAG1", WithDial(d.dial))
	require.NoError(t, err)
	assert.True(t, ran.Load())
	assert.Contains(t, d.addresses(), "k8s.invalid:6443", "the cleanup goes through the asset dial too")

	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	r := reqs[0]
	assert.Equal(t, "/api/v1/namespaces/ns1/pods/web-0/exec", r.URL.Path)
	q := r.URL.Query()
	assert.Equal(t, "app", q.Get("container"))
	assert.Equal(t, EndShellCommand("TAG1"), q["command"])
	assert.Equal(t, "TAG1", q["command"][len(q["command"])-1])
	assert.NotEqual(t, "true", q.Get("tty"))
	assert.NotEqual(t, "true", q.Get("stdin"))
	assert.Equal(t, "true", q.Get("stdout"))
}

// 清理脚本说明做不到的原因（例如找不到记录的进程号）：随错误带回，供调用方记日志。
func TestEndShellFailureCarriesTheScriptsReason(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, nil)
	srv.RunNonTTY = func(s *k8stest.ExecSession) int {
		_, _ = s.Stdout.Write([]byte("shell pid file not found\n"))
		return 3
	}
	d := &countingDial{target: srv.Host()}
	err := EndShell(context.Background(), k8stest.Kubeconfig("http://k8s.invalid:6443", nil, ""),
		ExecTarget{Namespace: "ns1", Pod: "web-0", Container: "app"}, "TAG1", WithDial(d.dial))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell pid file not found")
	code, ok := ExitCode(err)
	require.True(t, ok, "err = %v", err)
	assert.Equal(t, 3, code)
}
