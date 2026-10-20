// Package postgresql 注册内置的 PostgreSQL 驱动。
package postgresql

import (
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

var descriptor = dbdriver.Descriptor{
	ID:          asset_entity.DriverPostgreSQL,
	Name:        "PostgreSQL",
	Icon:        "postgresql",
	Order:       20,
	Aliases:     []string{"postgres"},
	DefaultPort: 5432,
	Form: []dbdriver.FormTabFields{
		{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
			dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldUsername, dbdriver.FieldPassword, dbdriver.FieldDatabase,
		}},
		{Tab: dbdriver.TabTunnel, Fields: []dbdriver.FormField{dbdriver.FieldTunnel}},
		{Tab: dbdriver.TabTLS, Fields: []dbdriver.FormField{dbdriver.FieldSSLMode}},
		{Tab: dbdriver.TabAdvanced, Fields: []dbdriver.FormField{
			dbdriver.FieldParams, dbdriver.FieldReadOnly, dbdriver.FieldQueryTimeout,
		}},
	},
	Capabilities: []dbdriver.Capability{
		dbdriver.CapSchemas, dbdriver.CapCreateDatabase, dbdriver.CapViewDDL,
		dbdriver.CapViews, dbdriver.CapProcedures, dbdriver.CapFunctions, dbdriver.CapTriggers,
		dbdriver.CapGridEdit, dbdriver.CapCreateTable, dbdriver.CapAlterTable, dbdriver.CapDropTable, dbdriver.CapTruncateTable,
		dbdriver.CapImport, dbdriver.CapExportSQL, dbdriver.CapCopySQL, dbdriver.CapReadOnly,
	},
	// 没有主键的表按 (tableoid, ctid) 定位单行。
	LocatesRowWithoutPrimaryKey: true,
	// 排序规则默认留空：不写 LC_COLLATE / LC_CTYPE，沿用模板库的（写死的值与模板库的拼写不一致时
	// CREATE DATABASE 报 22023）。
	CreateDatabaseOptions: []dbdriver.DatabaseOption{
		{Key: dbdriver.OptionCharset, Default: "UTF8"},
		{Key: dbdriver.OptionCollation},
	},
	AlterTableAttributes: alterAttributes,
}

type driver struct {
	dbdriver.NetworkRules
}

func init() {
	dbregistry.Register(driver{NetworkRules: dbdriver.NetworkRules{DefaultPort: descriptor.DefaultPort}})
}

func (driver) Descriptor() dbdriver.Descriptor { return descriptor }
