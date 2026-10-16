package dbdriver

import (
	"slices"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// Descriptor 是一个驱动的自我描述，原样经绑定交给前端。它只有可序列化的普通数据。
type Descriptor struct {
	// ID 是存进 DatabaseConfig.Driver 的值，改名即数据迁移。
	ID asset_entity.DatabaseDriver `json:"id"`
	// Name 是展示名（专有名词，不翻译）。
	Name string `json:"name"`
	// Icon 是资产图标名。
	Icon string `json:"icon"`
	// Order 是驱动自己声明的展示顺序，小的在前：驱动下拉、报错与帮助里的驱动列表都按它排，
	// 相同时按 ID。内置驱动占 10 / 20 / 30 / 40，中间留了空位。
	Order int `json:"order"`
	// Aliases 是 ID 之外可用于类型断言的名字（--type postgres）；ID 本身始终可用。
	Aliases []string `json:"aliases"`
	// DefaultPort 是默认端口；不走网络端口的驱动为 0。
	DefaultPort int `json:"defaultPort"`
	// Form 是驱动在资产表单各 Tab 中使用的字段。
	Form []FormTabFields `json:"form"`
	// Capabilities 是驱动支持的功能；每一项单独声明，未声明即不支持。
	Capabilities []Capability `json:"capabilities"`

	// 以下是写功能内部的细项声明，各自只在声明了所属的功能时有意义。

	// CanDisableForeignKeyChecks 声明导入（CapImport）能在执行期间关闭外键检查、结束后恢复
	// （Session.ExecuteBatch 的 BatchRequest.DisableForeignKeyChecks）。没声明时导入不显示
	// 「忽略外键」，会话以 ErrUnsupported 拒绝关闭外键检查。
	CanDisableForeignKeyChecks bool `json:"canDisableForeignKeyChecks"`
}

// FormTab 是资产表单的一个 Tab。
type FormTab string

const (
	TabConnection FormTab = "connection"
	TabTunnel     FormTab = "tunnel"
	TabTLS        FormTab = "tls"
	TabAdvanced   FormTab = "advanced"
)

// FormTabs 是全部已知的 Tab，按表单里的顺序。
func FormTabs() []FormTab {
	return []FormTab{TabConnection, TabTunnel, TabTLS, TabAdvanced}
}

// FormField 是资产表单的一个字段。除 FieldTunnel 外，取值就是 DatabaseConfig 的 JSON 键，
// 也是 opsctl / AI 入参里的同名键。
type FormField string

const (
	FieldHost     FormField = "host"
	FieldPort     FormField = "port"
	FieldUsername FormField = "username"
	// FieldPassword 同时声明「这个驱动用口令认证」：声明了，自动化入参才接受 password /
	// credential_id；没声明的驱动带上它们会被拒绝。
	FieldPassword     FormField = "password"
	FieldDatabase     FormField = "database"
	FieldSQLiteSource FormField = "sqlite_source"
	FieldPath         FormField = "path"
	// FieldSSHAsset 是文件所在的 SSH 资产（远端 SQLite），不是 FieldTunnel 的隧道。
	FieldSSHAsset FormField = "ssh_asset_id"
	// FieldTunnel 是所有资产类型共用的隧道 / 代理链配置区。
	FieldTunnel       FormField = "tunnel"
	FieldSSLMode      FormField = "ssl_mode"
	FieldTLS          FormField = "tls"
	FieldParams       FormField = "params"
	FieldReadOnly     FormField = "read_only"
	FieldQueryTimeout FormField = "query_timeout_seconds"
)

// FormFields 是全部已知的字段。
func FormFields() []FormField {
	return []FormField{
		FieldHost, FieldPort, FieldUsername, FieldPassword, FieldDatabase,
		FieldSQLiteSource, FieldPath, FieldSSHAsset, FieldTunnel,
		FieldSSLMode, FieldTLS, FieldParams, FieldReadOnly, FieldQueryTimeout,
	}
}

// FormTabFields 是一个 Tab 里驱动使用的字段，按展示顺序。
type FormTabFields struct {
	Tab    FormTab     `json:"tab"`
	Fields []FormField `json:"fields"`
}

// Capability 是一项可单独声明的功能。
type Capability string

const (
	// CapSchemas：库之下按 schema 分组（PostgreSQL、SQL Server）。
	CapSchemas        Capability = "schemas"
	CapCreateDatabase Capability = "create_database"
	// CapViewDDL：查看建表语句。
	CapViewDDL Capability = "view_ddl"
	// 对象浏览的四类对象各自声明。
	CapViews         Capability = "views"
	CapProcedures    Capability = "procedures"
	CapFunctions     Capability = "functions"
	CapTriggers      Capability = "triggers"
	CapGridEdit      Capability = "grid_edit"
	CapCreateTable   Capability = "create_table"
	CapAlterTable    Capability = "alter_table"
	CapDropTable     Capability = "drop_table"
	CapTruncateTable Capability = "truncate_table"
	CapImport        Capability = "import"
	CapExportSQL     Capability = "export_sql"
	CapCopySQL       Capability = "copy_sql"
	// CapReadOnly：连接级只读。
	CapReadOnly Capability = "read_only"
)

// Capabilities 是全部已知的功能。
func Capabilities() []Capability {
	return []Capability{
		CapSchemas, CapCreateDatabase, CapViewDDL,
		CapViews, CapProcedures, CapFunctions, CapTriggers,
		CapGridEdit, CapCreateTable, CapAlterTable, CapDropTable, CapTruncateTable,
		CapImport, CapExportSQL, CapCopySQL, CapReadOnly,
	}
}

// Names 返回可用于类型断言的全部名字：ID 在前，其后是别名。
func (d Descriptor) Names() []string {
	return append([]string{string(d.ID)}, d.Aliases...)
}

// Supports 报告驱动是否声明了这项功能。
func (d Descriptor) Supports(c Capability) bool {
	return slices.Contains(d.Capabilities, c)
}

// GeneratesRowStatements 报告驱动是否声明了用到 Session.RowStatements 的功能（导出为 SQL、
// 复制为 SQL）：声明了，会话就必须能生成行语句；都没声明，会话以 ErrUnsupported 拒绝。
func (d Descriptor) GeneratesRowStatements() bool {
	return d.Supports(CapExportSQL) || d.Supports(CapCopySQL)
}

// UsesField 报告驱动是否在表单的任一 Tab 中使用该字段。
func (d Descriptor) UsesField(field FormField) bool {
	for _, tab := range d.Form {
		if slices.Contains(tab.Fields, field) {
			return true
		}
	}
	return false
}
