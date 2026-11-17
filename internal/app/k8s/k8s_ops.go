package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	k8spkg "github.com/opskat/opskat/internal/pkg/k8s"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/pkg/socksdial"
	"github.com/opskat/opskat/internal/pkg/streambatch"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// k8sCallContext 是一个 K8S 资产的调用上下文，按资产缓存在 K8s.conns 里，同一资产的请求共用它。
type k8sCallContext struct {
	// conn 按资产配置（kubeconfig、context、SSH 隧道 / 代理链拨号）建立，请求经它复用到集群的连接。
	conn *k8spkg.Conn
	// namespace 是资产配置的默认命名空间，空表示未配置。
	namespace string
}

// Close 让缓存丢弃这一项时关闭它的连接。
func (c *k8sCallContext) Close() error { return c.conn.Close() }

// call 取资产的调用上下文：已缓存则复用，否则按资产当前配置新建并缓存。
// 资产改配置 / 删除、集群标签关闭、空闲超时后缓存项被丢弃，下次调用按最新配置重建；
// 连接断开由 HTTP 客户端在下次请求时重新拨号，缓存项本身不会因此失效。
func (k *K8s) call(ctx context.Context, assetID int64) (*k8sCallContext, error) {
	created := false
	c, _, err := k.conns.GetOrDial(assetID, strconv.FormatInt(assetID, 10), func() (*k8sCallContext, io.Closer, error) {
		created = true
		c, err := k.loadCall(ctx, assetID)
		return c, nil, err
	})
	if err != nil {
		return nil, err
	}
	if created {
		logger.Ctx(ctx).Debug("k8s connection created", zap.Int64("assetID", assetID))
	} else {
		logger.Ctx(ctx).Debug("k8s connection reused", zap.Int64("assetID", assetID))
	}
	return c, nil
}

// ReleaseK8sConnections 关闭并丢弃该资产缓存的集群连接（前端关闭集群标签时调用）；之后的请求按资产当前配置重新建立。
func (k *K8s) ReleaseK8sConnections(assetID int64) {
	k.dropAssetConns(k.ctx, assetID)
}

// dropAssetConns 丢弃该资产缓存的连接；也由 assetconn 在资产改配置或被删除时调用（见 New）。
func (k *K8s) dropAssetConns(ctx context.Context, assetID int64) {
	k.conns.DropAsset(assetID)
	logger.Ctx(ctx).Info("k8s connections released", zap.Int64("assetID", assetID))
}

// loadK8sCall 校验资产、解析 K8S 配置、解密 kubeconfig、按 SSH 隧道 / 代理链构造连接。
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
	opts, err := k.k8sClientOptions(ctx, asset, cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve K8S proxy chain: %w", err)
	}
	return newCallContext(kubeconfig, cfg.Namespace, opts...)
}

// newCallContext 由已解密的 kubeconfig、默认命名空间与拨号选项构造调用上下文。
func newCallContext(kubeconfig, namespace string, opts ...k8spkg.ClientOption) (*k8sCallContext, error) {
	conn, err := k8spkg.NewConn(kubeconfig, opts...)
	if err != nil {
		return nil, err
	}
	return &k8sCallContext{conn: conn, namespace: namespace}, nil
}

// runK8sCall 是 K8S 读取类 IPC 共用的模板：加载上下文 → 调用 fn → JSON 序列化，
// 并记录开始 / 结束 / 失败（带 assetID）。
func (k *K8s) runK8sCall(assetID int64, label string, fn func(ctx context.Context, c *k8sCallContext) (any, error)) (string, error) {
	return k.runLogged(assetID, label, 30*time.Second, false, nil, fn)
}

// runK8sWrite 是写入类 IPC（更新 / 应用）的模板：同 runK8sCall，但开始 / 结束以 Info 记录并带上操作对象
// （target 只放资源标识，绝不放资源内容）。
func (k *K8s) runK8sWrite(assetID int64, label string, target []zap.Field, fn func(ctx context.Context, c *k8sCallContext) (any, error)) (string, error) {
	return k.runLogged(assetID, label, 2*time.Minute, true, target, fn)
}

