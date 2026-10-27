package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// TableAlterer 是方言里「改表」的那一半：怎么读出改表对话框里的这张表、怎么把改动写成语句。对话框
// 提交的样子与表现在的定义怎么比较、改动是否在方言能改的范围内，由 Session 统一完成；表引用与标识符
// 的引号沿用 Tables。
//
// 实现 Dialect 的类型同时实现 Tables 与 TableAlterer 才能改表；没实现的方言，Session.AlterTableModel
// 与 Session.AlterTableStatements 返回 dbdriver.ErrUnsupported。
type TableAlterer interface {
	// AlterAttributes 是方言能改的属性；驱动描述的 AlterTableAttributes 声明的就是这一份。
	AlterAttributes() []dbdriver.AlterAttribute
	// AlterableTable 在 conn 上读出这张表在改表对话框里的样子；表不存在时 Columns 为空。
	AlterableTable(ctx context.Context, conn *sql.Conn, database, table string) (*AlterableTable, error)
	// AlterTableSQL 把改动写成语句，按执行的次序。change 至少有一项改动。
	AlterTableSQL(ctx context.Context, conn *sql.Conn, change TableChange) ([]string, error)
}

// AlterableTable 是方言读出的、改表对话框里的一张表。
type AlterableTable struct {
	// Name 是列表给出的表名里表本身的那部分（不带 schema）：列表名以它结尾，它前面的部分（schema
	// 与点）在改名后原样保留。
	Name    string
	Comment string
	// Columns 按列在表里的次序。
	Columns []AlterableColumn
	// Indexes 是现有的索引（主键是类型为 primary 的一项），按方言给出的次序；开启了索引编辑的方言
	// （TableIndexer）给出，设计器无法完整表示的带 ReadOnlyReason、不能删除的带 Undroppable。
	Indexes []dbdriver.IndexDefinition
	// TakenIndexNames 是索引名唯一范围（TableIndexer 的 IndexEditing.NameScope）内其他表已有的索引
	// 名，供重名校验；范围为表时为空。
	TakenIndexNames []string
}

// AlterableColumn 是改表对话框里的一列。开启了自增的方言（AutoIncrementer）在 AutoIncrement 里给出现状；
// 开启了默认值写法的方言（DefaultClassifier）在这里给目录里的默认值原文，写法由 Session 经它判定后填上。
type AlterableColumn struct {
	dbdriver.ColumnDefinition
	// Row 是方言读这一列时取到的原始一行（按结果列名）：改列时方言据此保留对话框里不显示、用户也
	// 改不了的定义（MySQL 的自增、ON UPDATE、排序规则、表达式默认值）。共享代码不解释它。
	Row map[string]any
}

// TableChange 是一次改表的全部改动。
type TableChange struct {
	Database string
	// Table / TableRef 是改名之前的列表名与表引用。
	Table    string
	TableRef string
	// Rename 是不带 schema 的新表名；不改名时为空。
	Rename string
	// Target / TargetRef 是改名之后的列表名与表引用（不改名时同 Table / TableRef），改名之后的
	// 语句都用它们。
	Target    string
	TargetRef string
	// Comment 非 nil 表示把表注释改成它（用户填的原文）。
	Comment *string
	// Columns 是新增与改动了的列，按对话框里的次序；没有改动的已有列不在其中。
	Columns []ColumnChange
	// Drops 是要删除的已有列，按请求的次序。
	Drops []string
	// Indexes 是要改动的与新增的索引，按请求的次序；每个改动的现有索引由方言写成删除后重建。
	Indexes []IndexChange
	// IndexDrops 是要删除的现有索引（含引用被删列而随列删除的），排在删列之前。
	IndexDrops []dbdriver.IndexDefinition
}

// ColumnChange 是一列的改动。
type ColumnChange struct {
	// Current 是这一列现在的定义；新增的列为 nil。
	Current *AlterableColumn
	// Column 是改表之后的样子（名称已去掉首尾空白，其余是用户填的原文）。
	Column dbdriver.ColumnDefinition
	// 以下只对已有的列有意义，比较的方式见 dbdriver.AlterTableRequest。
	Renamed         bool
	TypeChanged     bool
	NullableChanged bool
	DefaultChanged  bool
	CommentChanged  bool
}

// IsNew 报告这是新增的列。
func (c ColumnChange) IsNew() bool { return c.Current == nil }

func (c TableChange) empty() bool {
	return c.Rename == "" && c.Comment == nil && len(c.Columns) == 0 && len(c.Drops) == 0 &&
		len(c.Indexes) == 0 && len(c.IndexDrops) == 0
}

func (s *Session) alterer() (Tables, TableAlterer, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, nil, err
	}
	alterer, ok := s.dialect.(TableAlterer)
	if !ok {
		return nil, nil, fmt.Errorf("%w: this driver does not alter tables", dbdriver.ErrUnsupported)
	}
	return tables, alterer, nil
}

