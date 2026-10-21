package assettype

import (
	"context"
	"fmt"
	"maps"
	"strings"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	// 数据库资产类型离不开驱动：链接它的二进制同时链接全部内置驱动。
	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
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
		return nil, fmt.Errorf("database type requires driver parameter (%s)", strings.Join(dbregistry.IDs(), ", "))
	}
	return dbregistry.Lookup(asset_entity.DatabaseDriver(id))
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
	drv, err := dbregistry.Lookup(cfg.Driver)
	if err != nil {
		// 驱动不可用时没有谁能说出它的连接字段是哪些：只给驱动无关的部分，其中的 driver
		// 已经点明这是哪个驱动。
		return view
	}
	maps.Copy(view, drv.ConnectionView(cfg.DriverConfig()))
	if len(cfg.DriverOptions) > 0 {
		values, secretsSet := viewDriverOptions(drv.Descriptor(), cfg.DriverOptions)
		view[argDriverOptions] = values
		if len(secretsSet) > 0 {
			view["driver_options_secrets_set"] = secretsSet
		}
	}
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
	if err := h.checkUpdateDriverOptions(a, args); err != nil {
		return nil, err
	}
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

// checkUpdateDriverOptions 在审批前按驱动的声明校验更新后的 driver_options：入参叠加到已存取值上
// （切换驱动时从头取值），必填与默认值按合并结果查，与落库时一致。既没带 driver_options 也没
// 切换驱动时无事可查。
func (h *databaseHandler) checkUpdateDriverOptions(a *asset_entity.Asset, args map[string]any) error {
	_, given := args[argDriverOptions]
	if !given && ArgString(args, "driver") == "" {
		return nil
	}
	cfg, err := a.GetDatabaseConfig()
	if err != nil {
		return err
	}
	switched := switchesDriver(cfg, args)
	if !given && !switched {
		return nil
	}
	id := cfg.Driver
	if switched {
		id = asset_entity.DatabaseDriver(ArgString(args, "driver"))
	}
	drv, err := dbregistry.Lookup(id)
	if err != nil {
		return err
	}
	opts, err := driverOptionsArg(args)
	if err != nil {
		return err
	}
	// 与落库（ApplyUpdateArgs）同一条合并规则：用户点了同意之后不该再因必填缺失而失败。
	stored := cfg.DriverOptions
	if switched {
		stored = nil
	}
	_, err = mergeDriverOptions(drv.Descriptor(), stored, opts)
	return err
}

// switchesDriver 报告更新入参是否把资产换到另一个驱动（带了与已存的不同的 driver）。
func switchesDriver(cfg *asset_entity.DatabaseConfig, args map[string]any) bool {
	v := ArgString(args, "driver")
	return v != "" && asset_entity.DatabaseDriver(v) != cfg.Driver
}

func (h *databaseHandler) ValidateCreateArgs(args map[string]any) error {
	drv, err := requestedDriver(args)
	if err != nil {
		return err
	}
	if err := drv.ValidateCreateArgs(driverArgs(args)); err != nil {
		return err
	}
	_, err = validateCreateDriverOptions(drv.Descriptor(), args)
	return err
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
	driverCfg := cfg.DriverConfig()
	drv.ApplyCreateArgs(driverCfg, driverArgs(args))
	cfg.SetDriverConfig(driverCfg)
	opts, err := validateCreateDriverOptions(drv.Descriptor(), args)
	if err != nil {
		return err
	}
	if cfg.DriverOptions, err = encryptDriverOptions(drv.Descriptor(), opts); err != nil {
		return err
	}
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
	switched := switchesDriver(cfg, args)
	if switched {
		cfg.Driver = asset_entity.DatabaseDriver(ArgString(args, "driver"))
		// 专属字段属于驱动：旧驱动的取值不带进新驱动的配置，新驱动的取值按它自己的声明补齐。
		cfg.DriverOptions = nil
	}
	drv, err := dbregistry.Lookup(cfg.Driver)
	if err != nil {
		// 驱动不可用的资产仍可编辑：不带任何连接配置的更新（改名、描述、分组）没有东西要
		// 合并，配置原样保留。带了连接配置就没有驱动能解释它，报「驱动不可用」。
		if len(args) == 0 {
			return nil
		}
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
	driverCfg := cfg.DriverConfig()
	drv.ApplyUpdateArgs(driverCfg, driverArgs(args))
	cfg.SetDriverConfig(driverCfg)
	if updates, err := driverOptionsArg(args); err != nil {
		return err
	} else if updates != nil || switched {
		if cfg.DriverOptions, err = mergeDriverOptions(drv.Descriptor(), cfg.DriverOptions, updates); err != nil {
			return err
		}
	}
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
