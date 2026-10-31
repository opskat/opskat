package ociclient

import (
	"context"
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

func TestPullFollowsNetworkProxy(t *testing.T) {
	const host = "registry.oci.test"
	blob := []byte("proxied-package")
	setup := func(t *testing.T, mode netproxy.Mode) (*netproxytest.Proxy, Registry) {
		proxy := netproxytest.NewRecordingProxy(t)
		m := newMock(t, blob, true)
		proxy.Forward(host, strings.TrimPrefix(m.srv.URL, "http://"))
		s := proxy.Settings(netproxy.SchemeSOCKS5)
		s.Mode = mode
		useNetworkProxy(t, s)
		return proxy, Registry{Host: host + ":5000", PlainHTTP: true}
	}

	t.Run("custom proxy carries manifest, token and blob requests", func(t *testing.T) {
		proxy, reg := setup(t, netproxy.ModeCustom)
		require.NoError(t, Pull(context.Background(), reg, testRef, sum(blob), 1<<20, dstPath(t), nil))
		require.NotEmpty(t, proxy.Requests())
		for _, r := range proxy.Requests() {
			assert.Equal(t, host+":5000", r.Target)
		}
	})
	t.Run("direct never touches the proxy", func(t *testing.T) {
		proxy, reg := setup(t, netproxy.ModeDirect)
		err := Pull(context.Background(), reg, testRef, sum(blob), 1<<20, dstPath(t), nil)
		require.ErrorIs(t, err, ErrNetwork)
		assert.Empty(t, proxy.Requests())
	})
}
