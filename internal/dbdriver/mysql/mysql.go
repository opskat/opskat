// Package mysql 注册内置的 MySQL 驱动。
package mysql

import (
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

var descriptor = dbdriver.Descriptor{
	ID:          asset_entity.DriverMySQL,
	Name:        "MySQL",
	Icon:        "mysql",
	Order:       10,
	Aliases:     []string{},
	DefaultPort: 3306,
	Form: []dbdriver.FormTabFields{
		{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
			dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldUsername, dbdriver.FieldPassword, dbdriver.FieldDatabase,
		}},
		{Tab: dbdriver.TabTunnel, Fields: []dbdriver.FormField{dbdriver.FieldTunnel}},
		{Tab: dbdriver.TabTLS, Fields: []dbdriver.FormField{dbdriver.FieldTLS}},
		{Tab: dbdriver.TabAdvanced, Fields: []dbdriver.FormField{
			dbdriver.FieldParams, dbdriver.FieldReadOnly, dbdriver.FieldQueryTimeout,
		}},
	},
	Capabilities: []dbdriver.Capability{
		dbdriver.CapCreateDatabase, dbdriver.CapViewDDL,
		dbdriver.CapViews, dbdriver.CapProcedures, dbdriver.CapFunctions, dbdriver.CapTriggers,
		dbdriver.CapGridEdit, dbdriver.CapCreateTable, dbdriver.CapAlterTable, dbdriver.CapDropTable, dbdriver.CapTruncateTable,
		dbdriver.CapImport, dbdriver.CapExportSQL, dbdriver.CapCopySQL, dbdriver.CapReadOnly,
	},
	CanDisableForeignKeyChecks: true,
	CreateDatabaseOptions: []dbdriver.DatabaseOption{
		{Key: dbdriver.OptionCharset, Default: "utf8mb4"},
		{Key: dbdriver.OptionCollation, Default: "utf8mb4_0900_ai_ci"},
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
