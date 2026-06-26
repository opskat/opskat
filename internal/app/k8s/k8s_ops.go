package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	k8spkg "github.com/opskat/opskat/internal/pkg/k8s"
	"github.com/opskat/opskat/internal/pkg/socksdial"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// k8sCallContext 加载 K8S 资产时返回的所有调用上下文。
// kubeconfig 是已解密的 YAML 文本，opts 已带上 SSH 隧道 dial 函数（若配置）。
type k8sCallContext struct {
	kubeconfig string
	opts       []k8spkg.ClientOption
}

// loadK8sCall 校验资产、解析 K8S 配置、解密 kubeconfig、构造 ClientOption。
func (k *K8s) loadK8sCall(ctx context.Context, assetID int64) (*k8sCallContext, error) {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("get asset: %w", err)
	}
	if !asset.IsK8s() {
		return nil, fmt.Errorf("asset %d is not a K8S cluster", assetID)
	}
	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return nil, fmt.Errorf("get K8S config: %w", err)
	}
	if cfg.Kubeconfig == "" {
		return nil, fmt.Errorf("no kubeconfig configured for this K8S asset")
	}
	h, ok := assettype.Get(asset_entity.AssetTypeK8s)
	if !ok {
		return nil, fmt.Errorf("k8s asset type handler not registered")
	}
	kubeconfig, err := h.ResolvePassword(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("decrypt kubeconfig: %w", err)
	}
	return &k8sCallContext{
		kubeconfig: kubeconfig,
		opts:       k.k8sClientOptions(asset, cfg),
	}, nil
}

// runK8sCall 是 9 个 GetK8sNamespace*/GetK8sClusterInfo/GetK8sPodDetail 共用的模板：
// 加载上下文 → 调用 fn → JSON 序列化。
func (k *K8s) runK8sCall(assetID int64, label string, fn func(ctx context.Context, c *k8sCallContext) (any, error)) (string, error) {
	ctx, cancel := context.WithTimeout(k.ctx, 30*time.Second)
	defer cancel()

	c, err := k.loadK8sCall(ctx, assetID)
	if err != nil {
		return "", err
	}
	result, err := fn(ctx, c)
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal %s: %w", label, err)
	}
	return string(data), nil
}

func (k *K8s) GetK8sClusterInfo(assetID int64) (string, error) {
	return k.runK8sCall(assetID, "get K8S cluster info", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetClusterInfo(ctx, c.kubeconfig, c.opts...)
	})
}

func (k *K8s) GetK8sNamespaceResources(assetID int64, namespace string) (string, error) {
	return k.runK8sCall(assetID, "get K8S namespace resources", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetNamespaceResources(ctx, c.kubeconfig, namespace, c.opts...)
	})
}

func (k *K8s) GetK8sNamespacePods(assetID int64, namespace string) (string, error) {
	return k.runK8sCall(assetID, "get K8S namespace pods", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetNamespacePods(ctx, c.kubeconfig, namespace, c.opts...)
	})
}

func (k *K8s) GetK8sNamespaceDeployments(assetID int64, namespace string) (string, error) {
	return k.runK8sCall(assetID, "get K8S namespace deployments", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetNamespaceDeployments(ctx, c.kubeconfig, namespace, c.opts...)
	})
}

func (k *K8s) GetK8sNamespaceServices(assetID int64, namespace string) (string, error) {
	return k.runK8sCall(assetID, "get K8S namespace services", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetNamespaceServices(ctx, c.kubeconfig, namespace, c.opts...)
	})
}

func (k *K8s) GetK8sNamespaceConfigMaps(assetID int64, namespace string) (string, error) {
	return k.runK8sCall(assetID, "get K8S namespace configmaps", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetNamespaceConfigMaps(ctx, c.kubeconfig, namespace, c.opts...)
	})
}

func (k *K8s) GetK8sNamespaceSecrets(assetID int64, namespace string) (string, error) {
	return k.runK8sCall(assetID, "get K8S namespace secrets", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetNamespaceSecrets(ctx, c.kubeconfig, namespace, c.opts...)
	})
}

func (k *K8s) GetK8sPodDetail(assetID int64, namespace, podName string) (string, error) {
	return k.runK8sCall(assetID, "get K8S pod detail", func(ctx context.Context, c *k8sCallContext) (any, error) {
		return k8spkg.GetPodDetail(ctx, c.kubeconfig, namespace, podName, c.opts...)
	})
}

