package netproxy_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/pkg/netproxy/netproxytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeHost 是只有录制代理认得的目标主机名；.invalid 保证直连时解析失败（RFC 2606）。
const probeHost = "origin.invalid"

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u
}

// closedPort 返回一个刚关闭、无人监听的本机端口。
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

// hangingListener 接受连接但从不应答，用于超时与取消；返回地址与「已有连接到达」信号。
func hangingListener(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	accepted := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var conns []net.Conn
		defer func() {
			for _, c := range conns {
				_ = c.Close()
			}
		}()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
			select {
			case accepted <- struct{}{}:
			default:
			}
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return ln.Addr().String(), accepted
}

func directSettings() netproxy.Settings {
	s := netproxy.Default()
	s.Mode = netproxy.ModeDirect
	return s
}

func assertFailure(t *testing.T, got netproxy.ProbeResult, want netproxy.ProbeReason) {
	t.Helper()
	assert.False(t, got.OK, "result = %+v", got)
	assert.Equal(t, want, got.Reason, "result = %+v", got)
	assert.NotEmpty(t, got.Detail, "failure must carry the raw error text")
}

func TestProbeAnyHTTPResponseIsSuccessViaProxy(t *testing.T) {
	for _, scheme := range []netproxy.Scheme{netproxy.SchemeHTTP, netproxy.SchemeSOCKS5} {
		t.Run(string(scheme), func(t *testing.T) {
			var sawAuth atomic.Bool
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawAuth.Store(r.Header.Get("Authorization") != "")
				w.WriteHeader(http.StatusNotFound)
			}))
			t.Cleanup(origin.Close)
			proxy := netproxytest.NewRecordingProxy(t)
			proxy.RequireAuth("pu", "pp")
			proxy.Forward(probeHost, origin.Listener.Addr().String())

			got := netproxy.Probe(t.Context(), proxy.Settings(scheme), mustURL(t, "http://tu:tp@"+probeHost+"/x"))

			assert.True(t, got.OK, "result = %+v", got)
			assert.Equal(t, http.StatusNotFound, got.Status)
			assert.Equal(t, string(scheme)+"://127.0.0.1:"+strconv.Itoa(proxy.Port()), got.Via, "via names the proxy without credentials")
			assert.Equal(t, probeHost, got.Host)
			assert.Positive(t, got.Elapsed)
			assert.Empty(t, got.Reason)
			assert.False(t, sawAuth.Load(), "the probe GET must not carry the URL's credentials")
			reqs := proxy.Requests()
			require.NotEmpty(t, reqs)
			assert.Equal(t, net.JoinHostPort(probeHost, "80"), reqs[len(reqs)-1].Target)
		})
	}
}

func TestProbeDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Bool
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			followed.Store(true)
			return
		}
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	t.Cleanup(origin.Close)

	got := netproxy.Probe(t.Context(), directSettings(), mustURL(t, origin.URL+"/start"))

	assert.True(t, got.OK, "result = %+v", got)
	assert.Equal(t, http.StatusFound, got.Status)
	assert.Equal(t, netproxy.ViaDirect, got.Via)
	assert.False(t, followed.Load(), "redirect was followed")
}

func TestProbeDirectModeIgnoresProxyFields(t *testing.T) {
	proxy := netproxytest.NewRecordingProxy(t)
	s := proxy.Settings(netproxy.SchemeHTTP)
	s.Mode = netproxy.ModeDirect

	got := netproxy.Probe(t.Context(), s, mustURL(t, "http://"+probeHost+"/"))

	assertFailure(t, got, netproxy.ReasonDNS)
	assert.Equal(t, netproxy.ViaDirect, got.Via)
	assert.Equal(t, probeHost, got.Host)
	assert.Empty(t, proxy.Requests())
}

func TestProbeEnvModeReportsEnvironmentProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(origin.Close)
	proxy := netproxytest.NewRecordingProxy(t)
	proxy.Forward(probeHost, origin.Listener.Addr().String())
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:"+strconv.Itoa(proxy.Port()))

	got := netproxy.Probe(t.Context(), netproxy.Default(), mustURL(t, "http://"+probeHost+"/"))

	assert.True(t, got.OK, "result = %+v", got)
	assert.Equal(t, http.StatusOK, got.Status)
	assert.Equal(t, "http://127.0.0.1:"+strconv.Itoa(proxy.Port()), got.Via)
}

