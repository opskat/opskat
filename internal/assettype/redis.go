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

func (h *redisHandler) ValidateCreateArgs(args map[string]any) error {
	switch ArgString(args, "mode") {
	case asset_entity.RedisModeCluster, asset_entity.RedisModeSentinel:
		if len(ArgStringSlice(args, "nodes")) == 0 {
			return fmt.Errorf("missing required parameter: nodes")
		}
		return nil
	default:
		return validateRemoteServerArgs(args)
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
	if password := ArgString(args, "password"); password != "" {
		encrypted, err := credential_svc.Default().Encrypt(password)
		if err != nil {
			return fmt.Errorf("encrypt Redis password: %w", err)
		}
		cfg.Password = encrypted
		cfg.CredentialID = 0
	}
	if v := ArgString(args, "mode"); v != "" {
		cfg.Mode = v
	}
	if v := ArgStringSlice(args, "nodes"); len(v) > 0 {
		cfg.Nodes = v
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
