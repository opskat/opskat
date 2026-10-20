package query_svc

import (
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 对象浏览绑定返回的类型。查询由驱动在自己的会话上完成（dbdriver.Session 的浏览方法），
// 这里只是把驱动的结构化结果交给前端的形状；字段与驱动一一对应。

// ColumnMeta describes one column for the object browser.
type ColumnMeta struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Nullable   bool    `json:"nullable"`
	PrimaryKey bool    `json:"primaryKey"`
	Comment    string  `json:"comment"`
	Default    *string `json:"default"`
}

// IndexMeta describes one index.
type IndexMeta struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// ForeignKeyMeta describes one foreign-key column mapping.
type ForeignKeyMeta struct {
	Name             string `json:"name"`
	Column           string `json:"column"`
	ReferencedTable  string `json:"referencedTable"`
	ReferencedColumn string `json:"referencedColumn"`
}

// TableMetadata is the full object-browser detail for a single table.
type TableMetadata struct {
	Columns     []ColumnMeta     `json:"columns"`
	Indexes     []IndexMeta      `json:"indexes"`
	ForeignKeys []ForeignKeyMeta `json:"foreignKeys"`
}

// ObjectMeta names a schema object (view / routine / trigger). Table is set
// only for PostgreSQL triggers, whose names are unique per table (not per
// schema), so the source lookup needs the owning table to resolve them.
type ObjectMeta struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Table string `json:"table,omitempty"`
}

// DatabaseObjects groups non-table schema objects for the object browser.
type DatabaseObjects struct {
	Views      []ObjectMeta `json:"views"`
	Procedures []ObjectMeta `json:"procedures"`
	Functions  []ObjectMeta `json:"functions"`
	Triggers   []ObjectMeta `json:"triggers"`
}

// NewTableMetadata 把驱动给出的表元数据转成绑定返回的形状。
func NewTableMetadata(meta *dbdriver.TableMetadata) *TableMetadata {
	out := &TableMetadata{
		Columns:     make([]ColumnMeta, len(meta.Columns)),
		Indexes:     make([]IndexMeta, len(meta.Indexes)),
		ForeignKeys: make([]ForeignKeyMeta, len(meta.ForeignKeys)),
	}
	for i, c := range meta.Columns {
		out.Columns[i] = ColumnMeta(c)
	}
	for i, ix := range meta.Indexes {
		out.Indexes[i] = IndexMeta(ix)
	}
	for i, fk := range meta.ForeignKeys {
		out.ForeignKeys[i] = ForeignKeyMeta(fk)
	}
	return out
}

// NewDatabaseObjects 把驱动给出的对象列表转成绑定返回的形状；驱动给 nil 的分组保持 nil。
func NewDatabaseObjects(objs *dbdriver.DatabaseObjects) *DatabaseObjects {
	return &DatabaseObjects{
		Views:      newObjectMetas(objs.Views),
		Procedures: newObjectMetas(objs.Procedures),
		Functions:  newObjectMetas(objs.Functions),
		Triggers:   newObjectMetas(objs.Triggers),
	}
}

func newObjectMetas(in []dbdriver.ObjectMeta) []ObjectMeta {
	if in == nil {
		return nil
	}
	out := make([]ObjectMeta, len(in))
	for i, o := range in {
		out[i] = ObjectMeta(o)
	}
	return out
}
