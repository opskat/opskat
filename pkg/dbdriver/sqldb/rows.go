package sqldb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// RowWriter 是方言里「怎么把行写进表」的那一半：字面量的写法与只改一行的 UPDATE。表引用与
// 标识符的引号沿用 Tables。
//
// 实现 Dialect 的类型同时实现 Tables 与 RowWriter 才生成行语句；没实现的方言，
// Session.PrepareRowStatements 返回 dbdriver.ErrUnsupported。
type RowWriter interface {
	// Literals 返回在这条连接上写字面量的规则（规则随会话设置变化的方言在这里查，如 MySQL
	// 的 sql_mode）。
	Literals(ctx context.Context, conn *sql.Conn) (Literals, error)
	// UpdateRowSQL 是只改一行的 UPDATE 语句（含末尾分号）。set / where 已写好，不含关键字。
	UpdateRowSQL(tableRef, set, where string) string
}

// EqualityComparer 是方言里「按原值定位一行时，一列怎么与它的原值比较」：表格编辑在没有主键时、
// 「复制为 UPDATE」都按原值定位。没实现的方言写成 col = 字面量；有的类型不能用 = 比较的方言（Oracle
// 的 LOB，报 ORA-00932）在这里换成别的写法，如 DBMS_LOB.COMPARE(col, 字面量) = 0。原值是 NULL 的
// 列不经过它，一律写成 col IS NULL。
type EqualityComparer interface {
	// EqualitySQL 是 column（已加引号）等于 literal（已按列类型写好、不是 NULL 的字面量）的条件。
	// columnType 是这一列的类型文本（TableColumn.Type）；表的列里没有这一列时为空串。
	EqualitySQL(columnType, column, literal string) string
}

// InsertRowWriter 是方言里「某一行的 INSERT 要不要整条自己写」：默认的 INSERT INTO t (列) VALUES (值);
// 放不下的行（Oracle 放不进一条字面量的 LOB，要写成先插入空 LOB 再分段追加的 PL/SQL 块，以单独
// 一行 / 结尾）由它写出完整语句。只用于行语句生成器（RowStatementWriter：转储、复制为 INSERT、
// 导出为 SQL）的 RowInsert；RowUpdate 与表格编辑（RowEditor）不经过它。内置驱动都不实现。
type InsertRowWriter interface {
	// RowInsertStatement 为一行写出完整语句（自带结尾的分号或 /）；ok 为假表示这一行不用它，回落到默认
	// INSERT。返回 error 时整个请求失败。
	RowInsertStatement(row InsertRow) (statement string, ok bool, err error)
}

// InsertRow 是 InsertRowWriter 写一行语句要的全部：表引用、列（按语句里的次序，带类型与值）、
// 引号与字面量规则。
type InsertRow struct {
	// TableRef 是已按 RowStatementsRequest.OmitDatabase 取好的表引用。
	TableRef string
	// Columns 是写入的列，次序同 RowStatementsRequest.Columns；行里没有的列 Value 为 nil。
	Columns []InsertColumn
	// Modifier 是 RowStatementsRequest.InsertModifier（可为空）：默认 INSERT 把它写在列与 VALUES 之间；
	// 自己写整条语句的方言自行决定是否适用。
	Modifier string
	// Quote 给标识符加引号（Tables.QuoteIdent）。
	Quote func(string) string
	// Literals 是这条连接上的字面量规则。
	Literals Literals
}

// InsertColumn 是 InsertRow 里的一列。
type InsertColumn struct {
	Name string
	// Type 是列类型文本（TableColumn.Type）；表的列里没有这一列时为空串。
	Type string
	// Value 是这一行这一列的值，含义同 RowStatementsRequest.Rows。
	Value any
}

// PrepareRowStatements 在一条连接上查表的列类型与方言的字面量规则，用完即还；交回的 writer
// 只按它们生成语句，不再碰数据库。
func (s *Session) PrepareRowStatements(ctx context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	writer, ok := s.dialect.(RowWriter)
	if !ok {
		return nil, fmt.Errorf("%w: this driver does not generate row statements", dbdriver.ErrUnsupported)
	}
	return onConn(ctx, s, func(conn *sql.Conn) (dbdriver.RowStatementWriter, error) {
		columns, err := tables.TableColumns(ctx, conn, database, table)
		if err != nil {
			return nil, fmt.Errorf("query columns: %w", err)
		}
		literals, err := writer.Literals(ctx, conn)
		if err != nil {
			return nil, err
		}
		w := s.rowSQL(tables, writer, tables.TableRef(database, table), columns, literals)
		w.bareRef = tables.TableRef("", table)
		return w, nil
	})
}

