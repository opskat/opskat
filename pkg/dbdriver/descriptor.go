package dbdriver

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
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

	// 以下是表设计器（建表 / 改表）里列与名称的细项声明，只在声明了 CapCreateTable 或 CapAlterTable 时
	// 有意义。都可以不声明：缺省即不支持，序列化时省略，没声明它们的驱动（包括按旧版本构建的扩展）照旧。

	// ColumnTypes 是类型输入框下拉里的常用类型与新列、新表主键的预填类型；没声明时只有输入框，新列
	// 不预填类型。
	ColumnTypes *ColumnTypes `json:"columnTypes,omitempty"`
	// ExplicitDefaults 声明列的默认值按写法给出（ColumnDefinition.DefaultKind：无 / NULL / 值 / 表达式），
	// 改表读出的已有列也带写法。没声明时默认值是一段文本，按 NewColumn.DefaultValue 的规则猜写法；会话
	// 以 ErrUnsupported 拒绝带写法的请求。sqldb 的方言实现 DefaultClassifier 即开启（sqldb.ClassifiesDefaults）。
	ExplicitDefaults bool `json:"explicitDefaults,omitempty"`
	// AutoIncrement 声明新表与新列可以设自增（ColumnDefinition.AutoIncrement），改表读出的已有列也带自增
	// 现状；已有列的自增不能改。没声明时会话以 ErrUnsupported 拒绝要自增的列，读出的列也不带自增。
	// sqldb 的方言实现 AutoIncrementer 即开启（sqldb.AutoIncrements）。
	AutoIncrement bool `json:"autoIncrement,omitempty"`
	// AutoIncrementNewTablesOnly 声明自增只能在新表上设：改表时新增的列不能自增（如 SQLite 只有 INTEGER
	// PRIMARY KEY 自增，而 ADD COLUMN 加不出主键）。设计器不在已有表的新列上提供自增，会话以 ErrUnsupported
	// 拒绝。只与 AutoIncrement 一起声明；sqldb 的方言实现 NewTableAutoIncrementer 即声明
	// （sqldb.AutoIncrementsNewTablesOnly）。
	AutoIncrementNewTablesOnly bool `json:"autoIncrementNewTablesOnly,omitempty"`
	// CreateTableComments 声明建表时写得出表注释与列注释（CreateTableRequest.Comment、NewColumn.Comment）。
	// 没声明时设计器新建表不提供注释输入，会话与宿主转给扩展之前都以 ErrUnsupported 拒绝带注释的建表请求
	//（按旧版本构建的扩展不认识这两个字段，会建出没有注释的表而不报错）。
	CreateTableComments bool `json:"createTableComments,omitempty"`
	// EmptyStringIsNull 声明数据库把空字符串存成 NULL（如 Oracle）：表设计器在默认值写法为「值」且是空串时
	// 提醒（只提醒，不阻止保存）。只是给用户的提示，不影响生成的语句；与 ExplicitDefaults 一起声明才有意义。
	EmptyStringIsNull bool `json:"emptyStringIsNull,omitempty"`
	// NameFolding 是数据库把不加引号的名称折叠成的大小写：表设计器据此提醒与它不符的表名、列名、
	// 索引名之后要加引号才能访问（只提醒，不阻止保存、不改写）。空表示不折叠，不提醒。
	NameFolding NameFolding `json:"nameFolding,omitempty"`
	// IndexEditing 声明表设计器里的索引编辑（可用类型、主键可改、降序、前缀长度、索引注释、
	// 名称唯一范围与额外选项，见 IndexEditing）；没声明时索引页只读，会话以 ErrUnsupported 拒绝
	// 带索引改动的建表 / 改表请求。sqldb 的方言实现 TableIndexer 即开启（sqldb.IndexesTables）。
	IndexEditing *IndexEditing `json:"indexEditing,omitempty"`
	// TransactionalDDL 声明表结构语句（建表、改表、索引）可以在事务里执行、失败时整批回滚：表设计器
	// 保存时把整批语句交给 Session.ExecuteBatch 原子执行（BatchRequest.Atomic），失败则表结构不变。没声明
	// 时（如 MySQL、Oracle 的 DDL 会隐式提交）逐条执行、停在第一条失败，已执行的留下。
	TransactionalDDL bool `json:"transactionalDDL,omitempty"`

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

