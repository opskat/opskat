package connpool

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// DialRedis 按部署模式创建 Redis 客户端(单机 / 集群 / 哨兵),经直连、SSH 隧道、
// SOCKS5 代理或代理链拨号(代理链 > 隧道 > 代理)。每条节点连接都按 go-redis 请求的
// 目标地址拨号,先应用 cfg.NodeAddressMap。
// password 为已解析的明文数据节点密码;cfg.Proxy.Password 与 cfg.SentinelPassword
// 为明文,均由调用方负责解密。
func DialRedis(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.RedisConfig, password string, sshPool *sshpool.Pool) (redis.UniversalClient, io.Closer, error) {
	mode := cfg.EffectiveMode()
	logFields := []zap.Field{
		zap.Int64("assetID", asset.ID),
		zap.String("mode", mode),
		zap.Strings("addrs", redisSeedAddrs(cfg)),
		zap.String("masterName", cfg.MasterName),
	}
	opts, err := buildRedisOptions(cfg, password)
	if err != nil {
		return nil, nil, err
	}

	tunnel, err := configureRedisTransport(opts, asset, cfg, sshPool)
	if err != nil {
		return nil, nil, err
	}

	client := newRedisClient(cfg, opts)
	if mode == asset_entity.RedisModeSentinel {
		client.AddHook(&sentinelMasterWatcher{assetID: asset.ID, masterName: cfg.MasterName})
	}
	if pingErr := checkRedisConnected(ctx, client); pingErr != nil {
		logger.Ctx(ctx).Error("redis connect failed", append(logFields, zap.Error(pingErr))...)
		if err := client.Close(); err != nil {
			logger.Default().Warn("close redis client", zap.Error(err))
		}
		if tunnel != nil {
			if err := tunnel.Close(); err != nil {
				logger.Default().Warn("close ssh tunnel", zap.Error(err))
			}
		}
		return nil, nil, fmt.Errorf("redis 连接失败: %w", pingErr)
	}
	logger.Ctx(ctx).Info("redis connected", logFields...)

	// 直连时 tunnel 为 *SSHTunnel 的 nil，直接返回会变成 typed-nil 接口，
	// 调用方 `if closer != nil` 会误判为真并在 Close() 里 nil deref panic。
	if tunnel == nil {
		return client, nil, nil
	}
	return client, tunnel, nil
}

// checkRedisConnected 确认客户端可用。集群客户端的 PING 发往随机 slot 的主节点，任一宣告的
// 主节点不可达就会随机失败；因此集群改为加载拓扑：go-redis 依次向种子请求 CLUSTER SLOTS，
// 有一个种子应答即成功（认证失败、未开启集群模式等回复错误照常返回）。
func checkRedisConnected(ctx context.Context, client redis.UniversalClient) error {
	if cluster, ok := client.(*redis.ClusterClient); ok {
		_, err := RedisClusterNodeAddrs(ctx, cluster, false)
		return err
	}
	return client.Ping(ctx).Err()
}

// newRedisClient 按部署模式创建客户端,不做网络 I/O。
func newRedisClient(cfg *asset_entity.RedisConfig, opts *redis.UniversalOptions) redis.UniversalClient {
	switch cfg.EffectiveMode() {
	case asset_entity.RedisModeCluster:
		return redis.NewClusterClient(opts.Cluster())
	case asset_entity.RedisModeSentinel:
		return redis.NewFailoverClient(opts.Failover())
	default:
		return redis.NewClient(opts.Simple())
	}
}

// redisSeedAddrs 返回配置中用于建立连接的地址:单机为 host:port,集群 / 哨兵为 Nodes。
func redisSeedAddrs(cfg *asset_entity.RedisConfig) []string {
	if cfg.EffectiveMode() == asset_entity.RedisModeStandalone {
		return []string{net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))}
	}
	return cfg.Nodes
}

