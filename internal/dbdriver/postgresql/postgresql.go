// Package postgresql 注册内置的 PostgreSQL 驱动。
package postgresql

import (
	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

var descriptor = dbdriver.Descriptor{
	ID:          asset_entity.DriverPostgreSQL,
	Name:        "PostgreSQL",
	Icon:        "postgresql",
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
}

type driver struct {
	dbdriver.NetworkRules
}

func init() {
	dbdriver.Register(driver{NetworkRules: dbdriver.NetworkRules{DefaultPort: descriptor.DefaultPort}})
}

func (driver) Descriptor() dbdriver.Descriptor { return descriptor }
