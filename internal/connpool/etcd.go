package connpool

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const (
	defaultEtcdDialTimeout    = 5 * time.Second
	defaultEtcdCommandTimeout = 10 * time.Second
)

// buildEtcdClientConfig 把 EtcdConfig + 解密后的明文密码组装为 clientv3.Config。
// 仅处理参数映射,不负责 dialer/SSH 隧道（在 DialEtcd 中处理）。
func buildEtcdClientConfig(cfg *asset_entity.EtcdConfig, password string) (clientv3.Config, error) {
	dialTimeout := defaultEtcdDialTimeout
	if cfg.DialTimeoutSeconds > 0 {
		dialTimeout = time.Duration(cfg.DialTimeoutSeconds) * time.Second
	}

	c := clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: dialTimeout,
		Username:    cfg.Username,
		Password:    password,
	}

	if cfg.TLS {
		tlsCfg, err := buildEtcdTLSConfig(cfg)
		if err != nil {
			return clientv3.Config{}, err
		}
		c.TLS = tlsCfg
	}
	return c, nil
}

func buildEtcdTLSConfig(cfg *asset_entity.EtcdConfig) (*tls.Config, error) {
	return BuildTLSConfig("etcd", TLSFields{
		ServerName: cfg.TLSServerName,
		Insecure:   cfg.TLSInsecure,
		CAFile:     cfg.TLSCAFile,
		CertFile:   cfg.TLSCertFile,
		KeyFile:    cfg.TLSKeyFile,
	})
}

// etcdEntry 缓存项,client 和 tunnel 均可能为 nil(invalidate 暂用空项 / 直连无 tunnel)
type etcdEntry struct {
	client   *clientv3.Client
	tunnel   io.Closer // 可为 nil(直连)
	lastUsed int64     // unix 秒,通过 atomic 操作
}

type etcdPool struct {
	mu      sync.Mutex
	entries map[int64]*etcdEntry
}

func newEtcdPool() *etcdPool {
	return &etcdPool{entries: map[int64]*etcdEntry{}}
}

func (p *etcdPool) get(id int64) *etcdEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[id]
	if e != nil {
		atomic.StoreInt64(&e.lastUsed, time.Now().Unix())
	}
	return e
}

func (p *etcdPool) put(id int64, e *etcdEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[id] = e
}

func (p *etcdPool) invalidate(id int64) {
	p.mu.Lock()
	e := p.entries[id]
	delete(p.entries, id)
	p.mu.Unlock()
	if e != nil {
		closeEtcdEntry(e)
	}
}

func (p *etcdPool) gc(maxIdle time.Duration) {
	cutoff := time.Now().Add(-maxIdle).Unix()
	p.mu.Lock()
	stale := []*etcdEntry{}
	for id, e := range p.entries {
		if atomic.LoadInt64(&e.lastUsed) < cutoff {
			stale = append(stale, e)
			delete(p.entries, id)
		}
	}
	p.mu.Unlock()
	for _, e := range stale {
		closeEtcdEntry(e)
	}
}

func closeEtcdEntry(e *etcdEntry) {
	if e.client != nil {
		if err := e.client.Close(); err != nil {
			logger.Default().Warn("close etcd client", zap.Error(err))
		}
	}
	if e.tunnel != nil {
		if err := e.tunnel.Close(); err != nil {
			logger.Default().Warn("close etcd ssh tunnel", zap.Error(err))
		}
	}
}

var globalEtcdPool = newEtcdPool()

func init() {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for range t.C {
			globalEtcdPool.gc(5 * time.Minute)
		}
	}()
}

// InvalidateEtcd 资产更新/删除时调用,强制清理缓存的客户端。
func InvalidateEtcd(assetID int64) {
	globalEtcdPool.invalidate(assetID)
}

func etcdTunnelID(asset *asset_entity.Asset, cfg *asset_entity.EtcdConfig) int64 {
	if asset != nil && asset.SSHTunnelID > 0 {
		return asset.SSHTunnelID
	}
	if cfg != nil {
		return cfg.SSHAssetID
	}
	return 0
}

// DialEtcd 创建新的 etcd 客户端。可选走 SSH 隧道(仅对第一个 endpoint)。
// 返回的 tunnel 可为 nil(直连场景)。调用方负责 client.Close() / tunnel.Close()(若非 nil)。
func DialEtcd(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.EtcdConfig, password string, sshPool *sshpool.Pool) (*clientv3.Client, io.Closer, error) {
	clientCfg, err := buildEtcdClientConfig(cfg, password)
	if err != nil {
		return nil, nil, err
	}

	var tunnel *SSHTunnel
	tunnelID := etcdTunnelID(asset, cfg)
	if tunnelID > 0 && sshPool != nil && len(cfg.Endpoints) > 0 {
		host, portStr, err := net.SplitHostPort(cfg.Endpoints[0])
		if err != nil {
			return nil, nil, fmt.Errorf("解析 etcd endpoint 失败: %w", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, nil, fmt.Errorf("解析 etcd endpoint 端口失败: %w", err)
		}
		tunnel = NewSSHTunnel(tunnelID, host, port, sshPool)
		clientCfg.DialOptions = append(clientCfg.DialOptions,
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return tunnel.Dial(ctx)
			}))
		// SSH 隧道场景仅对第一个 endpoint 起隧道
		clientCfg.Endpoints = []string{cfg.Endpoints[0]}
	}

	client, err := clientv3.New(clientCfg)
	if err != nil {
		if tunnel != nil {
			if cerr := tunnel.Close(); cerr != nil {
				logger.Ctx(ctx).Warn("close etcd ssh tunnel after dial failure", zap.Error(cerr))
			}
		}
		return nil, nil, fmt.Errorf("etcd dial 失败: %w", err)
	}

	// 主动 Status 验证连通性
	pingCtx, cancel := context.WithTimeout(ctx, clientCfg.DialTimeout)
	defer cancel()
	if _, err := client.Status(pingCtx, clientCfg.Endpoints[0]); err != nil {
		if cerr := client.Close(); cerr != nil {
			logger.Ctx(ctx).Warn("close etcd client after status failure", zap.Error(cerr))
		}
		if tunnel != nil {
			if cerr := tunnel.Close(); cerr != nil {
				logger.Ctx(ctx).Warn("close etcd ssh tunnel after status failure", zap.Error(cerr))
			}
		}
		return nil, nil, fmt.Errorf("etcd 连通性检查失败: %w", err)
	}

	// 直连时 tunnel 是 *SSHTunnel 的 typed-nil,避免接口 typed-nil 陷阱
	if tunnel == nil {
		return client, nil, nil
	}
	return client, tunnel, nil
}

// GetOrDialEtcd 返回缓存中的客户端;未缓存则建立连接并缓存。
func GetOrDialEtcd(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.EtcdConfig, password string, sshPool *sshpool.Pool) (*clientv3.Client, error) {
	if e := globalEtcdPool.get(asset.ID); e != nil {
		return e.client, nil
	}
	client, tunnel, err := DialEtcd(ctx, asset, cfg, password, sshPool)
	if err != nil {
		return nil, err
	}
	globalEtcdPool.put(asset.ID, &etcdEntry{
		client:   client,
		tunnel:   tunnel,
		lastUsed: time.Now().Unix(),
	})
	logger.Ctx(ctx).Info("etcd client dialed",
		zap.Int64("assetID", asset.ID),
		zap.Int("endpoints", len(cfg.Endpoints)),
		zap.Bool("tls", cfg.TLS),
		zap.Bool("tunneled", tunnel != nil),
	)
	return client, nil
}