// AlterTableModel 在一条连接上读出改表对话框里的这张表。
func (s *Session) AlterTableModel(ctx context.Context, database, table string) (*dbdriver.AlterTableModel, error) {
	_, alterer, err := s.alterer()
	if err != nil {
		return nil, err
	}
	return onConn(ctx, s, func(conn *sql.Conn) (*dbdriver.AlterTableModel, error) {
		current, err := loadAlterableTable(ctx, conn, alterer, database, table)
		if err != nil {
			return nil, err
		}
		model := &dbdriver.AlterTableModel{
			Name:    current.Name,
			Comment: current.Comment,
			Columns: make([]dbdriver.ColumnDefinition, 0, len(current.Columns)),
		}
		for _, column := range current.Columns {
			model.Columns = append(model.Columns, column.ColumnDefinition)
		}
		if IndexesTables(alterer) {
			model.Indexes = current.Indexes
			model.TakenIndexNames = current.TakenIndexNames
		}
		return model, nil
	})
}

// AlterTableStatements 在一条连接上读出这张表现在的定义，与请求比较得出改动，再由方言写成语句；
// 只生成，不执行。
func (s *Session) AlterTableStatements(ctx context.Context, database, table string, req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
	tables, alterer, err := s.alterer()
	if err != nil {
		return nil, err
	}
	if err := columnFeatures(s.dialect).Check(req.Definitions()...); err != nil {
		return nil, err
	}
	if err := IndexEditing(s.dialect).CheckAlterIndexes(req.Indexes, req.IndexDrops); err != nil {
		return nil, err
	}
	return onConn(ctx, s, func(conn *sql.Conn) (*dbdriver.AlterTableStatements, error) {
		current, err := loadAlterableTable(ctx, conn, alterer, database, table)
		if err != nil {
			return nil, err
		}
		change, err := planTableChange(tables, s.dialect, alterer.AlterAttributes(), database, table, current, req)
		if err != nil {
			return nil, err
		}
		if change.empty() {
			return &dbdriver.AlterTableStatements{Statements: []string{}, Table: table}, nil
		}
		statements, err := alterer.AlterTableSQL(ctx, conn, *change)
		if err != nil {
			return nil, err
		}
		return &dbdriver.AlterTableStatements{Statements: statements, Table: change.Target}, nil
	})
}

func loadAlterableTable(ctx context.Context, conn *sql.Conn, alterer TableAlterer, database, table string) (*AlterableTable, error) {
	current, err := alterer.AlterableTable(ctx, conn, database, table)
	if err != nil {
		return nil, fmt.Errorf("query table definition: %w", err)
	}
	if len(current.Columns) == 0 {
		return nil, fmt.Errorf("table %s not found", table)
	}
	if !strings.HasSuffix(table, current.Name) {
		return nil, fmt.Errorf("the dialect names table %s %q, which is not the end of its name", table, current.Name)
	}
	if classifier, ok := alterer.(DefaultClassifier); ok {
		for i := range current.Columns {
			current.Columns[i].DefaultKind, current.Columns[i].DefaultValue = classifier.ClassifyDefault(current.Columns[i])
		}
	}
	return current, nil
}

