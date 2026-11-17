package k8s

import (
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/k8s/k8stest"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
)

// connTestCluster 是一个假 API Server：列表 / 计数请求都回空结果（略带延迟，让并发请求真正重叠），
// 并按服务端看到的连接计数——每次经代理链拨号都在这里多一条新连接。
type connTestCluster struct {
	srv    *httptest.Server
	opened atomic.Int32
	closed atomic.Int32
	// viaChain 是经代理链（SOCKS5 层）建立的连接数；与 opened 相等说明没有连接绕过资产的代理链。
	viaChain atomic.Int32
}

func newConnTestCluster(t *testing.T, tls bool) *connTestCluster {
	c := &connTestCluster{}
	c.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Accept"), "as=Table") {
			_, _ = w.Write([]byte(`{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{},"columnDefinitions":[],"rows":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"kind":"PartialObjectMetadataList","apiVersion":"meta.k8s.io/v1","metadata":{},"items":[]}`))
	}))
	c.srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			c.opened.Add(1)
		case http.StateClosed, http.StateHijacked:
			c.closed.Add(1)
		}
	}
	if tls {
		c.srv.EnableHTTP2 = true
		c.srv.StartTLS()
	} else {
		c.srv.Start()
	}
	t.Cleanup(c.srv.Close)
	return c
}

func (c *connTestCluster) kubeconfig() string {
	var ca []byte
	if c.srv.TLS != nil {
		ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.srv.Certificate().Raw})
	}
	return k8stest.Kubeconfig(c.srv.URL, ca, "")
}

// connTestBinder 返回一个 binder：资产经 SOCKS5 代理链连到 cluster，loads 记录每个资产按配置构造连接的次数。
func connTestBinder(t *testing.T, cluster *connTestCluster) (*K8s, *sync.Map) {
	t.Cleanup(func() { assetconn.UnregisterForTest("k8s") })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	k := New(ctx, nil, nil)
	k.Startup(ctx)
	t.Cleanup(k.Cleanup)

	proxyHost, proxyPortText, err := net.SplitHostPort(countingForwarder(t, socksdialtest.Start(t, "", ""), &cluster.viaChain))
	require.NoError(t, err)
	proxyPort, err := strconv.Atoi(proxyPortText)
	require.NoError(t, err)
	enabled := true
	cfg := &asset_entity.K8sConfig{ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{{
		Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: &enabled, Host: proxyHost, Port: proxyPort,
	}}}}

	loads := &sync.Map{} // assetID -> *atomic.Int32
	k.loadCall = func(ctx context.Context, assetID int64) (*k8sCallContext, error) {
		n, _ := loads.LoadOrStore(assetID, &atomic.Int32{})
		n.(*atomic.Int32).Add(1)
		opts, err := k.k8sClientOptions(ctx, &asset_entity.Asset{ID: assetID}, cfg)
		if err != nil {
			return nil, err
		}
		return newCallContext(cluster.kubeconfig(), "", opts...)
	}
	return k, loads
}

// countingForwarder 把连接原样转给 target，并计数。
func countingForwarder(t *testing.T, target string, count *atomic.Int32) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			go func() {
				defer func() { _ = in.Close() }()
				out, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer func() { _ = out.Close() }()
				go func() { _, _ = io.Copy(out, in); _ = out.Close() }()
				_, _ = io.Copy(in, out)
			}()
		}
	}()
	return ln.Addr().String()
}

func loadCount(loads *sync.Map, assetID int64) int32 {
	n, ok := loads.Load(assetID)
	if !ok {
		return 0
	}
	return n.(*atomic.Int32).Load()
}

func listReq(assetID int64, ns string) K8sListRequest {
	return K8sListRequest{AssetID: assetID, Version: "v1", Resource: "pods", Namespace: ns}
}

