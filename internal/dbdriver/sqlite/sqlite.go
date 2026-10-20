// Package sqlite 注册内置的 SQLite 驱动。
package sqlite

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

var descriptor = dbdriver.Descriptor{
	ID:          asset_entity.DriverSQLite,
	Name:        "SQLite",
	Icon:        "sqlite",
	Order:       40,
	Aliases:     []string{"sqlite3"},
	DefaultPort: 0,
	Form: []dbdriver.FormTabFields{
		{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
			dbdriver.FieldSQLiteSource, dbdriver.FieldPath, dbdriver.FieldSSHAsset,
		}},
		{Tab: dbdriver.TabTunnel, Fields: []dbdriver.FormField{dbdriver.FieldTunnel}},
		{Tab: dbdriver.TabAdvanced, Fields: []dbdriver.FormField{
			dbdriver.FieldParams, dbdriver.FieldReadOnly, dbdriver.FieldQueryTimeout,
		}},
	},
	// 没有 schema 分组、建库，也没有存储过程与函数。
	Capabilities: []dbdriver.Capability{
		dbdriver.CapViewDDL,
		dbdriver.CapViews, dbdriver.CapTriggers,
		dbdriver.CapGridEdit, dbdriver.CapCreateTable, dbdriver.CapAlterTable, dbdriver.CapDropTable, dbdriver.CapTruncateTable,
		dbdriver.CapImport, dbdriver.CapExportSQL, dbdriver.CapCopySQL, dbdriver.CapReadOnly,
	},
	CanDisableForeignKeyChecks: true,
	// 没有主键的表按 rowid 定位单行。
	LocatesRowWithoutPrimaryKey: true,
	AlterTableAttributes:        alterAttributes,
}

// 文件来源（Config.SQLiteSource）的取值，与资产实体的定义同源。
const (
	sourceLocal     = string(asset_entity.SQLiteSourceLocal)
	sourceRemoteSSH = string(asset_entity.SQLiteSourceRemoteSSHVFS)
)

type driver struct{}

func init() {
	dbregistry.Register(driver{})
}

func (driver) Descriptor() dbdriver.Descriptor { return descriptor }

func (driver) ValidateConfig(cfg *dbdriver.Config, route dbdriver.Route) error {
	if cfg.Path == "" {
		return errors.New("SQLite 必须指定 path")
	}
	source := cfg.SQLiteSource
	if source == "" {
		source = sourceLocal
	}
	switch source {
	case sourceLocal:
		if !filepath.IsAbs(cfg.Path) && !path.IsAbs(cfg.Path) {
			return errors.New("SQLite path 必须为绝对路径")
		}
		if route.SSHTunnelID > 0 || cfg.SSHAssetID > 0 {
			return errors.New("SQLite 本地文件不支持 SSH 隧道")
		}
	case sourceRemoteSSH:
		if !path.IsAbs(cfg.Path) {
			return errors.New("SQLite path 必须为绝对路径")
		}
		if route.SSHTunnelID == 0 && cfg.SSHAssetID == 0 {
			return errors.New("远端 SQLite 必须指定 SSH 资产")
		}
	default:
		return fmt.Errorf("不支持的 SQLite source: %s", source)
	}
	if route.Proxy {
		return errors.New("SQLite 不支持代理")
	}
	if route.ProxyChain {
		return errors.New("SQLite 本地文件不支持代理链")
	}
	return nil
}

func (driver) CreateDefaults(args dbdriver.Args) map[string]any {
	if args.String("sqlite_source") != "" {
		return nil
	}
	return map[string]any{"sqlite_source": sourceLocal}
}

func (driver) ValidateCreateArgs(args dbdriver.Args) error {
	if args.String("path") == "" {
		return errors.New("missing required parameter: path for SQLite")
	}
	return nil
}

func (driver) ApplyCreateArgs(cfg *dbdriver.Config, args dbdriver.Args) {
	cfg.Path = args.String("path")
	cfg.SQLiteSource = args.String("sqlite_source")
	if cfg.SQLiteSource == "" {
		cfg.SQLiteSource = sourceLocal
	}
}

func (driver) ApplyUpdateArgs(cfg *dbdriver.Config, args dbdriver.Args) {
	if args.Has("path") {
		cfg.Path = args.String("path")
	}
	if args.Has("sqlite_source") {
		cfg.SQLiteSource = args.String("sqlite_source")
	}
	// SSH 资产只对远端文件有意义：来源不是远端时不留它，否则本地文件会被校验拒绝。
	if cfg.SQLiteSource != sourceRemoteSSH {
		cfg.SSHAssetID = 0
	}
}

func (driver) ConnectionView(cfg *dbdriver.Config) map[string]any {
	return map[string]any{
		"sqlite_source": cfg.SQLiteSource,
		"path":          cfg.Path,
	}
}