func (k *K8s) StartK8sPodLogs(assetID int64, namespace, podName, container string, tailLines int64) (string, error) {
	loadCtx, loadCancel := context.WithTimeout(k.ctx, 30*time.Second)
	defer loadCancel()
	c, err := k.loadK8sCall(loadCtx, assetID)
	if err != nil {
		return "", err
	}

	streamID := fmt.Sprintf("k8s-log-%d", k.logStreamCounter.Add(1))
	ctx, cancel := context.WithCancel(k.ctx)
	k.logStreams.Store(streamID, cancel)

	reader, err := k8spkg.StreamPodLogs(ctx, c.kubeconfig, namespace, podName, container, tailLines, c.opts...)
	if err != nil {
		cancel()
		k.logStreams.Delete(streamID)
		return "", fmt.Errorf("open pod log stream: %w", err)
	}

	go func() {
		defer func() {
			if closeErr := reader.Close(); closeErr != nil {
				logger.Default().Warn("close k8s log reader", zap.Error(closeErr))
			}
		}()
		defer cancel()
		defer k.logStreams.Delete(streamID)

		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				data := base64.StdEncoding.EncodeToString(buf[:n])
				wailsRuntime.EventsEmit(k.ctx, "k8s:log:"+streamID, data)
			}
			if err != nil {
				if err != io.EOF {
					wailsRuntime.EventsEmit(k.ctx, "k8s:logerr:"+streamID, err.Error())
				}
				wailsRuntime.EventsEmit(k.ctx, "k8s:logend:"+streamID, streamID)
				return
			}
		}
	}()

	return streamID, nil
}

func (k *K8s) StopK8sPodLogs(streamID string) {
	if cancel, ok := k.logStreams.LoadAndDelete(streamID); ok {
		cancel.(context.CancelFunc)()
	}
}

func (k *K8s) k8sClientOptions(asset *asset_entity.Asset, cfg *asset_entity.K8sConfig) []k8spkg.ClientOption {
	opts := make([]k8spkg.ClientOption, 0, 2)
	if cfg.Context != "" {
		opts = append(opts, k8spkg.WithContext(cfg.Context))
	}

	switch selectDialSource(asset, cfg, k.pool != nil) {
	case dialTunnel:
		tunnelID := asset.SSHTunnelID
		opts = append(opts, k8spkg.WithDial(func(ctx context.Context, network, address string) (net.Conn, error) {
			client, err := k.pool.Get(ctx, tunnelID)
			if err != nil {
				return nil, fmt.Errorf("get SSH tunnel: %w", err)
			}
			conn, err := client.Dial(network, address)
			if err != nil {
				k.pool.Release(tunnelID)
				return nil, fmt.Errorf("dial K8S API through SSH tunnel: %w", err)
			}
			return &k8sTunnelConn{Conn: conn, pool: k.pool, assetID: tunnelID}, nil
		}))
	case dialProxy:
		// 代理密码入 socksdial 前须明文（与数据库族约定一致）。
		proxy := credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		opts = append(opts, k8spkg.WithDial(func(ctx context.Context, _, address string) (net.Conn, error) {
			return socksdial.Dial(ctx, proxy, address)
		}))
	}
	return opts
}

// dialSource 决定 K8S client 的底层拨号方式。
type dialSource int

const (
	dialNone dialSource = iota
	dialTunnel
	dialProxy
)

// selectDialSource 镜像 entity「SSH 隧道与 SOCKS5 代理互斥、隧道优先」约定：
// 配了隧道且连接池可用 → 隧道；否则未配隧道且配了代理 → 代理；其余直连。
func selectDialSource(asset *asset_entity.Asset, cfg *asset_entity.K8sConfig, poolAvailable bool) dialSource {
	if asset.SSHTunnelID != 0 && poolAvailable {
		return dialTunnel
	}
	if asset.SSHTunnelID == 0 && cfg.Proxy != nil {
		return dialProxy
	}
	return dialNone
}

type k8sTunnelConn struct {
	net.Conn
	pool    *sshpool.Pool
	assetID int64
	once    sync.Once
}

func (c *k8sTunnelConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.pool.Release(c.assetID) })
	return err
}
