package k8s

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

func (r *eventRecorder) all() []emitted {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]emitted(nil), r.events...)
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
	// 读不到终止状态时的重读放快放短，免得「终止状态一直不出现」的用例各等满几秒。
	oldInterval, oldWindow := execEndPollInterval, execEndPollWindow
	execEndPollInterval, execEndPollWindow = 20*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { execEndPollInterval, execEndPollWindow = oldInterval, oldWindow })
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
	return K8sExecRequest{SessionID: "sess-" + pod, AssetID: 7, Namespace: "ns1", Pod: pod, Container: "app", Shell: "auto", Cols: 80, Rows: 24}
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
	assert.Equal(t, []string{"/bin/bash"}, reqs[0].URL.Query()["command"], "'auto' tries bash first")

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
			srv := podStatusServer(t, runningPod, func(s *k8stest.ExecSession) int {
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
	assert.Empty(t, rec.named("k8s:exec:end:sess-web-0"), "a session that never started has no end event")

	failed := logs.FilterMessage("k8s exec failed").All()
	require.Len(t, failed, 1)
	assertSessionFields(t, failed[0], "web-0")
}

// 校验被拒也是一次失败的会话开始，按日志规范记录（带资产 / 命名空间 / Pod / 容器）。
func TestStartK8sExecLogsRejectedRequest(t *testing.T) {
	logs := observeLogs(t)
	k := &K8s{ctx: context.Background()}
	req := execReq("../secrets")
	_, err := k.StartK8sExec(req)
	require.Error(t, err)
	failed := logs.FilterMessage("k8s exec failed").All()
	require.Len(t, failed, 1)
	assert.Equal(t, "../secrets", failed[0].ContextMap()["pod"])
	assert.Equal(t, int64(7), failed[0].ContextMap()["assetID"])
}

// 请求校验发生在加载资产之前：没有资产服务的 binder 即可断言，非法请求不会触达集群。
func TestStartK8sExecRejectsInvalidRequests(t *testing.T) {
	k := &K8s{ctx: context.Background()}
	cases := map[string]struct {
		mutate func(*K8sExecRequest)
		want   string
	}{
		"missing namespace":    {func(r *K8sExecRequest) { r.Namespace = "" }, "namespace is required"},
		"missing pod":          {func(r *K8sExecRequest) { r.Pod = "" }, "pod is required"},
		"pod escapes path":     {func(r *K8sExecRequest) { r.Pod = "../secrets" }, "pod"},
		"namespace with slash": {func(r *K8sExecRequest) { r.Namespace = "a/b" }, "namespace"},
		"blank shell":          {func(r *K8sExecRequest) { r.Shell = "  " }, "shell is required"},
		"missing session id":   {func(r *K8sExecRequest) { r.SessionID = "" }, "session id is required"},
		"unterminated quote":   {func(r *K8sExecRequest) { r.Shell = `sh -c "ls` }, "unterminated"},
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

func TestSplitCommandFollowsShellWordRulesWithoutInterpretation(t *testing.T) {
	cases := map[string][]string{
		"  python3   -i ":           {"python3", "-i"},
		`python3 -c "print('a b')"`: {"python3", "-c", "print('a b')"},
		`sh -c 'echo "x y"'`:        {"sh", "-c", `echo "x y"`},
		`ls a\ b`:                   {"ls", "a b"},
		"echo $HOME && rm *":        {"echo", "$HOME", "&&", "rm", "*"},
		`echo "" end`:               {"echo", "", "end"},
		`echo "a\"b"`:               {"echo", `a"b`},
	}
	for in, want := range cases {
		got, err := splitCommand(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	_, err := splitCommand(`echo 'oops`)
	assert.Error(t, err)
}

const greeting = "greeting-before-any-input\r\n"

func startedOf(t *testing.T, rec *eventRecorder, id string) K8sExecStarted {
	t.Helper()
	var got []any
	require.Eventually(t, func() bool {
		got = rec.named("k8s:exec:started:" + id)
		return len(got) > 0
	}, 5*time.Second, 10*time.Millisecond, "no k8s:exec:started for %s", id)
	require.Len(t, got, 1)
	return got[0].(K8sExecStarted)
}

func commandsOf(srv *k8stest.ExecServer) [][]string {
	reqs := srv.Requests()
	out := make([][]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.URL.Query()["command"])
	}
	return out
}

// 没有 bash 的容器：运行时要么以 126/127 退出，要么在 error 流上报「executable file not found」。
func TestK8sExecAutoFallsBackToShWhenBashIsMissing(t *testing.T) {
	missing := map[string]func(s *k8stest.ExecSession) int{
		"exit 127": func(*k8stest.ExecSession) int { return 127 },
		"exit 126": func(*k8stest.ExecSession) int { return 126 },
		"runtime error": func(s *k8stest.ExecSession) int {
			s.Failure = `OCI runtime exec failed: exec failed: unable to start container process: exec: "/bin/bash": executable file not found in $PATH: unknown`
			return 0
		},
	}
	for name, bashMissing := range missing {
		t.Run(name, func(t *testing.T) {
			srv := k8stest.NewExecServer(t, false, func(s *k8stest.ExecSession) int {
				if s.Command()[0] == "/bin/bash" {
					return bashMissing(s)
				}
				_, _ = s.Stdout.Write([]byte(greeting))
				return k8stest.EchoUntilExit(s)
			})
			k, rec, c := execTestBinder(t, srv.Host())
			id, err := k.startExec(c, execReq("web-0"))
			require.NoError(t, err)

			assert.Equal(t, K8sExecStarted{Shell: "/bin/sh"}, startedOf(t, rec, id))
			require.Eventually(t, func() bool { return strings.Contains(rec.output(id), "greeting-before-any-input") }, 5*time.Second, 10*time.Millisecond)
			assert.Equal(t, [][]string{{"/bin/bash"}, {"/bin/sh"}}, commandsOf(srv))
			assert.Empty(t, rec.named("k8s:exec:end:"+id), "the fallback is invisible: the session stays alive")

			require.NoError(t, k.WriteK8sExec(id, base64.StdEncoding.EncodeToString([]byte("hi\n"))), "input reaches the fallback shell")
			require.Eventually(t, func() bool { return strings.Contains(rec.output(id), "hi") }, 5*time.Second, 10*time.Millisecond)
			k.StopK8sExec(id)
			rec.waitEnd(t, id)
		})
	}
}

func TestK8sExecAutoUsesBashWhenPresent(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, func(s *k8stest.ExecSession) int {
		_, _ = s.Stdout.Write([]byte(greeting))
		return k8stest.EchoUntilExit(s)
	})
	k, rec, c := execTestBinder(t, srv.Host())
	id, err := k.startExec(c, execReq("web-0"))
	require.NoError(t, err)
	assert.Equal(t, K8sExecStarted{Shell: "/bin/bash"}, startedOf(t, rec, id))
	assert.Equal(t, [][]string{{"/bin/bash"}}, commandsOf(srv))
	k.StopK8sExec(id)
	rec.waitEnd(t, id)
}

func TestK8sExecDoesNotFallBackAfterOutputOrForExplicitShell(t *testing.T) {
	t.Run("bash that already printed output then exits 127", func(t *testing.T) {
		srv := podStatusServer(t, runningPod, func(s *k8stest.ExecSession) int {
			_, _ = s.Stdout.Write([]byte("hello\r\n"))
			return 127
		})
		k, rec, c := execTestBinder(t, srv.Host())
		id, err := k.startExec(c, execReq("web-0"))
		require.NoError(t, err)
		code := 127
		assert.Equal(t, K8sExecEnd{Reason: ExecEndExited, ExitCode: &code}, rec.waitEnd(t, id))
		assert.Len(t, srv.Requests(), 1)
	})
	t.Run("explicit /bin/bash that is missing", func(t *testing.T) {
		srv := podStatusServer(t, runningPod, func(*k8stest.ExecSession) int { return 127 })
		k, rec, c := execTestBinder(t, srv.Host())
		req := execReq("web-0")
		req.Shell = "/bin/bash"
		id, err := k.startExec(c, req)
		require.NoError(t, err)
		code := 127
		assert.Equal(t, K8sExecEnd{Reason: ExecEndExited, ExitCode: &code}, rec.waitEnd(t, id))
		assert.Equal(t, [][]string{{"/bin/bash"}}, commandsOf(srv))
	})
	t.Run("sh missing too", func(t *testing.T) {
		srv := podStatusServer(t, runningPod, func(*k8stest.ExecSession) int { return 127 })
		k, rec, c := execTestBinder(t, srv.Host())
		id, err := k.startExec(c, execReq("web-0"))
		require.NoError(t, err)
		code := 127
		assert.Equal(t, K8sExecEnd{Reason: ExecEndExited, ExitCode: &code}, rec.waitEnd(t, id))
		assert.Equal(t, [][]string{{"/bin/bash"}, {"/bin/sh"}}, commandsOf(srv))
	})
}

func TestK8sExecCustomCommandRunsAsArgvAndReportsItself(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, nil)
	k, rec, c := execTestBinder(t, srv.Host())
	req := execReq("web-0")
	req.Shell = `python3 -c "print('a b')" ; $HOME`
	id, err := k.startExec(c, req)
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"python3", "-c", "print('a b')", ";", "$HOME"}}, commandsOf(srv))
	assert.Equal(t, K8sExecStarted{Shell: req.Shell}, startedOf(t, rec, id))
	k.StopK8sExec(id)
	rec.waitEnd(t, id)
}

