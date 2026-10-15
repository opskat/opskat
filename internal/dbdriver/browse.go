package dbdriver

import "fmt"

// 对象浏览的四类对象，ObjectSource 的 objType 取这些值。
const (
	ObjectView      = "view"
	ObjectProcedure = "procedure"
	ObjectFunction  = "function"
	ObjectTrigger   = "trigger"
)

// ObjectCapability 返回浏览某类对象所需声明的功能；不认识的类型返回 false。
func ObjectCapability(objType string) (Capability, bool) {
	switch objType {
	case ObjectView:
		return CapViews, true
	case ObjectProcedure:
		return CapProcedures, true
	case ObjectFunction:
		return CapFunctions, true
	case ObjectTrigger:
		return CapTriggers, true
	default:
		return "", false
	}
}

// CheckObjectType 报告驱动是否提供这类对象的浏览。没有声明对应功能（或类型不认识）时返回
// 包着 ErrUnsupported 的错误，调用方应在执行任何语句之前先问这一句。
func (d Descriptor) CheckObjectType(objType string) error {
	capability, ok := ObjectCapability(objType)
	if !ok {
		return fmt.Errorf("%w: %s has no object type %q", ErrUnsupported, d.ID, objType)
	}
	if !d.Supports(capability) {
		return fmt.Errorf("%w: %s does not provide %s", ErrUnsupported, d.ID, objType)
	}
	return nil
}

// ColumnMeta 是对象浏览里一列的描述。
type ColumnMeta struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
	// Comment 是列注释，没有（或驱动不支持）时为空串。
	Comment string `json:"comment"`
	// Default 是列的默认值表达式；nil（序列化为 null）表示没有默认值，与默认值为空串区分。
	Default *string `json:"default"`
}

// IndexMeta 是一个索引。
type IndexMeta struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// ForeignKeyMeta 是外键的一列映射。
type ForeignKeyMeta struct {
	Name             string `json:"name"`
	Column           string `json:"column"`
	ReferencedTable  string `json:"referencedTable"`
	ReferencedColumn string `json:"referencedColumn"`
}

// TableMetadata 是一张表在对象浏览里的全部细节。三个切片序列化成数组（不是 null）：前端
// 直接取 length。
type TableMetadata struct {
	Columns     []ColumnMeta     `json:"columns"`
	Indexes     []IndexMeta      `json:"indexes"`
	ForeignKeys []ForeignKeyMeta `json:"foreignKeys"`
}

// ObjectMeta 命名一个 schema 对象（视图 / 存储过程 / 函数 / 触发器）。Table 只有触发器的
// 名字在表内才唯一的驱动（PostgreSQL）才给，查源码时要靠它定位。
type ObjectMeta struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Table string `json:"table,omitempty"`
}

// DatabaseObjects 是库里非表对象的分组；驱动没有的那类为空。
type DatabaseObjects struct {
	Views      []ObjectMeta `json:"views"`
	Procedures []ObjectMeta `json:"procedures"`
	Functions  []ObjectMeta `json:"functions"`
	Triggers   []ObjectMeta `json:"triggers"`
}
