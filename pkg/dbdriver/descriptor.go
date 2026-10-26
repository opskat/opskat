package dbdriver

import (
	"fmt"
	"maps"
	"math"
	"slices"
)

// Descriptor 是一个驱动的自我描述，原样经绑定交给前端。它只有可序列化的普通数据。
type Descriptor struct {
	// ID 是存进资产配置 driver 字段的值，改名即数据迁移。
	ID DriverID `json:"id"`
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
	// LocatesRowWithoutPrimaryKey 声明表格编辑（CapGridEdit）在表没有主键时也能精确定位要改 / 删
	// 的那一行（PostgreSQL 的 (tableoid, ctid)、SQLite 的 rowid），而不只是在原值相同的几行里取一行。声明了，
	// 表格不显示「无主键」提示。
	LocatesRowWithoutPrimaryKey bool `json:"locatesRowWithoutPrimaryKey"`
	// CreateDatabaseOptions 是新建数据库（CapCreateDatabase）时驱动接受的选项字段及默认值，按
	// 对话框里的次序。建库对话框只显示这里声明的字段；没有声明的选项，会话以错误拒绝。
	CreateDatabaseOptions []DatabaseOption `json:"createDatabaseOptions"`
	// AlterTableAttributes 是改表（CapAlterTable）时，除了改表名、给列改名、新增与删除列之外还能
	// 改的属性。改表对话框锁定没有声明的属性的输入；会话拒绝改动它们。
	AlterTableAttributes []AlterAttribute `json:"alterTableAttributes"`

	// DriverFields 是驱动专属的配置字段：值存在资产配置的 driver_options 里，按这里的声明校验。
	// 内置驱动不声明。
	DriverFields []DriverField `json:"driverFields"`
}

// DriverFieldType 是驱动专属字段的取值类型。
type DriverFieldType string

const (
	FieldTypeText     DriverFieldType = "text"
	FieldTypeNumber   DriverFieldType = "number"
	FieldTypeSwitch   DriverFieldType = "switch"
	FieldTypeEnum     DriverFieldType = "enum"
	FieldTypePassword DriverFieldType = "password"
	FieldTypeTextarea DriverFieldType = "textarea"
)

// DriverFieldTypes 是全部已知的专属字段类型。
func DriverFieldTypes() []DriverFieldType {
	return []DriverFieldType{FieldTypeText, FieldTypeNumber, FieldTypeSwitch, FieldTypeEnum, FieldTypePassword, FieldTypeTextarea}
}

