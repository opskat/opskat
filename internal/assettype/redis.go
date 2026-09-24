package assettype

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type redisHandler struct{}

func init() {
	Register(&redisHandler{})
	policy.RegisterDefaultPolicy("redis", func() any { return asset_entity.DefaultRedisPolicy() })
}

func (h *redisHandler) Type() string     { return asset_entity.AssetTypeRedis }
func (h *redisHandler) DefaultPort() int { return 6379 }

func (h *redisHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetRedisConfig()
	if err != nil || cfg == nil {
		return nil
	}
	view := map[string]any{
		"host": cfg.Host, "port": cfg.Port,
		"username": cfg.Username, "redis_db": cfg.Database,
	}
	// 单机资产的安全视图与引入部署模式前相同；集群 / 哨兵额外显示 mode / nodes / master_name。
	if mode := cfg.EffectiveMode(); mode != asset_entity.RedisModeStandalone {
		view["mode"] = mode
		view["nodes"] = cfg.Nodes
		if cfg.MasterName != "" {
			view["master_name"] = cfg.MasterName
		}
	}
	return view
}

func (h *redisHandler) AuthenticationAssociation(a *asset_entity.Asset) (AuthenticationAssociation, bool, error) {
	cfg, err := a.GetRedisConfig()
	if err != nil || cfg == nil {
		return AuthenticationAssociation{}, false, err
	}
	return passwordAuthenticationAssociation(cfg.CredentialID)
}

func (h *redisHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetRedisConfig()
	if err != nil {
		return "", fmt.Errorf("get Redis config failed: %w", err)
	}
	return credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
}

// ValidateCreateArgs 按部署模式做审批前校验。单机模式与引入部署模式前完全相同
// (validateRemoteServerArgs,报出 host/port/username);集群/哨兵/未知 mode 复用实体层
// RedisConfig.ValidateMode(与桌面表单保存时、commit 时 validateRedis 同一份规则),缺
// master_name、映射行非法、节点缺端口、未知 mode 这些字段级错误在审批前就报出。
func (h *redisHandler) ValidateCreateArgs(args map[string]any) error {
	cfg := redisModeConfigFromArgs(args)
	if cfg.EffectiveMode() == asset_entity.RedisModeStandalone {
		return validateRemoteServerArgs(args)
	}
	return cfg.ValidateMode()
}

// AutomationUpdateContext 是 update 的审批前校验:把本次部分更新按 ApplyUpdateArgs 的同一
// 规则叠加到已存储的配置上(含切换模式时清掉另一模式的 nodes、只保留当前模式字段),再用
// ValidateMode 校验合并结果——与 create 一样在审批前报出具体字段,而不是审批通过后才在
// commit 的 validateRedis 里失败。args 原样返回。
func (h *redisHandler) AutomationUpdateContext(a *asset_entity.Asset, args map[string]any) (map[string]any, error) {
	cfg, err := a.GetRedisConfig()
	if err != nil {
		return nil, err
	}
	applyRedisUpdateFields(cfg, args)
	cfg.KeepModeFieldsOnly()
	if err := cfg.ValidateMode(); err != nil {
		return nil, err
	}
	return args, nil
}

// redisModeConfigFromArgs 从审批前的原始 create 参数里挑出集群/哨兵校验用到的字段,构造
// 一个未加密、未落库的 RedisConfig 供 ValidateMode 复用;不写密钥字段。
func redisModeConfigFromArgs(args map[string]any) *asset_entity.RedisConfig {
	return &asset_entity.RedisConfig{
		Database:       ArgInt(args, "redis_db"),
		Mode:           ArgString(args, "mode"),
		Nodes:          ArgStringSlice(args, "nodes"),
		MasterName:     ArgString(args, "master_name"),
		NodeAddressMap: ArgStringMap(args, "node_address_map"),
	}
}

func (h *redisHandler) DefaultPolicy() any { return asset_entity.DefaultRedisPolicy() }
func (h *redisHandler) PolicyKind() string { return policy.PolicyKindRedis }

func (h *redisHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg := &asset_entity.RedisConfig{
		Host:             ArgString(args, "host"),
		Port:             ArgInt(args, "port"),
		Username:         ArgString(args, "username"),
		CredentialID:     ArgInt64(args, "credential_id"),
		Database:         ArgInt(args, "redis_db"),
		SSHAssetID:       ArgInt64(args, "ssh_asset_id"),
		Mode:             ArgString(args, "mode"),
		Nodes:            ArgStringSlice(args, "nodes"),
		MasterName:       ArgString(args, "master_name"),
		SentinelUsername: ArgString(args, "sentinel_username"),
		NodeAddressMap:   ArgStringMap(args, "node_address_map"),
	}
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt Redis password: %w", err)
		}
		cfg.Password = encrypted
	}
	if sentinelPassword := ArgString(args, "sentinel_password"); sentinelPassword != "" {
		encrypted, err := credential_svc.Default().Encrypt(sentinelPassword)
		if err != nil {
			return fmt.Errorf("encrypt Redis sentinel password: %w", err)
		}
		cfg.SentinelPassword = encrypted
	}
	cfg.KeepModeFieldsOnly()
	return a.SetRedisConfig(cfg)
}

func (h *redisHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetRedisConfig()
	if err != nil || cfg == nil {
		return err
	}
	applyRedisUpdateFields(cfg, args)
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt Redis password: %w", err)
		}
		cfg.Password = encrypted
		cfg.CredentialID = 0
	}
	if sentinelPassword := ArgString(args, "sentinel_password"); sentinelPassword != "" {
		encrypted, err := credential_svc.Default().Encrypt(sentinelPassword)
		if err != nil {
			return fmt.Errorf("encrypt Redis sentinel password: %w", err)
		}
		cfg.SentinelPassword = encrypted
	}
	cfg.KeepModeFieldsOnly()
	return a.SetRedisConfig(cfg)
}

// applyRedisUpdateFields 把部分更新中的非密钥字段叠加到 cfg 上(只改传入的字段);密钥加密
// 留给 ApplyUpdateArgs。审批前校验(AutomationUpdateContext)与 commit 共用这一份合并规则。
func applyRedisUpdateFields(cfg *asset_entity.RedisConfig, args map[string]any) {
	if v := ArgString(args, "host"); v != "" {
		cfg.Host = v
	}
	if v := ArgInt(args, "port"); v > 0 {
		cfg.Port = v
	}
	if v := ArgString(args, "username"); v != "" {
		cfg.Username = v
	}
	if _, ok := args["redis_db"]; ok {
		cfg.Database = ArgInt(args, "redis_db")
	}
	if _, ok := args["ssh_asset_id"]; ok {
		cfg.SSHAssetID = ArgInt64(args, "ssh_asset_id")
	}
	if _, ok := args["credential_id"]; ok {
		cfg.CredentialID = ArgInt64(args, "credential_id")
		cfg.Password = ""
	}
	prevMode := cfg.EffectiveMode()
	if v := ArgString(args, "mode"); v != "" {
		cfg.Mode = v
	}
	if v := ArgStringSlice(args, "nodes"); len(v) > 0 {
		cfg.Nodes = v
	} else if cfg.EffectiveMode() != prevMode {
		cfg.Nodes = nil // 集群种子节点与哨兵节点含义不同，切换模式须重新给出 nodes
	}
	if v := ArgString(args, "master_name"); v != "" {
		cfg.MasterName = v
	}
	if v := ArgString(args, "sentinel_username"); v != "" {
		cfg.SentinelUsername = v
	}
	if v := ArgStringMap(args, "node_address_map"); len(v) > 0 {
		cfg.NodeAddressMap = v
	}
}