func TestK8sExecMissingCustomCommandIsPassedThrough(t *testing.T) {
	const reason = `OCI runtime exec failed: exec: "nope": executable file not found in $PATH: unknown`
	srv := k8stest.NewExecServer(t, false, func(s *k8stest.ExecSession) int {
		s.Failure = reason
		return 0
	})
	k, rec, c := execTestBinder(t, srv.Host())
	req := execReq("web-0")
	req.Shell = "nope"
	id, err := k.startExec(c, req)
	require.NoError(t, err)
	end := rec.waitEnd(t, id)
	assert.Equal(t, ExecEndFailed, end.Reason, "the cluster refused the command; the connection was fine")
	assert.Contains(t, end.Error, reason)
	assert.Len(t, srv.Requests(), 1, "an explicit command never falls back")
}

// 会话 ID 由前端生成并先订阅：首屏输出在 StartK8sExec 返回前就发出也不会丢。
func TestK8sExecEmitsUnderCallerSessionIDFromTheFirstByte(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, func(s *k8stest.ExecSession) int {
		_, _ = s.Stdout.Write([]byte(greeting))
		return k8stest.EchoUntilExit(s)
	})
	k, rec, c := execTestBinder(t, srv.Host())
	req := execReq("web-0")
	req.SessionID = "caller-chosen-id"
	id, err := k.startExec(c, req)
	require.NoError(t, err)
	assert.Equal(t, "caller-chosen-id", id)
	require.Eventually(t, func() bool { return rec.output(id) == greeting }, 5*time.Second, 10*time.Millisecond)
	for _, e := range rec.all() {
		assert.Contains(t, e.event, "caller-chosen-id", "every event of the session uses the caller's id")
	}
	k.StopK8sExec(id)
	rec.waitEnd(t, id)
}

