package assettype

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/opskat/opskat/internal/dbdriver"
	// 数据库资产类型离不开驱动：链接它的二进制同时链接全部内置驱动。
	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

type databaseHandler struct{}

func init() {
	Register(&databaseHandler{})
	policy.RegisterDefaultPolicy("database", func() any { return asset_entity.DefaultQueryPolicy() })
}

func (h *databaseHandler) Type() string { return asset_entity.AssetTypeDatabase }

// DefaultPort 数据库资产没有类型级的默认端口：端口默认值在各驱动的描述里。
func (h *databaseHandler) DefaultPort() int { return 0 }

// driverArgs 把自动化入参交给驱动：取值一律经共享的 Arg* 助手。
type driverArgs map[string]any

func (a driverArgs) Has(key string) bool      { _, ok := a[key]; return ok }
func (a driverArgs) String(key string) string { return ArgString(a, key) }
func (a driverArgs) Int(key string) int       { return ArgInt(a, key) }

// requestedDriver 返回入参 driver 指定的驱动。
func requestedDriver(args map[string]any) (dbdriver.Driver, error) {
	id := ArgString(args, "driver")
	if id == "" {
		return nil, fmt.Errorf("database type requires driver parameter (%s)", strings.Join(dbdriver.IDs(), ", "))
	}
	return dbdriver.Lookup(asset_entity.DatabaseDriver(id))
}

// usesPassword 报告驱动是否用口令认证（见 dbdriver.FieldPassword）。
func usesPassword(d dbdriver.Driver) bool {
	return d.Descriptor().UsesField(dbdriver.FieldPassword)
}

func (h *databaseHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetDatabaseConfig()
	if err != nil || cfg == nil {
		return nil
	}
	view := map[string]any{
		"driver":                string(cfg.Driver),
		"database":              cfg.Database,
		"read_only":             cfg.ReadOnly,
		"query_timeout_seconds": cfg.QueryTimeoutSeconds,
	}
	drv, err := dbdriver.Lookup(cfg.Driver)
	if err != nil {
		// 驱动不可用时没有谁能说出它的连接字段是哪些：只给驱动无关的部分，其中的 driver
		// 已经点明这是哪个驱动。
		return view
	}
	maps.Copy(view, drv.ConnectionView(cfg))
	return view
}

func (h *databaseHandler) AuthenticationAssociation(a *asset_entity.Asset) (AuthenticationAssociation, bool, error) {
	cfg, err := a.GetDatabaseConfig()
	if err != nil || cfg == nil {
		return AuthenticationAssociation{}, false, err
	}
	return passwordAuthenticationAssociation(cfg.CredentialID)
}

func (h *databaseHandler) ResolvePassword(ctx context.Context, a *asset_entity.Asset) (string, error) {
	cfg, err := a.GetDatabaseConfig()
	if err != nil {
		return "", fmt.Errorf("get database config failed: %w", err)
	}
	return credential_resolver.Default().ResolvePasswordGeneric(ctx, cfg)
}

func (h *databaseHandler) DefaultPolicy() any { return asset_entity.DefaultQueryPolicy() }
func (h *databaseHandler) PolicyKind() string { return policy.PolicyKindQuery }

func (h *databaseHandler) AutomationUpdateContext(a *asset_entity.Asset, args map[string]any) (map[string]any, error) {
	if _, ok := args["driver"]; ok {
		return args, nil
	}
	if _, hasPassword := args["password"]; !hasPassword {
		if _, hasReference := args["credential_id"]; !hasReference {
			return args, nil
		}
	}
	current, err := a.GetDatabaseConfig()
	if err != nil {
		return nil, err
	}
	out := cloneArgs(args)
	out["driver"] = string(current.Driver)
	return out, nil
}

func (h *databaseHandler) ValidateCreateArgs(args map[string]any) error {
	drv, err := requestedDriver(args)
	if err != nil {
		return err
	}
	return drv.ValidateCreateArgs(driverArgs(args))
}

func (h *databaseHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	drv, err := requestedDriver(args)
	if err != nil {
		return err
	}
	cfg := &asset_entity.DatabaseConfig{
		Driver:              drv.Descriptor().ID,
		CredentialID:        ArgInt64(args, "credential_id"),
		Database:            ArgString(args, "database"),
		ReadOnly:            ArgBool(args, "read_only"),
		QueryTimeoutSeconds: ArgInt(args, "query_timeout_seconds"),
		SSHAssetID:          ArgInt64(args, "ssh_asset_id"),
	}
	drv.ApplyCreateArgs(cfg, driverArgs(args))
	if usesPassword(drv) {
		if password := ArgString(args, "password"); password != "" {
			encrypted, err := credential_svc.Default().Encrypt(password)
			if err != nil {
				return fmt.Errorf("encrypt database password: %w", err)
			}
			cfg.Password = encrypted
		}
	}
	if err := applyProxyChainArg(a, args, &cfg.ProxyChain, &cfg.SSHAssetID); err != nil {
		return err
	}
	return a.SetDatabaseConfig(cfg)
}

func (h *databaseHandler) ApplyUpdateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	cfg, err := a.GetDatabaseConfig()
	if err != nil || cfg == nil {
		return err
	}
	if v := ArgString(args, "driver"); v != "" {
		cfg.Driver = asset_entity.DatabaseDriver(v)
	}
	drv, err := dbdriver.Lookup(cfg.Driver)
	if err != nil {
		return err
	}
	if _, ok := args["database"]; ok {
		cfg.Database = ArgString(args, "database")
	}
	if _, ok := args["read_only"]; ok {
		cfg.ReadOnly = ArgBool(args, "read_only")
	}
	if v := ArgInt(args, "query_timeout_seconds"); v > 0 {
		cfg.QueryTimeoutSeconds = v
	}
	// ssh_asset_id 先于驱动写入：驱动可以据自己的规则再收回它（SQLite 的本地文件不留 SSH 资产）。
	if _, ok := args["ssh_asset_id"]; ok {
		cfg.SSHAssetID = ArgInt64(args, "ssh_asset_id")
	}
	drv.ApplyUpdateArgs(cfg, driverArgs(args))
	if usesPassword(drv) {
		if _, ok := args["credential_id"]; ok {
			cfg.CredentialID = ArgInt64(args, "credential_id")
			cfg.Password = ""
		}
		if password := ArgString(args, "password"); password != "" {
			encrypted, err := credential_svc.Default().Encrypt(password)
			if err != nil {
				return fmt.Errorf("encrypt database password: %w", err)
			}
			cfg.Password = encrypted
			cfg.CredentialID = 0
		}
	}
	if err := applyProxyChainArg(a, args, &cfg.ProxyChain, &cfg.SSHAssetID); err != nil {
		return err
	}
	return a.SetDatabaseConfig(cfg)
}
