package etcd_svc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/sshpool"
)

// commandTimeout 是 etcd 单次操作的默认超时,与 connpool 内部默认值保持一致。
const commandTimeout = 10 * time.Second

// Service 是 etcd 资产的服务层入口,负责:
//   - 资产查找与类型校验
//   - 凭证解密
//   - 连接复用 / 即时拨号
//   - 命令分发 (Dispatch)
//   - 关键流程三态日志
//
// 策略检查与审计在调用方(AI Runner / IPC 绑定层)完成,Service 本身不感知策略。
type Service struct {
	sshPool *sshpool.Pool
}

// New 创建 etcd 服务实例。
func New(sshPool *sshpool.Pool) *Service {
	return &Service{sshPool: sshPool}
}

// Exec 执行一次 etcd 操作 (get/put/del/lease/...)。
func (s *Service) Exec(ctx context.Context, req *ExecRequest) (*ExecResult, error) {
	start := time.Now()
	logger.Ctx(ctx).Info("etcd exec start",
		zap.Int64("assetID", req.AssetID),
		zap.String("op", req.Op),
		zap.String("key", req.Key),
		zap.Bool("prefix", req.Prefix),
		zap.String("source", req.Source),
	)

	client, cfg, err := s.connect(ctx, req.AssetID)
	if err != nil {
		logger.Ctx(ctx).Error("etcd exec failed",
			zap.Int64("assetID", req.AssetID),
			zap.String("op", req.Op),
			zap.Error(err),
		)
		return nil, err
	}

	execCtx, cancel := s.commandCtx(ctx, cfg)
	defer cancel()

	result, err := Dispatch(execCtx, client, req)
	elapsed := time.Since(start)
	if err != nil {
		logger.Ctx(ctx).Error("etcd exec failed",
			zap.Int64("assetID", req.AssetID),
			zap.String("op", req.Op),
			zap.Duration("elapsed", elapsed),
			zap.Error(err),
		)
		return nil, err
	}

	logger.Ctx(ctx).Info("etcd exec end",
		zap.Int64("assetID", req.AssetID),
		zap.String("op", req.Op),
		zap.Duration("elapsed", elapsed),
		zap.Int64("count", result.Count),
	)
	return result, nil
}

// ListPrefixRequest 是 KV 树懒加载的入参。
type ListPrefixRequest struct {
	AssetID int64
	Prefix  string
	Delim   string // 默认 "/"
	Limit   int64  // 默认 1000
}

// ListPrefixResult 是 KV 树懒加载的返回。
type ListPrefixResult struct {
	Dirs      []string `json:"dirs"`
	Leaves    []EtcdKV `json:"leaves"`
	Truncated bool     `json:"truncated"`
}

// ListPrefix 按指定 prefix + 分隔符切分出当前层级的子目录与叶子,
// 用于 KV 树的懒加载。返回的叶子不带 value (KeysOnly)。
func (s *Service) ListPrefix(ctx context.Context, req *ListPrefixRequest) (*ListPrefixResult, error) {
	if req.Delim == "" {
		req.Delim = "/"
	}
	if req.Limit == 0 {
		req.Limit = 1000
	}

	client, cfg, err := s.connect(ctx, req.AssetID)
	if err != nil {
		return nil, err
	}
	listCtx, cancel := s.commandCtx(ctx, cfg)
	defer cancel()

	resp, err := client.Get(listCtx, req.Prefix,
		clientv3.WithPrefix(),
		clientv3.WithKeysOnly(),
		clientv3.WithLimit(req.Limit),
	)
	if err != nil {
		return nil, fmt.Errorf("etcd list prefix failed: %w", err)
	}

	dirSet := map[string]struct{}{}
	res := &ListPrefixResult{Truncated: resp.More}
	for _, k := range resp.Kvs {
		key := string(k.Key)
		rest := strings.TrimPrefix(key, req.Prefix)
		idx := strings.Index(rest, req.Delim)
		if idx < 0 {
			res.Leaves = append(res.Leaves, EtcdKV{
				Key:            key,
				ModRevision:    k.ModRevision,
				CreateRevision: k.CreateRevision,
				Version:        k.Version,
				Lease:          k.Lease,
			})
			continue
		}
		dir := rest[:idx]
		if _, ok := dirSet[dir]; !ok {
			dirSet[dir] = struct{}{}
			res.Dirs = append(res.Dirs, dir)
		}
	}
	return res, nil
}

