package assettype

import (
	"context"
	"fmt"

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

func (h *databaseHandler) Type() string     { return asset_entity.AssetTypeDatabase }
func (h *databaseHandler) DefaultPort() int { return 3306 }

func (h *databaseHandler) SafeView(a *asset_entity.Asset) map[string]any {
	cfg, err := a.GetDatabaseConfig()
	if err != nil || cfg == nil {
		return nil
	}
	if cfg.Driver == asset_entity.DriverSQLite {
		return map[string]any{
			"driver":                string(cfg.Driver),
			"sqlite_source":         cfg.SQLiteSource,
			"path":                  cfg.Path,
			"database":              cfg.Database,
			"read_only":             cfg.ReadOnly,
			"query_timeout_seconds": cfg.QueryTimeoutSeconds,
		}
	}
	return map[string]any{
		"host":                  cfg.Host,
		"port":                  cfg.Port,
		"username":              cfg.Username,
		"driver":                string(cfg.Driver),
		"database":              cfg.Database,
		"read_only":             cfg.ReadOnly,
		"query_timeout_seconds": cfg.QueryTimeoutSeconds,
	}
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
	driver := asset_entity.DatabaseDriver(ArgString(args, "driver"))
	if driver == "" {
		return fmt.Errorf("database type requires driver parameter (mysql, postgresql, mssql, sqlite)")
	}
	if driver == asset_entity.DriverSQLite {
		if ArgString(args, "path") == "" {
			return fmt.Errorf("missing required parameter: path for SQLite")
		}
		return nil
	}
	return validateRemoteServerArgs(args)
}

func (h *databaseHandler) ApplyCreateArgs(_ context.Context, a *asset_entity.Asset, args map[string]any) error {
	driver := ArgString(args, "driver")
	if driver == "" {
		return fmt.Errorf("database type requires driver parameter (mysql, postgresql, mssql, sqlite)")
	}
	cfg := &asset_entity.DatabaseConfig{
		Driver:              asset_entity.DatabaseDriver(driver),
		CredentialID:        ArgInt64(args, "credential_id"),
		Database:            ArgString(args, "database"),
		ReadOnly:            ArgBool(args, "read_only"),
		QueryTimeoutSeconds: ArgInt(args, "query_timeout_seconds"),
	}
	if cfg.Driver == asset_entity.DriverSQLite {
		cfg.Path = ArgString(args, "path")
		if source := ArgString(args, "sqlite_source"); source != "" {
			cfg.SQLiteSource = asset_entity.SQLiteSource(source)
		}
		if cfg.SQLiteSource == "" {
			cfg.SQLiteSource = asset_entity.SQLiteSourceLocal
		}
		cfg.SSHAssetID = ArgInt64(args, "ssh_asset_id")
	} else {
		cfg.Host = ArgString(args, "host")
		cfg.Port = ArgInt(args, "port")
		if cfg.Port == 0 {
			cfg.Port = cfg.Driver.DefaultPort()
		}
		cfg.Username = ArgString(args, "username")
		cfg.SSHAssetID = ArgInt64(args, "ssh_asset_id")
		if password := ArgString(args, "password"); password != "" {
			encrypted, err := credential_svc.Default().Encrypt(password)
			if err != nil {
				return fmt.Errorf("encrypt database password: %w", err)
			}
			cfg.Password = encrypted
		}
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
	if _, ok := args["database"]; ok {
		cfg.Database = ArgString(args, "database")
	}
	if v := ArgString(args, "read_only"); v != "" {
		cfg.ReadOnly = v == "true"
	}
	if v := ArgInt(args, "query_timeout_seconds"); v > 0 {
		cfg.QueryTimeoutSeconds = v
	}

	if cfg.Driver == asset_entity.DriverSQLite {
		if _, ok := args["path"]; ok {
			cfg.Path = ArgString(args, "path")
		}
		if _, ok := args["sqlite_source"]; ok {
			cfg.SQLiteSource = asset_entity.SQLiteSource(ArgString(args, "sqlite_source"))
		}
		if _, ok := args["ssh_asset_id"]; ok {
			cfg.SSHAssetID = ArgInt64(args, "ssh_asset_id")
		}
		if cfg.SQLiteSource != asset_entity.SQLiteSourceRemoteSSHVFS {
			cfg.SSHAssetID = 0
		}
		// SQLite 没有 host/port/user/pass
	} else {
		if v := ArgString(args, "host"); v != "" {
			cfg.Host = v
		}
		if v := ArgInt(args, "port"); v > 0 {
			cfg.Port = v
		}
		if v := ArgString(args, "username"); v != "" {
			cfg.Username = v
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
				return fmt.Errorf("encrypt database password: %w", err)
			}
			cfg.Password = encrypted
			cfg.CredentialID = 0
		}
	}
	return a.SetDatabaseConfig(cfg)
}
