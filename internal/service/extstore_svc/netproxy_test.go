package extstore_svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/pkg/netproxy/netproxytest"
)

// 调用 netproxy.Set 的测试改了进程级状态，不能 t.Parallel。
func useNetworkProxy(t *testing.T, s netproxy.Settings) {
	t.Helper()
	prev := netproxy.Get()
	netproxy.Set(s)
	t.Cleanup(func() { netproxy.Set(prev) })
}

func TestIndexFetchFollowsNetworkProxy(t *testing.T) {
	const host = "index.store.test"
	setup := func(t *testing.T, mode netproxy.Mode) *netproxytest.Proxy {
		proxy := netproxytest.NewRecordingProxy(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("index"))
		}))
		t.Cleanup(srv.Close)
		proxy.Forward(host, strings.TrimPrefix(srv.URL, "http://"))
		s := proxy.Settings(netproxy.SchemeHTTP)
		s.Mode = mode
		useNetworkProxy(t, s)
		return proxy
	}

	t.Run("custom proxy carries the request", func(t *testing.T) {
		proxy := setup(t, netproxy.ModeCustom)
		body, err := fetch(context.Background(), "http://"+host+"/index.json", 1<<10)
		require.NoError(t, err)
		assert.Equal(t, "index", string(body))
		require.Len(t, proxy.Requests(), 1)
		assert.Equal(t, host+":80", proxy.Requests()[0].Target)
	})
	t.Run("direct never touches the proxy", func(t *testing.T) {
		proxy := setup(t, netproxy.ModeDirect)
		_, err := fetch(context.Background(), "http://"+host+"/index.json", 1<<10)
		require.Error(t, err)
		assert.Empty(t, proxy.Requests())
	})
}
