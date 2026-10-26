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
}

// AlterableColumn 是改表对话框里的一列。
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
	return c.Rename == "" && c.Comment == nil && len(c.Columns) == 0 && len(c.Drops) == 0
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
	return onConn(ctx, s, func(conn *sql.Conn) (*dbdriver.AlterTableStatements, error) {
		current, err := loadAlterableTable(ctx, conn, alterer, database, table)
		if err != nil {
			return nil, err
		}
		change, err := planTableChange(tables, alterer.AlterAttributes(), database, table, current, req)
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
	return current, nil
}

// planTableChange 比较请求与表现在的定义，得出改动；要改方言不能改的属性、说到不在表里的列时失败。
func planTableChange(tables Tables, attributes []dbdriver.AlterAttribute, database, table string, current *AlterableTable, req dbdriver.AlterTableRequest) (*TableChange, error) {
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
		c := ColumnChange{
			Current:         existing,
			Column:          column,
			Renamed:         column.Name != existing.Name,
			TypeChanged:     !strings.EqualFold(strings.TrimSpace(column.Type), strings.TrimSpace(existing.Type)),
			NullableChanged: column.Nullable != existing.Nullable,
			DefaultChanged:  strings.TrimSpace(column.DefaultValue) != strings.TrimSpace(existing.DefaultValue),
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
	return change, nil
}