// TestConnection 即时拨号一次,验证配置可达。不进缓存,完成后立即关闭。
func (s *Service) TestConnection(ctx context.Context, assetID int64) error {
	asset, cfg, password, err := s.lookup(ctx, assetID)
	if err != nil {
		return err
	}
	return s.testDial(ctx, asset, cfg, password)
}

// TestConfig 用「未保存的配置」即时拨号一次,用于资产表单上的「测试连接」。
// 与 TestConnection 的区别:不通过 assetID 查找资产,而是接受调用方拼好的 cfg + 已解密的密码。
func (s *Service) TestConfig(ctx context.Context, cfg *asset_entity.EtcdConfig, password string) error {
	if cfg == nil {
		return fmt.Errorf("etcd config 为空")
	}
	if len(cfg.Endpoints) == 0 {
		return fmt.Errorf("至少需要 1 个 endpoint")
	}
	return s.testDial(ctx, &asset_entity.Asset{Type: asset_entity.AssetTypeEtcd, SSHTunnelID: cfg.SSHAssetID}, cfg, password)
}

// testDial 共用拨号 + 关闭逻辑。DialEtcd 失败时直接返回错误;成功后关闭 client / tunnel。
func (s *Service) testDial(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.EtcdConfig, password string) error {
	logger.Ctx(ctx).Info("etcd test connection start",
		zap.Int64("assetID", asset.ID),
		zap.Int("endpoints", len(cfg.Endpoints)),
		zap.Bool("tls", cfg.TLS),
		zap.Int64("sshTunnelID", asset.SSHTunnelID),
	)
	client, tunnel, err := connpool.DialEtcd(ctx, asset, cfg, password, s.sshPool)
	if err != nil {
		logger.Ctx(ctx).Error("etcd test connection failed",
			zap.Int64("assetID", asset.ID),
			zap.Error(err),
		)
		return err
	}
	if cerr := client.Close(); cerr != nil {
		logger.Ctx(ctx).Warn("close etcd client after test", zap.Error(cerr))
	}
	if tunnel != nil {
		if cerr := tunnel.Close(); cerr != nil {
			logger.Ctx(ctx).Warn("close etcd ssh tunnel after test", zap.Error(cerr))
		}
	}
	logger.Ctx(ctx).Info("etcd test connection ok",
		zap.Int64("assetID", asset.ID),
	)
	return nil
}

// --- 内部辅助 ---

// connect 查找资产,解密凭证,获取(可能缓存的) client。
func (s *Service) connect(ctx context.Context, assetID int64) (*clientv3.Client, *asset_entity.EtcdConfig, error) {
	asset, cfg, password, err := s.lookup(ctx, assetID)
	if err != nil {
		return nil, nil, err
	}
	client, err := connpool.GetOrDialEtcd(ctx, asset, cfg, password, s.sshPool)
	if err != nil {
		return nil, nil, err
	}
	return client, cfg, nil
}

func (s *Service) lookup(ctx context.Context, assetID int64) (*asset_entity.Asset, *asset_entity.EtcdConfig, string, error) {
	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsEtcd() {
		return nil, nil, "", fmt.Errorf("资产不是 etcd 类型")
	}
	cfg, err := asset.GetEtcdConfig()
	if err != nil {
		return nil, nil, "", fmt.Errorf("获取 etcd 配置失败: %w", err)
	}
	password, err := credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("解析 etcd 凭据失败: %w", err)
	}
	return asset, cfg, password, nil
}

func (s *Service) commandCtx(ctx context.Context, cfg *asset_entity.EtcdConfig) (context.Context, context.CancelFunc) {
	timeout := commandTimeout
	if cfg.CommandTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.CommandTimeoutSeconds) * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}
