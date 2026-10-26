// Package mssql 注册内置的 SQL Server 驱动。
package mssql

import (
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

var descriptor = dbdriver.Descriptor{
	ID:          asset_entity.DriverMSSQL,
	Name:        "SQL Server",
	Icon:        "sqlserver",
	Order:       30,
	Aliases:     []string{"sqlserver"},
	DefaultPort: 1433,
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
		dbdriver.CapSchemas, dbdriver.CapCreateDatabase, dbdriver.CapViewDDL,
		dbdriver.CapViews, dbdriver.CapProcedures, dbdriver.CapFunctions, dbdriver.CapTriggers,
		dbdriver.CapGridEdit, dbdriver.CapCreateTable, dbdriver.CapAlterTable, dbdriver.CapDropTable, dbdriver.CapTruncateTable,
		dbdriver.CapImport, dbdriver.CapExportSQL, dbdriver.CapCopySQL,
		// 连接级只读对 SQL Server 目前不生效（连接时只记一条日志），但表单上的只读开关
		// 一直对所有驱动显示；这一轮保持表单不变，所以仍然声明。
		dbdriver.CapReadOnly,
	},
	CanDisableForeignKeyChecks: true,
	// SQL Server 没有 CHARACTER SET，只有排序规则；默认空（用服务器默认）。
	CreateDatabaseOptions: []dbdriver.DatabaseOption{{Key: dbdriver.OptionCollation}},
	AlterTableAttributes:  alterAttributes,
}

type driver struct {
	dbdriver.NetworkRules
}

func init() {
	dbregistry.Register(driver{NetworkRules: dbdriver.NetworkRules{DefaultPort: descriptor.DefaultPort}})
}

func (driver) Descriptor() dbdriver.Descriptor { return descriptor }
