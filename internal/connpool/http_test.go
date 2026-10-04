package connpool

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
)

// httpGet 发一次 GET 并返回状态码（响应体读完即关）。
func httpGet(t *testing.T, transport http.RoundTripper, url string) (int, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, err
}

func writeServerCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, os.WriteFile(path, pemBytes, 0o600))
	return path
}

func TestNewHTTPTransport_TLSUsesAssetCAAndNeverSkipsVerification(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	// 没配 CA：证书校验失败必须原样报错，不能悄悄跳过校验。
	plain, route, err := NewHTTPTransport(context.Background(), HTTPConnConfig{}, nil)
	require.NoError(t, err)
	assert.Equal(t, HTTPRouteDirect, route)
	_, err = httpGet(t, plain, srv.URL)
	require.Error(t, err)
	var unknownAuthority x509.UnknownAuthorityError
	assert.ErrorAs(t, err, &unknownAuthority)

	withCA, _, err := NewHTTPTransport(context.Background(), HTTPConnConfig{TLS: TLSFields{CAFile: writeServerCA(t, srv)}}, nil)
	require.NoError(t, err)
	status, err := httpGet(t, withCA, srv.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)

	insecure, _, err := NewHTTPTransport(context.Background(), HTTPConnConfig{TLS: TLSFields{Insecure: true}}, nil)
	require.NoError(t, err)
	status, err = httpGet(t, insecure, srv.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)

	_, _, err = NewHTTPTransport(context.Background(), HTTPConnConfig{TLS: TLSFields{CAFile: filepath.Join(t.TempDir(), "missing.pem")}}, nil)
	assert.ErrorContains(t, err, "CA")
}

func TestNewHTTPTransport_TunnelWithoutPoolFails(t *testing.T) {
	_, _, err := NewHTTPTransport(context.Background(), HTTPConnConfig{TunnelID: 9}, nil)
	assert.ErrorContains(t, err, "SSH", "a configured tunnel must never degrade to a direct connection")
}

func TestNewHTTPTransport_ProxyChainRoutesThroughProxy(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	enabled := true
	proxyHost, proxyPort := splitHostPort(t, socksdialtest.Start(t, "", ""))
	chain := &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{{
		Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: &enabled, Order: 1, Host: proxyHost, Port: proxyPort,
	}}}
	transport, route, err := NewHTTPTransport(context.Background(), HTTPConnConfig{ProxyChain: chain, TunnelID: 5}, nil)
	require.NoError(t, err)
	assert.Equal(t, HTTPRouteProxyChain, route, "a proxy chain takes precedence over the tunnel")
	status, err := httpGet(t, transport, srv.URL)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, status)
	assert.Equal(t, 1, hits)

	// 代理不可达：错误原样返回，不退回直连（直连本可以成功）。
	deadHost, deadPort := splitHostPort(t, closedAddr(t))
	dead := &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{{
		Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: &enabled, Order: 1, Host: deadHost, Port: deadPort,
	}}}
	transport, _, err = NewHTTPTransport(context.Background(), HTTPConnConfig{ProxyChain: dead, TunnelID: 0}, nil)
	require.NoError(t, err)
	_, err = httpGet(t, transport, srv.URL)
	require.Error(t, err)
	assert.Equal(t, 1, hits, "the request must not reach the server directly")
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, port
}

// closedAddr 返回一个当前没有监听者的本地地址。
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func TestHTTPRouteFor(t *testing.T) {
	on, off := true, false
	ssh := asset_entity.ProxyChainLayer{Type: asset_entity.ProxyChainLayerSSH, Enabled: &on, Order: 1, SSHAssetID: 3}
	disabled := asset_entity.ProxyChainLayer{Type: asset_entity.ProxyChainLayerSSH, Enabled: &off, Order: 1, SSHAssetID: 3}

	assert.Equal(t, HTTPRouteProxyChain,
		HTTPRouteFor(HTTPConnConfig{ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{ssh}}, TunnelID: 5}),
		"an enabled proxy chain wins over the tunnel")
	assert.Equal(t, HTTPRouteSSHTunnel, HTTPRouteFor(HTTPConnConfig{TunnelID: 5}))
	assert.Equal(t, HTTPRouteSSHTunnel,
		HTTPRouteFor(HTTPConnConfig{ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{disabled}}, TunnelID: 5}),
		"a chain whose layers are all disabled is empty and falls through to the tunnel")
	assert.Equal(t, HTTPRouteDirect, HTTPRouteFor(HTTPConnConfig{}))
}