func (k *K8s) runLogged(assetID int64, label string, timeout time.Duration, write bool, target []zap.Field, fn func(ctx context.Context, c *k8sCallContext) (any, error)) (string, error) {
	ctx, cancel := context.WithTimeout(k.ctx, timeout)
	defer cancel()
	log := logger.Ctx(ctx).With(append([]zap.Field{zap.String("op", label), zap.Int64("assetID", assetID)}, target...)...)
	say := log.Debug
	if write {
		say = log.Info
	}
	start := time.Now()
	say("k8s call start")

	data, err := k.doK8sCall(ctx, assetID, label, fn)
	if err != nil {
		log.Error("k8s call failed", zap.Duration("elapsed", time.Since(start)), zap.Error(err))
		return "", err
	}
	say("k8s call done", zap.Duration("elapsed", time.Since(start)))
	return data, nil
}

func (k *K8s) doK8sCall(ctx context.Context, assetID int64, label string, fn func(ctx context.Context, c *k8sCallContext) (any, error)) (string, error) {
	c, err := k.call(ctx, assetID)
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

func (k *K8s) StartK8sPodLogs(assetID int64, namespace, podName, container string, tailLines int64) (string, error) {
	loadCtx, loadCancel := context.WithTimeout(k.ctx, 30*time.Second)
	defer loadCancel()
	c, err := k.call(loadCtx, assetID)
	if err != nil {
		return "", err
	}

	streamID := fmt.Sprintf("k8s-log-%d", k.logStreamCounter.Add(1))
	ctx, cancel := context.WithCancel(k.ctx)
	k.logStreams.Store(streamID, cancel)

	reader, err := k8spkg.StreamPodLogs(ctx, c.conn, namespace, podName, container, tailLines)
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

		pumpPodLogs(reader, streamID, k.emit)
	}()

	return streamID, nil
}

// podLogMaxBatchBytes 把单个 k8s:log 事件保持在原先单次 Read 的 4KB 量级：前端按事件块数
// （MAX_LOG_CHUNKS）保留日志历史，批太大会放大它的内存占用。
const podLogMaxBatchBytes = 4096

// pumpPodLogs 把日志流读到 EOF/出错为止，攒批后按 k8s:log 事件发给前端；
// 结束时先交付尾部数据，再发 k8s:logerr（非 EOF）与 k8s:logend。
func pumpPodLogs(reader io.Reader, streamID string, emit func(event string, payload any)) {
	batch := streambatch.New(streambatch.DefaultWindow, podLogMaxBatchBytes, func(data []byte) {
		emit("k8s:log:"+streamID, base64.StdEncoding.EncodeToString(data))
	})
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			batch.Add(buf[:n])
		}
		if err != nil {
			batch.Close()
			if err != io.EOF {
				emit("k8s:logerr:"+streamID, err.Error())
			}
			emit("k8s:logend:"+streamID, streamID)
			return
		}
	}
}

func (k *K8s) StopK8sPodLogs(streamID string) {
	if cancel, ok := k.logStreams.LoadAndDelete(streamID); ok {
		cancel.(context.CancelFunc)()
	}
}

func (k *K8s) k8sClientOptions(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.K8sConfig) ([]k8spkg.ClientOption, error) {
	opts := make([]k8spkg.ClientOption, 0, 2)
	if cfg.Context != "" {
		opts = append(opts, k8spkg.WithContext(cfg.Context))
	}
	effectiveChain := asset_entity.EffectiveProxyChain(cfg.ProxyChain, asset.SSHTunnelID, cfg.Proxy)
	layers, err := credential_resolver.Default().ResolveProxyChain(ctx, effectiveChain, 5)
	if err != nil {
		return nil, err
	}
	if len(layers) > 0 {
		opts = append(opts, k8spkg.WithDial(func(ctx context.Context, _, address string) (net.Conn, error) {
			return proxychain.Chain{Layers: layers}.Dial(ctx, address)
		}))
		return opts, nil
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
	return opts, nil
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
