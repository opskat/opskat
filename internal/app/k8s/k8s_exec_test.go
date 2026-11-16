package k8s

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	k8spkg "github.com/opskat/opskat/internal/pkg/k8s"
	"github.com/opskat/opskat/internal/pkg/k8s/k8stest"
)

// eventRecorder 收集 binder 发出的事件。
type eventRecorder struct {
	mu     sync.Mutex
	events []emitted
}

func (r *eventRecorder) emit(event string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, emitted{event, payload})
}

func (r *eventRecorder) named(event string) []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []any
	for _, e := range r.events {
		if e.event == event {
			out = append(out, e.payload)
		}
	}
	return out
}

func (r *eventRecorder) output(sessionID string) string {
	var sb strings.Builder
	for _, p := range r.named("k8s:exec:data:" + sessionID) {
		raw, _ := base64.StdEncoding.DecodeString(p.(string))
		sb.Write(raw)
	}
	return sb.String()
}

func (r *eventRecorder) waitEnd(t *testing.T, sessionID string) K8sExecEnd {
	t.Helper()
	var ends []any
	require.Eventually(t, func() bool {
		ends = r.named("k8s:exec:end:" + sessionID)
		return len(ends) > 0
	}, 5*time.Second, 10*time.Millisecond, "no k8s:exec:end for %s", sessionID)
	require.Len(t, ends, 1)
	return ends[0].(K8sExecEnd)
}

// execTestBinder 返回一个经计数拨号器连到 srv 的 binder 与调用上下文（kubeconfig 指向不可解析的主机）。
func execTestBinder(t *testing.T, target string) (*K8s, *eventRecorder, *k8sCallContext) {
	rec := &eventRecorder{}
	k := &K8s{ctx: context.Background(), emit: rec.emit}
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, target)
	}
	c := &k8sCallContext{
		kubeconfig: k8stest.Kubeconfig("http://k8s.invalid:6443", nil, ""),
		opts:       []k8spkg.ClientOption{k8spkg.WithDial(dial)},
	}
	return k, rec, c
}

func execReq(pod string) K8sExecRequest {
	return K8sExecRequest{AssetID: 7, Namespace: "ns1", Pod: pod, Container: "app", Shell: "auto", Cols: 80, Rows: 24}
}

func observeLogs(t *testing.T) *observer.ObservedLogs {
	core, logs := observer.New(zap.DebugLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })
	return logs
}

func assertSessionFields(t *testing.T, entry observer.LoggedEntry, pod string) {
	t.Helper()
	fields := entry.ContextMap()
	assert.Equal(t, int64(7), fields["assetID"], entry.Message)
	assert.Equal(t, "ns1", fields["namespace"], entry.Message)
	assert.Equal(t, pod, fields["pod"], entry.Message)
	assert.Equal(t, "app", fields["container"], entry.Message)
}

func TestK8sExecSessionPassesInputAndResizeThenStops(t *testing.T) {
	logs := observeLogs(t)
	sizes := make(chan k8stest.TerminalSize, 8)
	srv := k8stest.NewExecServer(t, false, func(s *k8stest.ExecSession) int {
		go func() {
			for size := range s.Resizes {
				sizes <- size
			}
		}()
		return k8stest.EchoUntilExit(s)
	})
	k, rec, c := execTestBinder(t, srv.Host())

	id, err := k.startExec(c, execReq("web-0"))
	require.NoError(t, err)
	require.NotEmpty(t, id)

	require.NoError(t, k.WriteK8sExec(id, base64.StdEncoding.EncodeToString([]byte("secret-input\n"))))
	require.Eventually(t, func() bool { return strings.Contains(rec.output(id), "secret-input") }, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, k.ResizeK8sExec(id, 132, 43))
	var got []k8stest.TerminalSize
	require.Eventually(t, func() bool {
		select {
		case s := <-sizes:
			got = append(got, s)
		default:
		}
		return len(got) > 0 && got[len(got)-1] == k8stest.TerminalSize{Width: 132, Height: 43}
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, k8stest.TerminalSize{Width: 80, Height: 24}, got[0], "the opening size is sent first")

	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, []string{"/bin/sh"}, reqs[0].URL.Query()["command"], "'auto' runs /bin/sh for now")

	k.StopK8sExec(id)
	assert.Equal(t, K8sExecEnd{Reason: ExecEndClosed}, rec.waitEnd(t, id))
	assert.True(t, srv.WaitEnded(5*time.Second), "stopping releases the connection")
	assert.Error(t, k.WriteK8sExec(id, base64.StdEncoding.EncodeToString([]byte("x"))), "a stopped session is gone")

	start := logs.FilterMessage("k8s exec start").All()
	require.Len(t, start, 1)
	assertSessionFields(t, start[0], "web-0")
	end := logs.FilterMessage("k8s exec ended").All()
	require.Len(t, end, 1)
	assertSessionFields(t, end[0], "web-0")
	assert.Equal(t, ExecEndClosed, end[0].ContextMap()["reason"])
	for _, e := range logs.All() {
		for _, v := range e.ContextMap() {
			s, _ := v.(string)
			assert.NotContains(t, s, "secret-input", "terminal content must never be logged")
		}
	}
}