// rowSQL 是在 tableRef 这张表上按会话方言写行语句：字面量按 columns 里的列类型写，按原值定位时
// 一列怎么比较取自 EqualityComparer。
func (s *Session) rowSQL(tables Tables, writer RowWriter, tableRef string, columns []TableColumn, literals Literals) rowSQL {
	types := make(map[string]string, len(columns))
	for _, column := range columns {
		types[column.Name] = column.Type
	}
	equality := func(_, column, literal string) string { return column + " = " + literal }
	if comparer, ok := s.dialect.(EqualityComparer); ok {
		equality = comparer.EqualitySQL
	}
	inserter, _ := s.dialect.(InsertRowWriter)
	return rowSQL{
		inserter: inserter,
		tableRef: tableRef,
		quote:    tables.QuoteIdent,
		literals: literals,
		types:    types,
		equality: equality,
		update:   writer.UpdateRowSQL,
	}
}

// rowSQL 是写一张表的行语句所需的方言部分。
type rowSQL struct {
	tableRef string
	// bareRef 是不带库名的表引用（RowStatementsRequest.OmitDatabase）。
	bareRef string
	// inserter 非空时先问它某一行的 INSERT 要不要整条自己写。
	inserter InsertRowWriter
	quote    func(string) string
	literals Literals
	// types 是列名到列类型文本；表的列里没有的列不在其中。
	types    map[string]string
	equality func(columnType, column, literal string) string
	update   func(tableRef, set, where string) string
}

// literal 把 column 列的一个值按列类型写成字面量。
func (w rowSQL) literal(column string, value any) (string, error) {
	return w.literals.Literal(value, w.types[column])
}

// RowStatements 为每行生成一条语句，只生成，不碰数据库。
func (w rowSQL) RowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	if req.Kind != dbdriver.RowInsert && req.Kind != dbdriver.RowUpdate {
		return nil, fmt.Errorf("unknown row statement kind %q", req.Kind)
	}
	statements := make([]string, 0, len(req.Rows))
	for _, row := range req.Rows {
		statement, err := w.statement(req, row)
		if err != nil {
			return nil, err
		}
		statements = append(statements, statement)
	}
	return statements, nil
}

func (w rowSQL) statement(req dbdriver.RowStatementsRequest, row map[string]any) (string, error) {
	tableRef := w.tableRef
	if req.OmitDatabase {
		tableRef = w.bareRef
	}
	if req.Kind == dbdriver.RowInsert {
		if w.inserter != nil {
			columns := make([]InsertColumn, len(req.Columns))
			for i, column := range req.Columns {
				columns[i] = InsertColumn{Name: column, Type: w.types[column], Value: row[column]}
			}
			statement, ok, err := w.inserter.RowInsertStatement(InsertRow{
				TableRef: tableRef, Columns: columns, Modifier: req.InsertModifier, Quote: w.quote, Literals: w.literals,
			})
			if err != nil || ok {
				return statement, err
			}
		}
		names := make([]string, len(req.Columns))
		values := make([]string, len(req.Columns))
		for i, column := range req.Columns {
			value, err := w.literal(column, row[column])
			if err != nil {
				return "", err
			}
			names[i], values[i] = w.quote(column), value
		}
		modifier := ""
		if req.InsertModifier != "" {
			modifier = " " + req.InsertModifier
		}
		return fmt.Sprintf("INSERT INTO %s (%s)%s VALUES (%s);", tableRef, strings.Join(names, ", "), modifier, strings.Join(values, ", ")), nil
	}

	set := make([]string, len(req.Columns))
	for i, column := range req.Columns {
		value, err := w.literal(column, row[column])
		if err != nil {
			return "", err
		}
		set[i] = w.quote(column) + " = " + value
	}
	keys := req.PrimaryKeys
	if len(keys) == 0 {
		keys = req.Columns
	}
	where, err := w.where(keys, row)
	if err != nil {
		return "", err
	}
	return w.update(tableRef, strings.Join(set, ", "), where), nil
}

// where 是按 keys 列的值定位 row 的条件（不含关键字）：NULL 写成 IS NULL，其余按方言的相等比较
// （见 EqualityComparer）。
func (w rowSQL) where(keys []string, row map[string]any) (string, error) {
	where := make([]string, len(keys))
	for i, column := range keys {
		if row[column] == nil {
			where[i] = w.quote(column) + " IS NULL"
			continue
		}
		value, err := w.literal(column, row[column])
		if err != nil {
			return "", err
		}
		where[i] = w.equality(w.types[column], w.quote(column), value)
	}
	return strings.Join(where, " AND "), nil
}