// planTableChange 比较请求与表现在的定义，得出改动；要改方言不能改的属性、说到不在表里的列时失败。
func planTableChange(tables Tables, dialect Dialect, attributes []dbdriver.AlterAttribute, database, table string, current *AlterableTable, req dbdriver.AlterTableRequest) (*TableChange, error) {
	mustAllow := func(attribute dbdriver.AlterAttribute, format string, args ...any) error {
		if slices.Contains(attributes, attribute) {
			return nil
		}
		return fmt.Errorf("%w: this driver cannot change "+format, append([]any{dbdriver.ErrUnsupported}, args...)...)
	}

	change := &TableChange{Database: database, Table: table, TableRef: tables.TableRef(database, table), Target: table}
	if name := strings.TrimSpace(req.Name); name != "" && name != current.Name {
		change.Rename = name
		change.Target = strings.TrimSuffix(table, current.Name) + name
	}
	change.TargetRef = tables.TableRef(database, change.Target)
	if strings.TrimSpace(req.Comment) != strings.TrimSpace(current.Comment) {
		if err := mustAllow(dbdriver.AlterTableComment, "the table comment"); err != nil {
			return nil, err
		}
		comment := req.Comment
		change.Comment = &comment
	}

	columns := make(map[string]*AlterableColumn, len(current.Columns))
	for i := range current.Columns {
		columns[current.Columns[i].Name] = &current.Columns[i]
	}
	kept := make(map[string]bool, len(req.Columns))
	for _, requested := range req.Columns {
		column := requested.Definition()
		column.Name = strings.TrimSpace(column.Name)
		if column.Name == "" {
			return nil, fmt.Errorf("a column of table %s has no name", table)
		}
		commentGiven := strings.TrimSpace(column.Comment) != ""
		if requested.OriginalName == "" {
			if commentGiven {
				if err := mustAllow(dbdriver.AlterColumnComment, "column comments (column %q)", column.Name); err != nil {
					return nil, err
				}
			}
			change.Columns = append(change.Columns, ColumnChange{Column: column})
			continue
		}

		existing, ok := columns[requested.OriginalName]
		if !ok {
			return nil, fmt.Errorf("column %q is not in table %s", requested.OriginalName, table)
		}
		if kept[existing.Name] {
			return nil, fmt.Errorf("column %q of table %s is given more than once", existing.Name, table)
		}
		kept[existing.Name] = true
		// 已有列的自增不能开启；请求不带自增（旧的改表对话框、扩展、设计器里已有列都不勾选）表示照旧，
		// 交给方言的列定义带上现状，重写列定义时才不会丢掉它。
		if column.AutoIncrement && !existing.AutoIncrement {
			return nil, fmt.Errorf("%w: existing column %q cannot become auto-increment", dbdriver.ErrUnsupported, existing.Name)
		}
		column.AutoIncrement = existing.AutoIncrement
		c := ColumnChange{
			Current:         existing,
			Column:          column,
			Renamed:         column.Name != existing.Name,
			TypeChanged:     !strings.EqualFold(strings.TrimSpace(column.Type), strings.TrimSpace(existing.Type)),
			NullableChanged: column.Nullable != existing.Nullable,
			DefaultChanged:  defaultChanged(column, existing.ColumnDefinition),
			CommentChanged:  strings.TrimSpace(column.Comment) != strings.TrimSpace(existing.Comment),
		}
		for _, attribute := range []struct {
			changed   bool
			attribute dbdriver.AlterAttribute
			what      string
		}{
			{c.TypeChanged, dbdriver.AlterColumnType, "the type"},
			{c.NullableChanged, dbdriver.AlterColumnNullable, "the nullability"},
			{c.DefaultChanged, dbdriver.AlterColumnDefault, "the default value"},
			{c.CommentChanged, dbdriver.AlterColumnComment, "the comment"},
		} {
			if attribute.changed {
				if err := mustAllow(attribute.attribute, "%s of existing column %q", attribute.what, existing.Name); err != nil {
					return nil, err
				}
			}
		}
		if c.Renamed || c.TypeChanged || c.NullableChanged || c.DefaultChanged || c.CommentChanged {
			change.Columns = append(change.Columns, c)
		}
	}

	dropped := make(map[string]bool, len(req.Drops))
	for _, name := range req.Drops {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("column %q is not in table %s", name, table)
		}
		if kept[name] || dropped[name] {
			return nil, fmt.Errorf("column %q of table %s is given more than once", name, table)
		}
		dropped[name] = true
		change.Drops = append(change.Drops, name)
	}
	// 要建 / 要改的索引的列名按改动之后的表解析，删掉的列也一并算出：引用被删列的索引随列一起删。
	after := tableColumnsAfter{
		renames: make(map[string]string, len(req.Columns)),
		present: make(map[string]bool, len(current.Columns)),
		dropped: make(map[string]bool, len(req.Drops)),
	}
	for _, column := range change.Columns {
		after.present[column.Column.Name] = true
		if column.Current != nil && column.Column.Name != column.Current.Name {
			after.renames[column.Current.Name] = column.Column.Name
		}
	}
	for i := range current.Columns {
		after.present[current.Columns[i].Name] = true
	}
	for _, name := range change.Drops {
		after.dropped[name] = true
		delete(after.present, name)
	}
	if err := planIndexes(IndexEditing(dialect), current, change, req, after); err != nil {
		return nil, err
	}
	return change, nil
}

// defaultChanged 比较默认值（见 dbdriver.AlterTableRequest）：请求没给写法时照旧比较去掉首尾空白的文本；
// 给了写法时写法不同即改动，同为「值」时比较原文，同为表达式时比较去掉首尾空白的文本。
func defaultChanged(requested, current dbdriver.ColumnDefinition) bool {
	if requested.DefaultKind == "" {
		return strings.TrimSpace(requested.DefaultValue) != strings.TrimSpace(current.DefaultValue)
	}
	if requested.DefaultKind != current.DefaultKind {
		return true
	}
	switch requested.DefaultKind {
	case dbdriver.DefaultKindValue:
		return requested.DefaultValue != current.DefaultValue
	case dbdriver.DefaultKindExpression:
		return strings.TrimSpace(requested.DefaultValue) != strings.TrimSpace(current.DefaultValue)
	}
	return false
}
