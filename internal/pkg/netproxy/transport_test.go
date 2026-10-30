package netproxy_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/pkg/netproxy/netproxytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// svcHost 是测试目标的非回环主机名：回环地址永远不经代理，目标必须是代理才认得的名字。
const svcHost = "svc.example"

func startUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只回固定内容（回显请求数据会被 gosec 判为 XSS）；按路径区分以确认请求原样到达上游。
		_, _ = io.WriteString(w, upstreamBodies[r.URL.Path])
	}))
	t.Cleanup(srv.Close)
	return srv
}

var upstreamBodies = map[string]string{
	"/hello": "upstream hello", "/s": "upstream s", "/direct": "upstream direct", "/env": "upstream env",
	"/one": "upstream one", "/two": "upstream two",
}

func svcURL(srv *httptest.Server, path string) string {
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "http://" + net.JoinHostPort(svcHost, port) + path
}

func get(t *testing.T, rt http.RoundTripper, rawURL string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: rt}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

// dialOverride 让 svcHost 直连拨到上游，其余地址照常拨号，用于观察「直连」路径。
func dialOverride(tr *http.Transport, upstream string) {
	dial := tr.DialContext
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, _ := net.SplitHostPort(addr); host == svcHost {
			addr = upstream
		}
		return dial(ctx, network, addr)
	}
}

func TestNewTransportCustomHTTPForwardsAbsoluteURI(t *testing.T) {
	up := startUpstream(t)
	p := netproxytest.NewRecordingProxy(t)
	p.Forward(svcHost, up.Listener.Addr().String())
	p.RequireAuth("user@corp", "p:ss/w@rd")

	target := svcURL(up, "/hello")
	body, err := get(t, netproxy.NewTransport(p.Settings(netproxy.SchemeHTTP)), target)
	require.NoError(t, err)
	assert.Equal(t, "upstream hello", body)

	reqs := p.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, netproxytest.ProtocolHTTP, reqs[0].Protocol)
	assert.Equal(t, http.MethodGet, reqs[0].Method)
	assert.Equal(t, target, reqs[0].URL)
	assert.Equal(t, "user@corp", reqs[0].Username)
	assert.Equal(t, "p:ss/w@rd", reqs[0].Password)
}

func TestNewTransportCustomHTTPTunnelsHTTPS(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tls")
	}))
	t.Cleanup(up.Close)
	p := netproxytest.NewRecordingProxy(t)
	// httptest 的证书签给 example.com；由代理把它映射到上游，本机不解析。
	p.Forward("example.com", up.Listener.Addr().String())

	tr := netproxy.NewTransport(p.Settings(netproxy.SchemeHTTP))
	tr.TLSClientConfig = up.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	_, port, _ := net.SplitHostPort(up.Listener.Addr().String())
	target := "example.com:" + port
	body, err := get(t, tr, "https://"+target+"/")
	require.NoError(t, err)
	assert.Equal(t, "tls", body)

	reqs := p.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodConnect, reqs[0].Method)
	assert.Equal(t, target, reqs[0].Target)
}

func TestNewTransportSurfacesProxyAuthFailure(t *testing.T) {
	p := netproxytest.NewRecordingProxy(t)
	p.Forward("example.com", "127.0.0.1:1")
	p.RequireAuth("user", "right")
	s := p.Settings(netproxy.SchemeHTTP)
	s.Password = "wrong"

	_, err := get(t, netproxy.NewTransport(s), "https://example.com/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Proxy Authentication Required")
	reqs := p.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, "wrong", reqs[0].Password)
}

func TestNewTransportCustomSOCKS5PassesHostname(t *testing.T) {
	up := startUpstream(t)
	p := netproxytest.NewRecordingProxy(t)
	p.Forward(svcHost, up.Listener.Addr().String())
	p.RequireAuth("sockuser", "sockpass")

	target := svcURL(up, "/s")
	body, err := get(t, netproxy.NewTransport(p.Settings(netproxy.SchemeSOCKS5)), target)
	require.NoError(t, err)
	assert.Equal(t, "upstream s", body)

	reqs := p.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, netproxytest.ProtocolSOCKS5, reqs[0].Protocol)
	assert.Equal(t, strings.TrimPrefix(svcURL(up, ""), "http://"), reqs[0].Target)
	assert.Equal(t, "sockuser", reqs[0].Username)
	assert.Equal(t, "sockpass", reqs[0].Password)
}