const runningPod = `[{"name":"app","state":{"running":{}}}]`

// finishedNow 在 podStatusServer 响应时才换成当前时间：终止发生在会话开始之后，夹具里不能提前取时间，
// 否则跨过整秒边界时「终止」会早于会话开始。
const finishedNow = "{{now}}"

func podStatusServer(t *testing.T, statuses string, run k8stest.ExecFunc) *k8stest.ExecServer {
	srv := k8stest.NewExecServer(t, false, run)
	srv.Other = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if statuses == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404,"message":"pods \"web-0\" not found"}`))
			return
		}
		statuses := strings.ReplaceAll(statuses, finishedNow, time.Now().UTC().Format(time.RFC3339))
		_, _ = w.Write([]byte(`{"kind":"Pod","apiVersion":"v1","metadata":{"name":"web-0","namespace":"ns1"},"status":{"phase":"Running","containerStatuses":` + statuses + `}}`))
	})
	return srv
}

func TestK8sExecEndEnrichedWithContainerTerminationReason(t *testing.T) {
	now := finishedNow
	long := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	cases := map[string]struct {
		statuses string
		want     K8sExecEnd
	}{
		"oom killed now": {
			`[{"name":"app","state":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"` + now + `"}}}]`,
			K8sExecEnd{Reason: ExecEndExited, ContainerReason: "OOMKilled"},
		},
		"oom killed then restarted": {
			`[{"name":"app","state":{"running":{}},"lastState":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"` + now + `"}}}]`,
			K8sExecEnd{Reason: ExecEndExited, ContainerReason: "OOMKilled"},
		},
		"stale earlier termination is ignored": {
			`[{"name":"app","state":{"running":{}},"lastState":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"` + long + `"}}}]`,
			K8sExecEnd{Reason: ExecEndExited},
		},
		"another container's status is ignored": {
			`[{"name":"side","state":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"` + now + `"}}}]`,
			K8sExecEnd{Reason: ExecEndExited},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := podStatusServer(t, tc.statuses, func(*k8stest.ExecSession) int { return 137 })
			k, rec, c := execTestBinder(t, srv.Host())
			id, err := k.startExec(c, execReq("web-0"))
			require.NoError(t, err)
			code := 137
			want := tc.want
			want.ExitCode = &code
			assert.Equal(t, want, rec.waitEnd(t, id))
		})
	}
}

// kubelet 写入容器终止状态晚于 exec 流结束：首次读 Pod 还没有终止信息时要再读几次。
func TestK8sExecEndWaitsForTerminationThatLandsLate(t *testing.T) {
	var reads atomic.Int32
	srv := k8stest.NewExecServer(t, false, func(*k8stest.ExecSession) int { return 137 })
	srv.Other = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		statuses := runningPod
		if reads.Add(1) >= 2 {
			statuses = `[{"name":"app","state":{"terminated":{"reason":"OOMKilled","exitCode":137,"finishedAt":"` +
				time.Now().UTC().Format(time.RFC3339) + `"}}}]`
		}
		_, _ = w.Write([]byte(`{"kind":"Pod","apiVersion":"v1","metadata":{"name":"web-0","namespace":"ns1"},"status":{"phase":"Running","containerStatuses":` + statuses + `}}`))
	})
	k, rec, c := execTestBinder(t, srv.Host())
	id, err := k.startExec(c, execReq("web-0"))
	require.NoError(t, err)
	code := 137
	assert.Equal(t, K8sExecEnd{Reason: ExecEndExited, ExitCode: &code, ContainerReason: "OOMKilled"}, rec.waitEnd(t, id))
	assert.GreaterOrEqual(t, reads.Load(), int32(2))
}