// DriverField 声明驱动的一个专属配置字段。
type DriverField struct {
	// Key 是 driver_options 里的键，也是 opsctl / AI 入参 driver_options 对象里的键；
	// 不能与内置表单字段（FormFields）同名。
	Key  string          `json:"key"`
	Type DriverFieldType `json:"type"`
	// Tab 是字段所在的表单 Tab。
	Tab      FormTab `json:"tab"`
	Required bool    `json:"required,omitempty"`
	// Default 是未给出时的取值，类型与 Type 一致；口令类不能有默认值。
	Default any `json:"default,omitempty"`
	// Enum 是枚举类的取值，只有枚举类声明。
	Enum []string `json:"enum,omitempty"`
	// Label / Description 可以是扩展多语言（locales/）里的键：宿主按调用方的语言用提供驱动的
	// 扩展的翻译解析后交给前端（Descriptor.Localized），没有翻译的原样显示。
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

// Localized 返回专属字段的标签与说明经 tr 翻译后的描述副本，不改 d。
func (d Descriptor) Localized(tr func(key string) string) Descriptor {
	if len(d.DriverFields) == 0 {
		return d
	}
	fields := make([]DriverField, len(d.DriverFields))
	for i, f := range d.DriverFields {
		if f.Label != "" {
			f.Label = tr(f.Label)
		}
		if f.Description != "" {
			f.Description = tr(f.Description)
		}
		fields[i] = f
	}
	d.DriverFields = fields
	return d
}

// Validate 检查描述自身是否合法；扩展加载时用它拒绝不合法的驱动。错误点名驱动 id 与出问题的键。
func (d Descriptor) Validate() error {
	if d.ID == "" {
		return fmt.Errorf("driver descriptor has no id")
	}
	for _, tab := range d.Form {
		if !slices.Contains(FormTabs(), tab.Tab) {
			return fmt.Errorf("driver %q: unknown form tab %q", d.ID, tab.Tab)
		}
		for _, f := range tab.Fields {
			if !slices.Contains(FormFields(), f) {
				return fmt.Errorf("driver %q: unknown form field %q", d.ID, f)
			}
			if home, ok := groupTabs[f]; ok && tab.Tab != home {
				return fmt.Errorf("driver %q: form field %q belongs on the %q tab, not %q", d.ID, f, home, tab.Tab)
			}
		}
	}
	if d.UsesField(FieldTLSCert) && d.UsesField(FieldTLS) {
		return fmt.Errorf("driver %q: form field %q already includes the %q switch; declare only one of them", d.ID, FieldTLSCert, FieldTLS)
	}
	for _, c := range d.Capabilities {
		if !slices.Contains(Capabilities(), c) {
			return fmt.Errorf("driver %q: unknown capability %q", d.ID, c)
		}
	}
	seen := map[string]bool{}
	for _, f := range d.DriverFields {
		if err := f.validate(); err != nil {
			return fmt.Errorf("driver %q: %w", d.ID, err)
		}
		if seen[f.Key] {
			return fmt.Errorf("driver %q: duplicate driver field %q", d.ID, f.Key)
		}
		seen[f.Key] = true
	}
	return nil
}

func (f DriverField) validate() error {
	if f.Key == "" {
		return fmt.Errorf("driver field has an empty key")
	}
	if slices.Contains(FormFields(), FormField(f.Key)) {
		return fmt.Errorf("driver field %q has the same name as a built-in form field", f.Key)
	}
	if !slices.Contains(DriverFieldTypes(), f.Type) {
		return fmt.Errorf("driver field %q has unknown type %q", f.Key, f.Type)
	}
	if !slices.Contains(FormTabs(), f.Tab) {
		return fmt.Errorf("driver field %q has unknown tab %q", f.Key, f.Tab)
	}
	if f.Type == FieldTypeEnum && len(f.Enum) == 0 {
		return fmt.Errorf("driver field %q is an enum without values", f.Key)
	}
	if f.Type != FieldTypeEnum && len(f.Enum) > 0 {
		return fmt.Errorf("driver field %q declares enum values but is not an enum", f.Key)
	}
	if f.Default == nil {
		return nil
	}
	if f.Type == FieldTypePassword {
		return fmt.Errorf("driver field %q is a password and cannot have a default", f.Key)
	}
	if err := f.checkValue(f.Default); err != nil {
		return fmt.Errorf("default of %w", err)
	}
	return nil
}

// checkValue 检查取值是否符合声明的类型；错误点名键。
func (f DriverField) checkValue(v any) error {
	bad := func(want string) error { return fmt.Errorf("driver field %q must be %s", f.Key, want) }
	switch f.Type {
	case FieldTypeText, FieldTypeTextarea, FieldTypePassword:
		if _, ok := v.(string); !ok {
			return bad("a string")
		}
	case FieldTypeSwitch:
		if _, ok := v.(bool); !ok {
			return bad("a boolean")
		}
	case FieldTypeNumber:
		var n float64
		switch x := v.(type) {
		case int:
			n = float64(x)
		case int64:
			n = float64(x)
		case float64:
			n = x
		default:
			return bad("a number")
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return bad("an integer")
		}
	case FieldTypeEnum:
		s, ok := v.(string)
		if !ok || !slices.Contains(f.Enum, s) {
			return bad(fmt.Sprintf("one of %v", f.Enum))
		}
	}
	return nil
}

// DriverField 返回按键声明的专属字段。
func (d Descriptor) DriverField(key string) (DriverField, bool) {
	for _, f := range d.DriverFields {
		if f.Key == key {
			return f, true
		}
	}
	return DriverField{}, false
}

// CheckDriverOptions 检查 driver_options：键必须是声明过的，给出的值必须符合类型。不检查必填、
// 不补默认值（局部更新只带部分键）。
func (d Descriptor) CheckDriverOptions(opts map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(opts)) {
		f, ok := d.DriverField(key)
		if !ok {
			return fmt.Errorf("driver %q has no driver option %q", d.ID, key)
		}
		if err := f.checkValue(opts[key]); err != nil {
			return fmt.Errorf("driver %q: %w", d.ID, err)
		}
	}
	return nil
}

// CompleteDriverOptions 补上缺省值并检查必填，返回完整的 driver_options（不改入参）。
func (d Descriptor) CompleteDriverOptions(opts map[string]any) (map[string]any, error) {
	out := maps.Clone(opts)
	if out == nil {
		out = map[string]any{}
	}
	for _, f := range d.DriverFields {
		v, present := out[f.Key]
		if s, isString := v.(string); present && isString && s == "" {
			present = false
		}
		if !present && f.Default != nil {
			out[f.Key] = f.Default
			present = true
		}
		if !present && f.Required {
			return nil, fmt.Errorf("driver %q: driver option %q is required", d.ID, f.Key)
		}
	}
	return out, nil
}

