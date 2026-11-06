package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// RowEditor 是方言里「表格编辑怎么写一行」的那一半：新增一行、只删一行，以及没有主键时怎么把
// 定位条件收敛到物理上的一行。字面量与只改一行的 UPDATE 沿用 RowWriter，表引用与标识符的引号
// 沿用 Tables。
//
// 实现 Dialect 的类型同时实现 Tables、RowWriter 与 RowEditor 才生成表格编辑的语句；没实现的方言，
// Session.GridEditStatements 返回 dbdriver.ErrUnsupported。
type RowEditor interface {
	// InsertRowSQL 是新增一行的 INSERT（含末尾分号）。columns / values 已写好、以逗号分隔、不含
	// 括号；为空表示整行取默认值。identity 为真表示写入的列里有数据库默认拒绝显式写值、要用方言
	// 的开关才能写的自增列（TableColumn.IdentityAlways 且不是 NoIdentityOverride）。
	InsertRowSQL(tableRef, columns, values string, identity bool) string
	// DeleteRowSQL 是只删一行的 DELETE（含末尾分号）。where 已写好，不含关键字。
	DeleteRowSQL(tableRef, where string) string
	// ExactRow 把 where 匹配到的行收敛成其中物理上的一行，给出新的条件（PostgreSQL 的 (tableoid, ctid)、
	// SQLite 的 rowid）。方言没有物理行标识时 ok 为假：只改 / 只删一行由 UpdateRowSQL /
	// DeleteRowSQL 自己的写法保证（LIMIT 1、TOP (1)），驱动描述也不能声明
	// LocatesRowWithoutPrimaryKey。
	ExactRow(tableRef, where string) (exact string, ok bool)
}

// GridEditStatements 在一条连接上查表的列与方言的字面量规则，再为每个新增、改动、删除的行各
// 生成一条语句；只生成，不执行。请求里没有行时不碰数据库。
func (s *Session) GridEditStatements(ctx context.Context, database, table string, req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	writer, isWriter := s.dialect.(RowWriter)
	editor, isEditor := s.dialect.(RowEditor)
	if !isWriter || !isEditor {
		return nil, fmt.Errorf("%w: this driver does not generate grid edit statements", dbdriver.ErrUnsupported)
	}
	if len(req.Inserts)+len(req.Updates)+len(req.Deletes) == 0 {
		return &dbdriver.GridEditStatements{Inserts: []string{}, Updates: []string{}, Deletes: []string{}}, nil
	}
	return onConn(ctx, s, func(conn *sql.Conn) (*dbdriver.GridEditStatements, error) {
		columns, err := tables.TableColumns(ctx, conn, database, table)
		if err != nil {
			return nil, fmt.Errorf("query columns: %w", err)
		}
		if len(columns) == 0 {
			return nil, fmt.Errorf("table %s not found", table)
		}
		literals, err := writer.Literals(ctx, conn)
		if err != nil {
			return nil, err
		}
		byName := make(map[string]TableColumn, len(columns))
		for _, column := range columns {
			byName[column.Name] = column
		}
		g := gridSQL{
			rowSQL:  s.rowSQL(tables, writer, tables.TableRef(database, table), columns, literals),
			editor:  editor,
			columns: byName,
			table:   table,
		}
		return g.statements(req)
	})
}

// gridSQL 是写一张表的表格编辑语句所需的方言部分与表的列。
type gridSQL struct {
	rowSQL
	editor  RowEditor
	columns map[string]TableColumn
	table   string
}

func (g gridSQL) statements(req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	out := &dbdriver.GridEditStatements{
		Inserts: make([]string, 0, len(req.Inserts)),
		Updates: make([]string, 0, len(req.Updates)),
		Deletes: make([]string, 0, len(req.Deletes)),
	}
	// 定位用的列不核对：表格里的列可能有目录不列出的（SQLite 的生成列不在 table_info 里），它们照
	// 原值参与定位即可。
	keys := req.PrimaryKeys
	if len(keys) == 0 {
		keys = req.Columns
	}
	for _, insert := range req.Inserts {
		statement, err := g.insert(insert.Values)
		if err != nil {
			return nil, err
		}
		out.Inserts = append(out.Inserts, statement)
	}
	for _, update := range req.Updates {
		set, err := g.assignments(update.Set)
		if err != nil {
			return nil, err
		}
		where, err := g.locate(keys, len(req.PrimaryKeys) > 0, update.Row)
		if err != nil {
			return nil, err
		}
		out.Updates = append(out.Updates, g.update(g.tableRef, set, where))
	}
	for _, row := range req.Deletes {
		where, err := g.locate(keys, len(req.PrimaryKeys) > 0, row)
		if err != nil {
			return nil, err
		}
		out.Deletes = append(out.Deletes, g.editor.DeleteRowSQL(g.tableRef, where))
	}
	return out, nil
}

// known 确认要写的列在表里：列名对不上（表被改过、请求有误）时生成失败，而不是写出一条注定报错
// 的语句。
func (g gridSQL) known(column string) error {
	if _, ok := g.columns[column]; !ok {
		return fmt.Errorf("column %q is not in table %s", column, g.table)
	}
	return nil
}

func (g gridSQL) insert(values []dbdriver.ColumnValue) (string, error) {
	names := make([]string, len(values))
	literals := make([]string, len(values))
	identity := false
	for i, value := range values {
		if err := g.known(value.Column); err != nil {
			return "", err
		}
		literal, err := g.literal(value.Column, value.Value)
		if err != nil {
			return "", err
		}
		names[i], literals[i] = g.quote(value.Column), literal
		identity = identity || g.columns[value.Column].overridesIdentity()
	}
	return g.editor.InsertRowSQL(g.tableRef, strings.Join(names, ", "), strings.Join(literals, ", "), identity), nil
}

func (g gridSQL) assignments(values []dbdriver.ColumnValue) (string, error) {
	set := make([]string, len(values))
	for i, value := range values {
		if err := g.known(value.Column); err != nil {
			return "", err
		}
		literal, err := g.literal(value.Column, value.Value)
		if err != nil {
			return "", err
		}
		set[i] = g.quote(value.Column) + " = " + literal
	}
	return strings.Join(set, ", "), nil
}

// locate 是定位一行已有数据的条件：按主键，或没有主键时按全部列的原值；方言有物理行标识时再
// 收敛到其中一行。
func (g gridSQL) locate(keys []string, byPrimaryKey bool, row map[string]any) (string, error) {
	where, err := g.where(keys, row)
	if err != nil {
		return "", err
	}
	if byPrimaryKey {
		return where, nil
	}
	if exact, ok := g.editor.ExactRow(g.tableRef, where); ok {
		return exact, nil
	}
	return where, nil
}