// 用户在 shell 里 `exit 1` 这类普通的非 0 退出，容器没有被杀：只读一次 Pod，不为等终止状态拖延结束提示。
func TestK8sExecEndReadsPodOnceForPlainNonZeroExit(t *testing.T) {
	var reads atomic.Int32
	srv := k8stest.NewExecServer(t, false, func(*k8stest.ExecSession) int { return 1 })
	srv.Other = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Pod","apiVersion":"v1","metadata":{"name":"web-0","namespace":"ns1"},"status":{"phase":"Running","containerStatuses":` + runningPod + `}}`))
	})
	k, rec, c := execTestBinder(t, srv.Host())
	id, err := k.startExec(c, execReq("web-0"))
	require.NoError(t, err)
	code := 1
	assert.Equal(t, K8sExecEnd{Reason: ExecEndExited, ExitCode: &code}, rec.waitEnd(t, id))
	assert.Equal(t, int32(1), reads.Load())
}

// 正常删除时 Pod 先进入 Terminating（带 deletionTimestamp），exec 流结束时它还在：这也算 Pod 被删除。
func TestK8sExecEndReportsPodBeingDeleted(t *testing.T) {
	srv := k8stest.NewExecServer(t, false, func(*k8stest.ExecSession) int { return 137 })
	srv.Other = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Pod","apiVersion":"v1","metadata":{"name":"web-0","namespace":"ns1","deletionTimestamp":"` +
			time.Now().UTC().Format(time.RFC3339) + `"},"status":{"phase":"Running","containerStatuses":` + runningPod + `}}`))
	})
	k, rec, c := execTestBinder(t, srv.Host())
	id, err := k.startExec(c, execReq("web-0"))
	require.NoError(t, err)
	code := 137
	assert.Equal(t, K8sExecEnd{Reason: ExecEndExited, ExitCode: &code, PodGone: true}, rec.waitEnd(t, id))
}

// 网络断开：关掉底层连接，会话以「连接已断开」结束；Pod 已被删除时一并告知。
func TestK8sExecEndOnConnectionDrop(t *testing.T) {
	for name, statuses := range map[string]string{
		"pod still there": `[{"name":"app","state":{"running":{}}}]`,
		"pod deleted":     "",
	} {
		t.Run(name, func(t *testing.T) {
			srv := podStatusServer(t, statuses, func(s *k8stest.ExecSession) int {
				_, _ = s.Stdout.Write([]byte(greeting))
				select {} // 一直运行，直到连接被掐断
			})
			k, rec, c := execTestBinder(t, srv.Host())
			var mu sync.Mutex
			var conns []net.Conn
			c.opts = []k8spkg.ClientOption{k8spkg.WithDial(func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				conn, err := d.DialContext(ctx, network, srv.Host())
				if err == nil {
					mu.Lock()
					conns = append(conns, conn)
					mu.Unlock()
				}
				return conn, err
			})}
			id, err := k.startExec(c, execReq("web-0"))
			require.NoError(t, err)
			require.Eventually(t, func() bool { return strings.Contains(rec.output(id), "greeting") }, 5*time.Second, 10*time.Millisecond)
			mu.Lock()
			require.NotEmpty(t, conns)
			_ = conns[0].Close()
			mu.Unlock()

			end := rec.waitEnd(t, id)
			assert.Equal(t, ExecEndError, end.Reason)
			assert.NotEmpty(t, end.Error)
			assert.Nil(t, end.ExitCode)
			assert.Equal(t, statuses == "", end.PodGone)
		})
	}
}

// 状态里的时间只到秒：与会话开始同一秒内的终止属于本次会话，更早一秒的不算。
func TestContainerTerminationAtSecondPrecision(t *testing.T) {
	since := time.Date(2026, 11, 16, 10, 0, 1, 900_000_000, time.UTC)
	podAt := func(finished time.Time) corev1.Pod {
		return corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled", FinishedAt: metav1.NewTime(finished.Truncate(time.Second)),
			}},
		}}}}
	}
	assert.NotNil(t, containerTermination(podAt(since.Add(-800*time.Millisecond)), "app", since), "same second as session start")
	assert.Nil(t, containerTermination(podAt(since.Add(-time.Second)), "app", since), "previous second")
}