func TestK8sExecReportsCommandExit(t *testing.T) {
	for name, code := range map[string]int{"zero": 0, "non-zero": 2} {
		t.Run(name, func(t *testing.T) {
			srv := k8stest.NewExecServer(t, false, func(s *k8stest.ExecSession) int {
				k8stest.EchoUntilExit(s)
				return code
			})
			k, rec, c := execTestBinder(t, srv.Host())
			id, err := k.startExec(c, execReq("web-0"))
			require.NoError(t, err)
			require.NoError(t, k.WriteK8sExec(id, base64.StdEncoding.EncodeToString([]byte("exit\n"))))
			exitCode := code
			assert.Equal(t, K8sExecEnd{Reason: ExecEndExited, ExitCode: &exitCode}, rec.waitEnd(t, id))
		})
	}
}

func TestK8sExecCleanupEndsAllSessions(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, nil)
	k, rec, c := execTestBinder(t, srv.Host())
	a, err := k.startExec(c, execReq("web-0"))
	require.NoError(t, err)
	b, err := k.startExec(c, execReq("web-1"))
	require.NoError(t, err)
	require.NotEqual(t, a, b)

	k.Cleanup()
	for _, id := range []string{a, b} {
		assert.Equal(t, K8sExecEnd{Reason: ExecEndClosed}, rec.waitEnd(t, id))
		assert.Error(t, k.ResizeK8sExec(id, 100, 30))
	}
	assert.True(t, srv.WaitEnded(5*time.Second))
	assert.True(t, srv.WaitEnded(5*time.Second))
}

func TestK8sExecConnectFailureReturnsClusterReason(t *testing.T) {
	logs := observeLogs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403,` +
			`"message":"pods \"web-0\" is forbidden: cannot create resource \"pods/exec\""}`))
	}))
	t.Cleanup(srv.Close)
	k, rec, c := execTestBinder(t, srv.Listener.Addr().String())

	_, err := k.startExec(c, execReq("web-0"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `cannot create resource "pods/exec"`)
	assert.Empty(t, rec.named("k8s:exec:end:k8s-exec-1"), "a session that never started has no end event")

	failed := logs.FilterMessage("k8s exec failed").All()
	require.Len(t, failed, 1)
	assertSessionFields(t, failed[0], "web-0")
}

// 请求校验发生在加载资产之前：零值 binder 即可断言，非法请求不会触达集群。
func TestStartK8sExecRejectsInvalidRequests(t *testing.T) {
	k := &K8s{}
	cases := map[string]struct {
		mutate func(*K8sExecRequest)
		want   string
	}{
		"missing namespace":    {func(r *K8sExecRequest) { r.Namespace = "" }, "namespace is required"},
		"missing pod":          {func(r *K8sExecRequest) { r.Pod = "" }, "pod is required"},
		"pod escapes path":     {func(r *K8sExecRequest) { r.Pod = "../secrets" }, "pod"},
		"namespace with slash": {func(r *K8sExecRequest) { r.Namespace = "a/b" }, "namespace"},
		"blank shell":          {func(r *K8sExecRequest) { r.Shell = "  " }, "shell is required"},
		"zero size":            {func(r *K8sExecRequest) { r.Cols = 0 }, "terminal size"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			req := execReq("web-0")
			c.mutate(&req)
			_, err := k.StartK8sExec(req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestExecCommandSplitsCustomCommands(t *testing.T) {
	assert.Equal(t, []string{"/bin/bash"}, execCommand("/bin/bash"))
	assert.Equal(t, []string{"python3", "-i"}, execCommand("  python3   -i "))
}