func TestNewTransportBypassesProxy(t *testing.T) {
	up := startUpstream(t)
	p := netproxytest.NewRecordingProxy(t)
	p.Forward(svcHost, up.Listener.Addr().String())
	setProxyEnv(t, map[string]string{"HTTPS_PROXY": "http://" + p.Addr(), "HTTP_PROXY": "http://" + p.Addr()})

	bypass := p.Settings(netproxy.SchemeHTTP)
	bypass.NoProxy = "*." + svcHost
	cases := map[string]netproxy.Settings{
		"direct ignores env proxy": {Mode: netproxy.ModeDirect},
		"custom no-proxy entry":    bypass,
	}
	for name, s := range cases {
		tr := netproxy.NewTransport(s)
		dialOverride(tr, up.Listener.Addr().String())
		body, err := get(t, tr, svcURL(up, "/direct"))
		require.NoError(t, err, name)
		assert.Equal(t, "upstream direct", body, name)
	}
	assert.Empty(t, p.Requests())
}

func TestNewTransportEnvUsesEnvironmentProxy(t *testing.T) {
	up := startUpstream(t)
	p := netproxytest.NewRecordingProxy(t)
	p.Forward(svcHost, up.Listener.Addr().String())
	setProxyEnv(t, map[string]string{"HTTP_PROXY": "http://" + p.Addr()})

	body, err := get(t, netproxy.NewTransport(netproxy.Default()), svcURL(up, "/env"))
	require.NoError(t, err)
	assert.Equal(t, "upstream env", body)
	assert.Len(t, p.Requests(), 1)
}

func TestTransportFollowsSet(t *testing.T) {
	t.Cleanup(func() { netproxy.Set(netproxy.Default()) })
	up := startUpstream(t)
	p1 := netproxytest.NewRecordingProxy(t)
	p2 := netproxytest.NewRecordingProxy(t)
	p1.Forward(svcHost, up.Listener.Addr().String())
	p2.Forward(svcHost, up.Listener.Addr().String())
	rt := netproxy.Transport()

	netproxy.Set(p1.Settings(netproxy.SchemeHTTP))
	assert.Equal(t, p1.Settings(netproxy.SchemeHTTP), netproxy.Get())
	body, err := get(t, rt, svcURL(up, "/one"))
	require.NoError(t, err)
	assert.Equal(t, "upstream one", body)
	assert.Len(t, p1.Requests(), 1)

	netproxy.Set(p2.Settings(netproxy.SchemeSOCKS5))
	body, err = get(t, rt, svcURL(up, "/two"))
	require.NoError(t, err)
	assert.Equal(t, "upstream two", body)
	assert.Len(t, p1.Requests(), 1)
	assert.Len(t, p2.Requests(), 1)

	netproxy.Set(netproxy.Settings{Mode: netproxy.ModeDirect})
	_, _ = get(t, rt, svcURL(up, "/three")) // 直连时本机解析 svc.example，结果不重要
	assert.Len(t, p1.Requests(), 1)
	assert.Len(t, p2.Requests(), 1)
}

func TestTransportInFlightRequestSurvivesSet(t *testing.T) {
	t.Cleanup(func() { netproxy.Set(netproxy.Default()) })
	entered, release := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			_, _ = io.WriteString(w, "first,")
			w.(http.Flusher).Flush()
			close(entered)
			<-release
		}
		_, _ = io.WriteString(w, map[string]string{"/slow": "done slow", "/fast": "done fast"}[r.URL.Path])
	}))
	t.Cleanup(up.Close)
	p1 := netproxytest.NewRecordingProxy(t)
	p2 := netproxytest.NewRecordingProxy(t)
	// SOCKS5 隧道保持长连接，换设置后若旧连接被强行关闭，在途请求会读到错误。
	p1.Forward(svcHost, up.Listener.Addr().String())
	p2.Forward(svcHost, up.Listener.Addr().String())
	rt := netproxy.Transport()
	netproxy.Set(p1.Settings(netproxy.SchemeSOCKS5))

	type result struct {
		body string
		err  error
	}
	slow := make(chan result, 1)
	go func() {
		body, err := get(t, rt, svcURL(up, "/slow"))
		slow <- result{body, err}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("slow request never reached upstream")
	}

	netproxy.Set(p2.Settings(netproxy.SchemeSOCKS5))
	body, err := get(t, rt, svcURL(up, "/fast"))
	require.NoError(t, err)
	assert.Equal(t, "done fast", body)
	assert.Len(t, p2.Requests(), 1, "new request uses the new settings")

	close(release)
	r := <-slow
	require.NoError(t, r.err)
	assert.Equal(t, "first,done slow", r.body)
	assert.Len(t, p1.Requests(), 1)
}

func TestSetLeavesDefaultTransportAlone(t *testing.T) {
	t.Cleanup(func() { netproxy.Set(netproxy.Default()) })
	setProxyEnv(t, nil)
	up := startUpstream(t)
	p := netproxytest.NewRecordingProxy(t)
	p.Forward(svcHost, up.Listener.Addr().String())
	netproxy.Set(p.Settings(netproxy.SchemeHTTP))
	_ = netproxy.NewTransport(p.Settings(netproxy.SchemeHTTP))

	_, _ = get(t, http.DefaultTransport, svcURL(up, "/default")) // 资产连接克隆的默认传输不得被送进代理
	assert.Empty(t, p.Requests())
}
