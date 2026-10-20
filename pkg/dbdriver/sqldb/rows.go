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
		types := make(map[string]string, len(columns))
		for _, column := range columns {
			types[column.Name] = column.Type
		}
		return rowSQL{
			tableRef: tables.TableRef(database, table),
			quote:    tables.QuoteIdent,
			literal:  func(column string, value any) (string, error) { return literals.Literal(value, types[column]) },
			update:   writer.UpdateRowSQL,
		}, nil
	})
}

// rowSQL 是写一张表的行语句所需的方言部分。
type rowSQL struct {
	tableRef string
	quote    func(string) string
	literal  func(column string, value any) (string, error)
	update   func(tableRef, set, where string) string
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
	if req.Kind == dbdriver.RowInsert {
		names := make([]string, len(req.Columns))
		values := make([]string, len(req.Columns))
		for i, column := range req.Columns {
			value, err := w.literal(column, row[column])
			if err != nil {
				return "", err
			}
			names[i], values[i] = w.quote(column), value
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);", w.tableRef, strings.Join(names, ", "), strings.Join(values, ", ")), nil
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
	return w.update(w.tableRef, strings.Join(set, ", "), where), nil
}

// where 是按 keys 列的值定位 row 的条件（不含关键字）：NULL 写成 IS NULL。
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
		where[i] = w.quote(column) + " = " + value
	}
	return strings.Join(where, " AND "), nil
}