func TestK8sConcurrentCallsForOneAssetShareConnections(t *testing.T) {
	for name, tls := range map[string]bool{"https (HTTP/2)": true, "http (HTTP/1.1)": false} {
		t.Run(name, func(t *testing.T) {
			cluster := newConnTestCluster(t, tls)
			k, _ := connTestBinder(t, cluster)

			// 打开集群页：几十个计数与列表同时发出。
			const n = 40
			var wg sync.WaitGroup
			errs := make(chan error, n)
			for i := range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					req := listReq(7, fmt.Sprintf("ns%d", i))
					var err error
					if i%2 == 0 {
						_, err = k.CountK8sResources(req)
					} else {
						_, err = k.ListK8sResources(req)
					}
					errs <- err
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			burst := cluster.opened.Load()
			t.Logf("%d concurrent calls opened %d connections through the proxy chain", n, burst)
			assert.Less(t, burst, int32(n/4), "concurrent calls for one asset must share a few connections")
			assert.Equal(t, burst, cluster.viaChain.Load(), "every connection must go through the asset's proxy chain")

			// 之后的请求继续用已建立的连接。
			for range 10 {
				_, err := k.CountK8sResources(listReq(7, "default"))
				require.NoError(t, err)
			}
			assert.Equal(t, burst, cluster.opened.Load(), "later calls must not dial again")
		})
	}
}

func TestK8sConfigChangeStopsUsingOldConnection(t *testing.T) {
	cluster := newConnTestCluster(t, true)
	k, loads := connTestBinder(t, cluster)

	_, err := k.CountK8sResources(listReq(7, "default"))
	require.NoError(t, err)
	require.Equal(t, int32(1), cluster.opened.Load())

	// 资产改配置 / 删除时 asset_svc 广播的就是它。
	assetconn.InvalidateAsset(context.Background(), 7)
	require.Eventually(t, func() bool { return cluster.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond,
		"the old connection must be closed")

	_, err = k.CountK8sResources(listReq(7, "default"))
	require.NoError(t, err)
	assert.Equal(t, int32(2), loadCount(loads, 7), "the next call must load the asset's new configuration")
	assert.Equal(t, int32(2), cluster.opened.Load(), "the next call must dial a new connection")
}

func TestK8sDroppedConnectionIsRebuiltOnNextCall(t *testing.T) {
	for name, tls := range map[string]bool{"https (HTTP/2)": true, "http (HTTP/1.1)": false} {
		t.Run(name, func(t *testing.T) {
			cluster := newConnTestCluster(t, tls)
			k, _ := connTestBinder(t, cluster)

			_, err := k.CountK8sResources(listReq(7, "default"))
			require.NoError(t, err)
			require.Equal(t, int32(1), cluster.opened.Load())

			cluster.srv.CloseClientConnections()
			require.Eventually(t, func() bool { return cluster.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

			_, err = k.CountK8sResources(listReq(7, "default"))
			require.NoError(t, err)
			assert.Equal(t, int32(2), cluster.opened.Load(), "the next call must dial a new connection")
			assert.Equal(t, int32(2), cluster.viaChain.Load(), "the new connection must go through the proxy chain")
		})
	}
}

func TestK8sReleaseConnectionsOnlyReleasesThatAsset(t *testing.T) {
	cluster := newConnTestCluster(t, true)
	k, loads := connTestBinder(t, cluster)

	for _, id := range []int64{7, 8} {
		_, err := k.CountK8sResources(listReq(id, "default"))
		require.NoError(t, err)
	}
	require.Equal(t, int32(2), cluster.opened.Load())

	// 关闭资产 7 的集群标签。
	k.ReleaseK8sConnections(7)
	require.Eventually(t, func() bool { return cluster.closed.Load() == 1 }, 5*time.Second, 10*time.Millisecond,
		"the released asset's connection must be closed")

	for _, id := range []int64{7, 8} {
		_, err := k.CountK8sResources(listReq(id, "default"))
		require.NoError(t, err)
	}
	assert.Equal(t, int32(2), loadCount(loads, 7), "the released asset must be loaded again")
	assert.Equal(t, int32(1), loadCount(loads, 8), "other assets keep their connection")
	assert.Equal(t, int32(3), cluster.opened.Load(), "only the released asset dials again")
}

func TestK8sCleanupClosesConnections(t *testing.T) {
	cluster := newConnTestCluster(t, true)
	k, _ := connTestBinder(t, cluster)

	_, err := k.CountK8sResources(listReq(7, "default"))
	require.NoError(t, err)
	_, err = k.CountK8sResources(listReq(8, "default"))
	require.NoError(t, err)
	require.Equal(t, int32(2), cluster.opened.Load())

	k.Cleanup()
	assert.Eventually(t, func() bool { return cluster.closed.Load() == 2 }, 5*time.Second, 10*time.Millisecond,
		"Cleanup must close every asset's connections")
}