// configureRedisTransport 按 代理链 > 隧道 > 代理 > 直连 设置 opts.Dialer,返回隧道(可为 nil)。
// 拨号一律使用 go-redis 请求的目标地址(集群发现的节点、哨兵返回的主节点),并先做地址映射;
// 直连且无映射时保留 go-redis 默认 dialer(哨兵模式除外:需记录拨号地址以发现主从切换)。
// go-redis 设置自定义 Dialer 后默认 dialer 的 TLS 逻辑被绕过,因此把 TLSConfig
// 移入 dialer 内手动包裹并清空 opts.TLSConfig,避免 TLS 静默失效。
func configureRedisTransport(opts *redis.UniversalOptions, asset *asset_entity.Asset, cfg *asset_entity.RedisConfig, sshPool *sshpool.Pool) (*SSHTunnel, error) {
	var tunnel *SSHTunnel
	var dial dialContextFunc
	tunnelID := asset.SSHTunnelID
	if tunnelID == 0 {
		tunnelID = cfg.SSHAssetID // backward compat
	}
	addrMap := cfg.NodeAddressMap
	if cfg.EffectiveMode() == asset_entity.RedisModeStandalone {
		addrMap = nil // 地址映射只属于集群 / 哨兵模式
	}
	switch {
	case cfg.ProxyChain != nil:
		var err error
		dial, err = chainDialFunc(context.Background(), cfg.ProxyChain)
		if err != nil {
			return nil, err
		}
	case tunnelID > 0 && sshPool != nil:
		tunnel = NewSSHTunnel(tunnelID, cfg.Host, cfg.Port, sshPool)
		dial = tunnelAddrDialFunc(tunnel)
	case cfg.Proxy != nil:
		dial = proxyDialFunc(cfg.Proxy)
	}
	sentinel := cfg.EffectiveMode() == asset_entity.RedisModeSentinel
	if dial == nil { // 直连(含代理链解析为空层)
		if len(addrMap) == 0 && !sentinel {
			return nil, nil
		}
		dial = directDialFunc()
	}
	dial = mappedDialFunc(dial, addrMap)
	if sentinel {
		dial = recordDialedAddr(dial) // 供 sentinelMasterWatcher 得知数据连接拨往的主节点
	}
	opts.Dialer = tlsWrappedDialFunc(dial, opts.TLSConfig)
	opts.TLSConfig = nil
	return tunnel, nil
}

func buildRedisOptions(cfg *asset_entity.RedisConfig, password string) (*redis.UniversalOptions, error) {
	opts := &redis.UniversalOptions{
		Username: cfg.Username,
		Password: password,
	}
	switch cfg.EffectiveMode() {
	case asset_entity.RedisModeStandalone:
		opts.Addrs = []string{fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)}
		opts.DB = cfg.Database
	case asset_entity.RedisModeCluster:
		opts.Addrs = cfg.Nodes
	case asset_entity.RedisModeSentinel:
		opts.Addrs = cfg.Nodes
		opts.DB = cfg.Database
		opts.MasterName = cfg.MasterName
		opts.SentinelUsername = cfg.SentinelUsername
		opts.SentinelPassword = cfg.SentinelPassword
	default:
		return nil, fmt.Errorf("不支持的 Redis 部署模式: %s", cfg.Mode)
	}
	if cfg.CommandTimeoutSeconds > 0 {
		timeout := time.Duration(cfg.CommandTimeoutSeconds) * time.Second
		opts.ReadTimeout = timeout
		opts.WriteTimeout = timeout
	}
	if cfg.TLS {
		tlsConfig, err := buildRedisTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		opts.TLSConfig = tlsConfig
	}
	return opts, nil
}

func buildRedisTLSConfig(cfg *asset_entity.RedisConfig) (*tls.Config, error) {
	return BuildTLSConfig("Redis", TLSFields{
		ServerName: cfg.TLSServerName,
		Insecure:   cfg.TLSInsecure,
		CAFile:     cfg.TLSCAFile,
		CertFile:   cfg.TLSCertFile,
		KeyFile:    cfg.TLSKeyFile,
	})
}

// dialedAddrKey 是 sentinelMasterWatcher 放进拨号 ctx 的地址记录槽(*string)。
type dialedAddrKey struct{}

// recordDialedAddr 把请求拨号的地址(映射前的宣告地址)写入 ctx 中的记录槽。
// go-redis 的 FailoverClient 在建立数据连接时,于同一 ctx 下先经哨兵解析主节点、再拨往主节点,
// 因此槽里最后一次写入的就是主节点地址。
func recordDialedAddr(dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		if slot, ok := ctx.Value(dialedAddrKey{}).(*string); ok {
			*slot = addr
		}
		return dial(ctx, addr)
	}
}

// sentinelMasterWatcher 是哨兵模式客户端的 go-redis hook:记录每条新数据连接拨往的主节点,
// 地址变化即哨兵报告了新主节点(主从切换),记一条结构化日志(不含密钥)。
type sentinelMasterWatcher struct {
	assetID    int64
	masterName string

	mu     sync.Mutex
	master string
}

func (w *sentinelMasterWatcher) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var dialed string
		conn, err := next(context.WithValue(ctx, dialedAddrKey{}, &dialed), network, addr)
		if err == nil && dialed != "" {
			w.observe(ctx, dialed)
		}
		return conn, err
	}
}

func (w *sentinelMasterWatcher) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (w *sentinelMasterWatcher) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (w *sentinelMasterWatcher) observe(ctx context.Context, master string) {
	w.mu.Lock()
	prev := w.master
	w.master = master
	w.mu.Unlock()
	if prev != "" && prev != master {
		logger.Ctx(ctx).Warn("redis sentinel master switched",
			zap.Int64("assetID", w.assetID),
			zap.String("masterName", w.masterName),
			zap.String("from", prev),
			zap.String("to", master))
	}
}