// AlterAttribute 是改表时可以单独声明能否修改的一项属性。
type AlterAttribute string

const (
	// AlterTableComment：表注释。
	AlterTableComment AlterAttribute = "table_comment"
	// AlterColumnType / AlterColumnNullable / AlterColumnDefault：已有列的类型、可空与默认值。
	// 新增的列总能给出这三项。
	AlterColumnType     AlterAttribute = "column_type"
	AlterColumnNullable AlterAttribute = "column_nullable"
	AlterColumnDefault  AlterAttribute = "column_default"
	// AlterColumnComment：列注释，新增的列与已有列都算。
	AlterColumnComment AlterAttribute = "column_comment"
)

// AlterAttributes 是全部已知的改表属性。
func AlterAttributes() []AlterAttribute {
	return []AlterAttribute{AlterTableComment, AlterColumnType, AlterColumnNullable, AlterColumnDefault, AlterColumnComment}
}

// DatabaseOption 是新建数据库的一个选项字段。
type DatabaseOption struct {
	// Key 是选项的键，Session.CreateDatabaseStatements 的 Options 按它取值；对话框据此选标签。
	Key string `json:"key"`
	// Default 是对话框里这个字段的初始值，可以为空。
	Default string `json:"default"`
}

// 建库选项的键。
const (
	OptionCharset   = "charset"
	OptionCollation = "collation"
)

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

// FormField 是资产表单的一个字段。除 FieldTunnel 外，取值就是数据库资产配置的 JSON 键，
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
	FieldTunnel  FormField = "tunnel"
	FieldSSLMode FormField = "ssl_mode"
	FieldTLS     FormField = "tls"
	// FieldTLSCert 是证书字段组，与 FieldTunnel 一样不是单个配置键：启用 TLS（tls）、跳过证书
	// 校验（tls_insecure）、服务器名（tls_server_name），CA 证书 / 客户端证书 / 客户端私钥各填
	// 文件路径或粘贴内容（tls_{ca,cert,key}_{file,pem}），与 Redis 资产同名同义。只能声明在
	// TLS Tab，且自带启用开关，不再与 FieldTLS 同时声明。声明了的驱动在资产启用 TLS 时，打开
	// 会话就收到 OpenRequest.TLS。要求扩展的 hostABI 2.4 及以上。
	FieldTLSCert FormField = "tls_cert"
	// FieldKerberos 是 Kerberos 字段组，也不是单个配置键：认证方式（口令或 Kerberos）与选了
	// Kerberos 时的主体、凭据、KDC 地址、服务主体。只能声明在连接 Tab。声明了的驱动在资产的认证
	// 方式为 Kerberos 时，打开会话就收到 OpenRequest.Kerberos。要求扩展在契约里声明
	// capabilities.databaseKerberos（hostABI 2.5 及以上）。
	FieldKerberos     FormField = "kerberos"
	FieldParams       FormField = "params"
	FieldReadOnly     FormField = "read_only"
	FieldQueryTimeout FormField = "query_timeout_seconds"
)

// FormFields 是全部已知的字段。
func FormFields() []FormField {
	return []FormField{
		FieldHost, FieldPort, FieldUsername, FieldPassword, FieldDatabase,
		FieldSQLiteSource, FieldPath, FieldSSHAsset, FieldTunnel,
		FieldSSLMode, FieldTLS, FieldTLSCert, FieldKerberos, FieldParams, FieldReadOnly, FieldQueryTimeout,
	}
}

// groupTabs 是只能声明在某一个 Tab 的字段组及其 Tab。
var groupTabs = map[FormField]FormTab{FieldTLSCert: TabTLS, FieldKerberos: TabConnection}

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
	// CapClassifyStatements：驱动自己切分并分类 SQL（StatementClassifier，经 ClassifyStatements
	// 取用），AI / opsctl 的权限判断与查询面板按它的结果，而不是宿主通用的 SQL 解析。扩展驱动
	// 声明它要求扩展的 hostABI 2.5 及以上。
	CapClassifyStatements Capability = "classify_statements"
)

// Capabilities 是全部已知的功能。
func Capabilities() []Capability {
	return []Capability{
		CapSchemas, CapCreateDatabase, CapViewDDL,
		CapViews, CapProcedures, CapFunctions, CapTriggers,
		CapGridEdit, CapCreateTable, CapAlterTable, CapDropTable, CapTruncateTable,
		CapImport, CapExportSQL, CapCopySQL, CapReadOnly, CapClassifyStatements,
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

// GeneratesRowStatements 报告驱动是否声明了用到 Session.PrepareRowStatements 的功能（导出为 SQL、
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