// Localized 返回专属字段的标签与说明、常用类型的分组名、索引类型与选项的显示名经 tr 翻译后的
// 描述副本，不改 d。
func (d Descriptor) Localized(tr func(key string) string) Descriptor {
	if len(d.DriverFields) > 0 {
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
	}
	if d.ColumnTypes != nil && len(d.ColumnTypes.Groups) > 0 {
		types := *d.ColumnTypes
		types.Groups = make([]ColumnTypeGroup, len(d.ColumnTypes.Groups))
		for i, group := range d.ColumnTypes.Groups {
			group.Label = tr(group.Label)
			types.Groups[i] = group
		}
		d.ColumnTypes = &types
	}
	if d.IndexEditing != nil && (len(d.IndexEditing.Types) > 0 || len(d.IndexEditing.Options) > 0) {
		editing := *d.IndexEditing
		if len(editing.Types) > 0 {
			editing.Types = make([]IndexTypeDecl, len(d.IndexEditing.Types))
			for i, declared := range d.IndexEditing.Types {
				declared.Label = tr(declared.Label)
				editing.Types[i] = declared
			}
		}
		if len(editing.Options) > 0 {
			editing.Options = make([]IndexOptionDecl, len(d.IndexEditing.Options))
			for i, option := range d.IndexEditing.Options {
				option.Label = tr(option.Label)
				editing.Options[i] = option
			}
		}
		d.IndexEditing = &editing
	}
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
	if d.ColumnTypes != nil {
		if err := d.ColumnTypes.validate(); err != nil {
			return fmt.Errorf("driver %q: %w", d.ID, err)
		}
	}
	if d.AutoIncrementNewTablesOnly && !d.AutoIncrement {
		return fmt.Errorf("driver %q: auto-increment limited to new tables is declared without auto-increment", d.ID)
	}
	if d.NameFolding != "" && !slices.Contains(NameFoldings(), d.NameFolding) {
		return fmt.Errorf("driver %q: unknown name folding %q", d.ID, d.NameFolding)
	}
	if d.IndexEditing != nil {
		if err := d.IndexEditing.validate(); err != nil {
			return fmt.Errorf("driver %q: %w", d.ID, err)
		}
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

// ColumnTypes 是表设计器里类型输入框的常用类型。下拉只是提示：输入任何类型文本都原样采用。
type ColumnTypes struct {
	// Groups 是下拉里的分组，按下拉里的次序；可以为空（只声明预填类型）。
	Groups []ColumnTypeGroup `json:"groups"`
	// NewColumn 是新列预填的类型；空表示不预填，要用户填写。
	NewColumn string `json:"newColumn,omitempty"`
	// PrimaryKey 是新表预置的自增主键 id 的类型；空表示不预填。
	PrimaryKey string `json:"primaryKey,omitempty"`
}

// ColumnTypeGroup 是下拉里的一组常用类型。
type ColumnTypeGroup struct {
	// Label 是分组的显示名：宿主多语言里的键，或（扩展驱动）扩展多语言（locales/）里的键，同
	// DriverField.Label 经 Descriptor.Localized 解析，没有翻译的原样显示。
	Label string `json:"label"`
	// Types 是这一组的类型文本，按下拉里的次序。
	Types []string `json:"types"`
}

// validate 检查常用类型：要声明点什么；分组有名字、有类型；类型文本非空、没有首尾空白、不分大小写
// 不重复（下拉里两个一样的选项分不清）。
func (c ColumnTypes) validate() error {
	if len(c.Groups) == 0 && c.NewColumn == "" && c.PrimaryKey == "" {
		return fmt.Errorf("column types declare no group and no default type; leave them out instead")
	}
	seen := map[string]bool{}
	for _, group := range c.Groups {
		if strings.TrimSpace(group.Label) == "" {
			return fmt.Errorf("a column type group has an empty label")
		}
		if len(group.Types) == 0 {
			return fmt.Errorf("column type group %q has no types", group.Label)
		}
		for _, columnType := range group.Types {
			if strings.TrimSpace(columnType) == "" {
				return fmt.Errorf("column type group %q has an empty type", group.Label)
			}
			if strings.TrimSpace(columnType) != columnType {
				return fmt.Errorf("column type %q has surrounding blanks", columnType)
			}
			if seen[strings.ToLower(columnType)] {
				return fmt.Errorf("column type %q is listed more than once", columnType)
			}
			seen[strings.ToLower(columnType)] = true
		}
	}
	for _, preset := range []struct{ what, columnType string }{{"new column", c.NewColumn}, {"primary key", c.PrimaryKey}} {
		if preset.columnType != "" && (strings.TrimSpace(preset.columnType) == "" || strings.TrimSpace(preset.columnType) != preset.columnType) {
			return fmt.Errorf("the %s type %q is blank or has surrounding blanks", preset.what, preset.columnType)
		}
	}
	return nil
}

// NameFolding 是数据库把不加引号的名称折叠成的大小写。
type NameFolding string

const (
	// NameFoldUpper：不加引号的名称按大写存（Oracle）。
	NameFoldUpper NameFolding = "upper"
	// NameFoldLower：不加引号的名称按小写存（PostgreSQL）。
	NameFoldLower NameFolding = "lower"
)

// NameFoldings 是全部已知的名称折叠；不折叠不声明。
func NameFoldings() []NameFolding {
	return []NameFolding{NameFoldUpper, NameFoldLower}
}

// ColumnFeatures 返回描述对建表 / 改表请求里列的声明。
func (d Descriptor) ColumnFeatures() ColumnFeatures {
	return ColumnFeatures{
		ExplicitDefaults: d.ExplicitDefaults, AutoIncrement: d.AutoIncrement, NewTablesOnly: d.AutoIncrementNewTablesOnly,
		CreateComments: d.CreateTableComments,
	}
}

// IndexType 是索引类型的标识：内置的见 IndexType 常量，扩展可以声明自己的 id（如 Oracle 的
// 位图 bitmap）。
type IndexType string

const (
	// IndexTypePrimary：主键。主键作为一项索引显示；已有的主键能否改由 IndexEditing.AlterPrimaryKey
	// 声明，没声明时已有主键只读、类型「主键」不可选。
	IndexTypePrimary IndexType = "primary"
	// IndexTypeUnique：唯一索引。
	IndexTypeUnique IndexType = "unique"
	// IndexTypeNormal：普通索引。
	IndexTypeNormal IndexType = "normal"
	// IndexTypeFullText：全文索引。
	IndexTypeFullText IndexType = "fulltext"
)

// IndexNameScope 是索引名的唯一范围，由驱动声明；重名校验与读范围内其他表的索引名都按它。
type IndexNameScope string

const (
	// IndexScopeTable：索引名在一张表内唯一（MySQL、SQL Server）。
	IndexScopeTable IndexNameScope = "table"
	// IndexScopeSchema：索引名在一个 schema 内唯一（PostgreSQL）。
	IndexScopeSchema IndexNameScope = "schema"
	// IndexScopeDatabase：索引名在整个库内唯一（SQLite）。
	IndexScopeDatabase IndexNameScope = "database"
)

// IndexNameScopes 是全部已知的索引名范围。
func IndexNameScopes() []IndexNameScope {
	return []IndexNameScope{IndexScopeTable, IndexScopeSchema, IndexScopeDatabase}
}

// IndexTypeDecl 声明一个可用的索引类型。
type IndexTypeDecl struct {
	// ID 是类型的标识，见 IndexType。
	ID IndexType `json:"id"`
	// Label 是类型的显示名：宿主多语言里的键，或（扩展驱动）扩展多语言（locales/）里的键，同
	// DriverField.Label 经 Descriptor.Localized 解析，没有翻译的原样显示。
	Label string `json:"label"`
}

// IndexOptionDecl 声明索引的一个额外选项（如 MySQL 的方法、SQL Server 的聚集）：索引编辑区按
// 声明通用渲染——枚举给下拉、开关给开关，值经 IndexDefinition.Options 按键交给驱动。
type IndexOptionDecl struct {
	// Key 是选项的键，IndexDefinition.Options 按它取值。
	Key string `json:"key"`
	// Label 是选项的显示名，同 IndexTypeDecl.Label。
	Label string `json:"label"`
	// Type 是选项的取值类型：FieldTypeEnum（取值见 Enum）或 FieldTypeSwitch。
	Type DriverFieldType `json:"type"`
	// Enum 是枚举的取值，按编辑区下拉里的次序；只有 FieldTypeEnum 声明。
	Enum []string `json:"enum,omitempty"`
	// Exclusive 声明这个开关一张表至多一个索引打开（如 SQL Server 的聚集）：设计器就地报错，会话拒绝改完
	// 之后有两个索引打开它的请求。只有 FieldTypeSwitch 可以声明。
	Exclusive bool `json:"exclusive,omitempty"`
}

// IndexEditing 是表设计器里的索引编辑声明，见 Descriptor.IndexEditing。
type IndexEditing struct {
	// Types 是可用的索引类型，按编辑区下拉里的次序；新建的表要带主键就在其中声明 primary。
	Types []IndexTypeDecl `json:"types"`
	// AlterPrimaryKey 声明已有的主键可以改（换列、删除）；没声明时对主键的任何改动以
	// ErrUnsupported 拒绝。
	AlterPrimaryKey bool `json:"alterPrimaryKey,omitempty"`
	// Descending 声明索引的列可以降序（IndexColumn.Descending）。
	Descending bool `json:"descending,omitempty"`
	// PrefixLength 声明索引的列可以给前缀长度（IndexColumn.PrefixLength）。
	PrefixLength bool `json:"prefixLength,omitempty"`
	// Comments 声明索引可以写注释（IndexDefinition.Comment）。
	Comments bool `json:"comments,omitempty"`
	// NameScope 是索引名的唯一范围；改表读结构时给出范围内其他表已有的索引名
	//（AlterTableModel.TakenIndexNames）供重名校验。
	NameScope IndexNameScope `json:"nameScope"`
	// Options 是驱动声明的额外选项，按编辑区里的次序；没有为空。
	Options []IndexOptionDecl `json:"options,omitempty"`
}

// validate 检查索引编辑声明：至少声明一个类型；类型与选项有标识、有显示名，不重复；选项是枚举
// （有取值）或开关；名称范围是已知的。
func (i IndexEditing) validate() error {
	if len(i.Types) == 0 {
		return fmt.Errorf("index editing declares no type; leave it out instead")
	}
	if !slices.Contains(IndexNameScopes(), i.NameScope) {
		return fmt.Errorf("index editing has the unknown name scope %q", i.NameScope)
	}
	seenTypes := map[IndexType]bool{}
	for _, declared := range i.Types {
		if strings.TrimSpace(string(declared.ID)) == "" {
			return fmt.Errorf("an index type has an empty id")
		}
		if strings.TrimSpace(string(declared.ID)) != string(declared.ID) {
			return fmt.Errorf("index type id %q has surrounding blanks", declared.ID)
		}
		if strings.TrimSpace(declared.Label) == "" {
			return fmt.Errorf("index type %q has an empty label", declared.ID)
		}
		if seenTypes[declared.ID] {
			return fmt.Errorf("index type %q is declared more than once", declared.ID)
		}
		seenTypes[declared.ID] = true
	}
	seenKeys := map[string]bool{}
	for _, option := range i.Options {
		if strings.TrimSpace(option.Key) == "" {
			return fmt.Errorf("an index option has an empty key")
		}
		if strings.TrimSpace(option.Key) != option.Key {
			return fmt.Errorf("index option key %q has surrounding blanks", option.Key)
		}
		if strings.TrimSpace(option.Label) == "" {
			return fmt.Errorf("index option %q has an empty label", option.Key)
		}
		if seenKeys[option.Key] {
			return fmt.Errorf("index option %q is declared more than once", option.Key)
		}
		seenKeys[option.Key] = true
		switch option.Type {
		case FieldTypeEnum:
			if len(option.Enum) == 0 {
				return fmt.Errorf("index option %q is an enum without values", option.Key)
			}
			seenValues := map[string]bool{}
			for _, value := range option.Enum {
				if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
					return fmt.Errorf("index option %q has the blank or padded enum value %q", option.Key, value)
				}
				if seenValues[value] {
					return fmt.Errorf("index option %q lists the enum value %q more than once", option.Key, value)
				}
				seenValues[value] = true
			}
			if option.Exclusive {
				return fmt.Errorf("index option %q is an enum and cannot be exclusive; only a switch can", option.Key)
			}
		case FieldTypeSwitch:
			if len(option.Enum) > 0 {
				return fmt.Errorf("index option %q is a switch but declares enum values", option.Key)
			}
		default:
			return fmt.Errorf("index option %q has type %q; an index option is an enum or a switch", option.Key, option.Type)
		}
	}
	return nil
}

// DeclaresType 报告 t 是声明的索引类型之一；没声明索引编辑的驱动只有不声明任何类型。
func (i *IndexEditing) DeclaresType(t IndexType) bool {
	if i == nil {
		return false
	}
	return slices.ContainsFunc(i.Types, func(declared IndexTypeDecl) bool { return declared.ID == t })
}

// Check 检查要写出的索引定义（新建的索引、改动的索引改完的样子、建表请求里的索引）只用了声明了
// 的能力：类型是声明的之一，降序、前缀长度、注释与选项只在声明了时给出，都以包着 ErrUnsupported
// 的错误拒绝；INCLUDE 列与表达式列读得出、写不了，同样拒绝。选项值不符合声明的类型是普通错误。
// e 为 nil（驱动没声明索引编辑）时任何索引都不支持。会话、宿主转给扩展之前都按它检查。错误点名索引。
func (i *IndexEditing) Check(indexes ...IndexDefinition) error {
	for _, index := range indexes {
		if !i.DeclaresType(index.Type) {
			return fmt.Errorf("%w: this driver does not declare the index type %q (index %q)", ErrUnsupported, index.Type, index.Name)
		}
		if len(index.Include) > 0 {
			return fmt.Errorf("%w: INCLUDE columns cannot be written, only read (index %q)", ErrUnsupported, index.Name)
		}
		for _, column := range index.Columns {
			if column.Expression {
				return fmt.Errorf("%w: expression index columns cannot be written, only read (index %q)", ErrUnsupported, index.Name)
			}
			if column.Descending && !i.Descending {
				return fmt.Errorf("%w: this driver does not declare descending index columns (index %q)", ErrUnsupported, index.Name)
			}
			if column.PrefixLength > 0 && !i.PrefixLength {
				return fmt.Errorf("%w: this driver does not declare index column prefixes (index %q)", ErrUnsupported, index.Name)
			}
		}
		if strings.TrimSpace(index.Comment) != "" && !i.Comments {
			return fmt.Errorf("%w: this driver does not declare index comments (index %q)", ErrUnsupported, index.Name)
		}
		for key, value := range index.Options {
			option, ok := i.indexOption(key)
			if !ok {
				return fmt.Errorf("%w: this driver does not declare the index option %q (index %q)", ErrUnsupported, key, index.Name)
			}
			if err := option.checkValue(key, value); err != nil {
				return fmt.Errorf("index %q: %w", index.Name, err)
			}
		}
	}
	return nil
}

func (i *IndexEditing) indexOption(key string) (IndexOptionDecl, bool) {
	for _, option := range i.Options {
		if option.Key == key {
			return option, true
		}
	}
	return IndexOptionDecl{}, false
}

// checkValue 检查选项的取值是否符合声明的类型；错误点名键。
func (o IndexOptionDecl) checkValue(key string, v any) error {
	switch o.Type {
	case FieldTypeEnum:
		s, ok := v.(string)
		if !ok || !slices.Contains(o.Enum, s) {
			return fmt.Errorf("index option %q must be one of %v", key, o.Enum)
		}
	case FieldTypeSwitch:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("index option %q must be a boolean", key)
		}
	}
	return nil
}

// CheckAlterIndexes 检查改表请求里的索引只用声明了的能力（会话、宿主转给扩展之前都按它拒绝）：
// 没声明索引编辑时任何索引改动都以包着 ErrUnsupported 的错误拒绝；声明了时新建的索引按 Check 检查、
// 新建主键还要声明了主键可改。已有的索引与现状的比较（只读、改名、随删列、删除不可删的）要读了表
// 才知道，由会话做。
func (i *IndexEditing) CheckAlterIndexes(indexes []AlterIndex, drops []string) error {
	if i == nil {
		if len(indexes) > 0 || len(drops) > 0 {
			return fmt.Errorf("%w: this driver does not declare index editing, but the request changes indexes", ErrUnsupported)
		}
		return nil
	}
	for _, index := range indexes {
		if index.OriginalName != "" {
			continue
		}
		if index.Type == IndexTypePrimary && !i.AlterPrimaryKey {
			return fmt.Errorf("%w: this driver does not declare altering the primary key, but new index %q is one", ErrUnsupported, index.Name)
		}
		if err := i.Check(index.Definition()); err != nil {
			return err
		}
	}
	return nil
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
