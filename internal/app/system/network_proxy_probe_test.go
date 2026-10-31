package system

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/pkg/netproxy/netproxytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeOrigin 是只有录制代理认得的目标主机名（.invalid 直连时必定解析失败）。
const probeOrigin = "origin.invalid"

func setupNetworkProxyProbeTest(t *testing.T) *System {
	t.Helper()
	s := setupNetworkProxyTest(t, &bootstrap.AppConfig{})
	s.ctx = t.Context()
	return s
}

func TestTestNetworkProxyUsesUnsavedSettingsWithSavedPasswordAndNeverSaves(t *testing.T) {
	s := setupNetworkProxyProbeTest(t)
	saved := customInput() // socks5 127.0.0.1:7890, u / secret
	require.NoError(t, s.SaveNetworkProxySettings(saved))
	storedBefore := *bootstrap.GetConfig().NetworkProxy
	appliedBefore := netproxy.Get()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(origin.Close)
	proxy := netproxytest.NewRecordingProxy(t)
	proxy.RequireAuth("u", "secret")
	proxy.Forward(probeOrigin, origin.Listener.Addr().String())
	in := NetworkProxyInput{Mode: "custom", Type: "http", Host: "127.0.0.1", Port: proxy.Port(), Username: "u", Password: "", NoProxy: ""}

	got, err := s.TestNetworkProxy("probe-saved-pw", in, "http://"+probeOrigin+"/")

	require.NoError(t, err)
	assert.Equal(t, &NetworkProxyTestResult{
		OK: true, Via: "http://127.0.0.1:" + strconv.Itoa(proxy.Port()), Host: probeOrigin, Status: http.StatusUnauthorized,
		ElapsedMs: got.ElapsedMs,
	}, got)
	reqs := proxy.Requests()
	require.Len(t, reqs, 1, "the unsaved proxy settings were used")
	assert.Equal(t, "secret", reqs[0].Password, "empty password falls back to the saved one")
	assert.Equal(t, storedBefore, *bootstrap.GetConfig().NetworkProxy, "testing must not save")
	assert.Equal(t, appliedBefore, netproxy.Get(), "testing must not apply")
}

func TestTestNetworkProxyResultJSON(t *testing.T) {
	s := setupNetworkProxyProbeTest(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	in := NetworkProxyInput{Mode: "custom", Type: "socks5", Host: "127.0.0.1", Port: port}

	got, err := s.TestNetworkProxy("", in, "https://"+probeOrigin+"/")
	require.NoError(t, err)

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.ElementsMatch(t, []string{"ok", "via", "host", "status", "elapsedMs", "reason", "detail"}, keys(m))
	assert.Equal(t, false, m["ok"])
	assert.Equal(t, "proxy_unreachable", m["reason"])
	assert.Equal(t, "socks5://127.0.0.1:"+strconv.Itoa(port), m["via"])
	assert.Equal(t, probeOrigin, m["host"])
	assert.NotEmpty(t, m["detail"])
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestTestNetworkProxyRejectsInvalidInputWithoutProbing(t *testing.T) {
	proxy := netproxytest.NewRecordingProxy(t)
	valid := NetworkProxyInput{Mode: "custom", Type: "http", Host: "127.0.0.1", Port: proxy.Port()}
	badPort := valid
	badPort.Port = 70000
	cases := []struct {
		name   string
		in     NetworkProxyInput
		target string
		zh, en string
	}{
		{"invalid settings", badPort, "http://" + probeOrigin + "/", "代理端口无效：须为 1–65535 的整数", "Invalid proxy port: must be an integer between 1 and 65535"},
		{"empty target", valid, "", networkProxyTargetZh, networkProxyTargetEn},
		{"non-http scheme", valid, "ftp://" + probeOrigin + "/", networkProxyTargetZh, networkProxyTargetEn},
		{"no scheme", valid, probeOrigin, networkProxyTargetZh, networkProxyTargetEn},
		{"no host", valid, "http:///path", networkProxyTargetZh, networkProxyTargetEn},
		{"unparsable", valid, "http://[::1", networkProxyTargetZh, networkProxyTargetEn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := setupNetworkProxyProbeTest(t)
			_, err := s.TestNetworkProxy("", tc.in, tc.target)
			require.Error(t, err)
			assert.Equal(t, tc.zh, err.Error())
			s.SetLanguage("en")
			_, err = s.TestNetworkProxy("", tc.in, tc.target)
			require.Error(t, err)
			assert.Equal(t, tc.en, err.Error())
		})
	}
	assert.Empty(t, proxy.Requests(), "rejected input must not be probed")
}

const (
	networkProxyTargetZh = "测试地址须为 http:// 或 https:// 开头的完整 URL"
	networkProxyTargetEn = "The test URL must be a full URL starting with http:// or https://"
)

func TestTestNetworkProxyCancellable(t *testing.T) {
	s := setupNetworkProxyProbeTest(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	in := NetworkProxyInput{Mode: "direct", Type: "http"}

	type outcome struct {
		res *NetworkProxyTestResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := s.TestNetworkProxy("probe-cancel-me", in, "http://"+ln.Addr().String()+"/")
		done <- outcome{res, err}
	}()
	select {
	case c := <-accepted:
		t.Cleanup(func() { _ = c.Close() })
	case <-time.After(5 * time.Second):
		t.Fatal("probe never reached the server")
	}
	s.CancelTest("probe-cancel-me")

	select {
	case o := <-done:
		assert.Nil(t, o.res)
		assert.True(t, errors.Is(o.err, context.Canceled), "err = %v", o.err)
	case <-time.After(5 * time.Second):
		t.Fatal("CancelTest did not stop the probe")
	}
}