func TestProbeProxyUnreachable(t *testing.T) {
	for _, scheme := range []netproxy.Scheme{netproxy.SchemeHTTP, netproxy.SchemeSOCKS5} {
		t.Run(string(scheme), func(t *testing.T) {
			s := netproxy.Settings{Mode: netproxy.ModeCustom, Scheme: scheme, Host: "127.0.0.1", Port: closedPort(t)}
			for _, target := range []string{"http://" + probeHost + "/", "https://" + probeHost + "/"} {
				assertFailure(t, netproxy.Probe(t.Context(), s, mustURL(t, target)), netproxy.ReasonProxyUnreachable)
			}
		})
	}
}

func TestProbeProxyAuth(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(origin.Close)
	cases := []struct {
		name     string
		scheme   netproxy.Scheme
		username string
		target   string
	}{
		{"http CONNECT 407 without credentials", netproxy.SchemeHTTP, "", "https://" + probeHost + "/"},
		{"http CONNECT 407 wrong password", netproxy.SchemeHTTP, "pu", "https://" + probeHost + "/"},
		// 明文目标经 HTTP 代理时 407 是代理自己回的响应，不是目标的。
		{"http proxy answers 407 for http target", netproxy.SchemeHTTP, "", "http://" + probeHost + "/"},
		{"socks5 without credentials", netproxy.SchemeSOCKS5, "", "http://" + probeHost + "/"},
		{"socks5 wrong password", netproxy.SchemeSOCKS5, "pu", "http://" + probeHost + "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := netproxytest.NewRecordingProxy(t)
			proxy.Forward(probeHost, origin.Listener.Addr().String())
			s := proxy.Settings(tc.scheme)
			proxy.RequireAuth("pu", "pp")
			s.Username, s.Password = tc.username, "wrong"

			got := netproxy.Probe(t.Context(), s, mustURL(t, tc.target))

			assertFailure(t, got, netproxy.ReasonProxyAuth)
			assert.NotEqual(t, netproxy.ViaDirect, got.Via)
		})
	}
}

func TestProbeTLSFailure(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(plain.Close)
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	t.Cleanup(untrusted.Close)

	t.Run("plain HTTP server on https URL", func(t *testing.T) {
		got := netproxy.Probe(t.Context(), directSettings(), mustURL(t, "https://"+plain.Listener.Addr().String()+"/"))
		assertFailure(t, got, netproxy.ReasonTLS)
	})
	t.Run("untrusted certificate", func(t *testing.T) {
		assertFailure(t, netproxy.Probe(t.Context(), directSettings(), mustURL(t, untrusted.URL)), netproxy.ReasonTLS)
	})
	t.Run("untrusted certificate through CONNECT tunnel", func(t *testing.T) {
		proxy := netproxytest.NewRecordingProxy(t)
		proxy.Forward(probeHost, untrusted.Listener.Addr().String())
		got := netproxy.Probe(t.Context(), proxy.Settings(netproxy.SchemeHTTP), mustURL(t, "https://"+probeHost+"/"))
		assertFailure(t, got, netproxy.ReasonTLS)
	})
}

func TestProbeTimeout(t *testing.T) {
	addr, _ := hangingListener(t)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := netproxy.Probe(ctx, directSettings(), mustURL(t, "http://"+addr+"/"))

	assertFailure(t, got, netproxy.ReasonTimeout)
	assert.Less(t, time.Since(start), 5*time.Second, "the caller's deadline bounds the probe")
}

func TestProbeOtherFailure(t *testing.T) {
	t.Run("target refuses the connection", func(t *testing.T) {
		got := netproxy.Probe(t.Context(), directSettings(), mustURL(t, "http://127.0.0.1:"+strconv.Itoa(closedPort(t))+"/"))
		assertFailure(t, got, netproxy.ReasonOther)
	})
	t.Run("proxy rejects the CONNECT with 502", func(t *testing.T) {
		proxy := netproxytest.NewRecordingProxy(t)
		got := netproxy.Probe(t.Context(), proxy.Settings(netproxy.SchemeHTTP), mustURL(t, "https://"+probeHost+"/"))
		assertFailure(t, got, netproxy.ReasonOther)
	})
}
