package sqldb

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// Importer 是方言里「导入怎么写」的那一半：列属于哪一类，以及插入类模式（追加、复制、追加/更新、
// 追加/跳过）的 INSERT 写法。按键更新与删除、复制前的清空各方言写法相同，由 Session 生成；字面量
// 沿用 RowWriter，表引用与标识符的引号沿用 Tables。
//
// 实现 Dialect 的类型同时实现 Tables、RowWriter 与 Importer 才生成导入语句，打开表时也才给出列的
// 类别；没实现的方言，Session.ImportStatements 返回 dbdriver.ErrUnsupported。
type Importer interface {
	// ColumnCategory 按方言的类型系统判断 columnType 列的值属于哪一类（columnType 是 TableColumns
	// 给出的写法）。
	ColumnCategory(columnType string) dbdriver.ColumnCategory
	// ImportInsertSQL 是把 insert.Rows 写进表的语句，按执行的次序。
	ImportInsertSQL(insert ImportInsert) []string
}

// ImportConflict 是插入时遇到键已存在的行怎么办。
type ImportConflict int

const (
	// ConflictNone：不处理，照常插入（追加、复制）。
	ConflictNone ImportConflict = iota
	// ConflictSkip：跳过这一行（追加/跳过，以及没有可更新列的追加/更新）。
	ConflictSkip
	// ConflictUpdate：用这一行更新已有的行（追加/更新），只更新 Updates 列。
	ConflictUpdate
)

// ImportInsert 是一次导入的插入：标识符与字面量都已按方言写好。
type ImportInsert struct {
	TableRef string
	// Columns 是写入的列（已加引号），按语句里的次序。
	Columns []string
	// Keys 是匹配已有行的键列（已加引号）；ConflictNone 时可能为空。
	Keys []string
	// Updates 是 ConflictUpdate 时要更新的列（已加引号）：键列与数据库不允许更新的自增列不在其中，
	// 且不为空。
	Updates  []string
	Conflict ImportConflict
	// Rows 是各行的字面量，与 Columns 一一对应。
	Rows [][]string
	// Identity 为真表示写入的列里有数据库默认拒绝显式写值、要用方言的开关才能写的自增列
	// （TableColumn.IdentityAlways 且不是 NoIdentityOverride）。
	Identity bool
	// MaxStatementSize 大于 0 表示合并成多行 INSERT，每条不超过这么多字符；为 0 表示每行一条。
	MaxStatementSize int
	// MaxRows 大于 0 表示合并时每条最多这么多行（方言对 VALUES 的行数有上限时在 ImportInsertSQL 里
	// 设它，如 SQL Server 的 1000 行）；为 0 表示只按 MaxStatementSize 合并。
	MaxRows int
}

// Head 是 "keyword 表引用 (列, …)"。
func (ins ImportInsert) Head(keyword string) string {
	return keyword + " " + ins.TableRef + " (" + strings.Join(ins.Columns, ", ") + ")"
}

// OnConflictDoUpdate 是 " ON CONFLICT (键, …) DO UPDATE SET 列 = excluded.列, …"（PostgreSQL 与
// SQLite 的 upsert 写法）。
func (ins ImportInsert) OnConflictDoUpdate() string {
	set := make([]string, len(ins.Updates))
	for i, column := range ins.Updates {
		set[i] = column + " = excluded." + column
	}
	return " ON CONFLICT (" + strings.Join(ins.Keys, ", ") + ") DO UPDATE SET " + strings.Join(set, ", ")
}

// Values 是 "prefix (行) suffix;" 形式的 INSERT：每行一条，或按 MaxStatementSize（与 MaxRows）合并成
// 多行（"prefix (行), (行)suffix;"）。一行本身就超过上限时单独成一条。长度按字符（UTF-16 码元）计。
func (ins ImportInsert) Values(prefix, suffix string) []string {
	tuples := make([]string, len(ins.Rows))
	for i, row := range ins.Rows {
		tuples[i] = "(" + strings.Join(row, ", ") + ")"
	}
	statement := func(chunk []string) string {
		return prefix + " " + strings.Join(chunk, ", ") + suffix + ";"
	}
	if ins.MaxStatementSize <= 0 {
		statements := make([]string, len(tuples))
		for i, tuple := range tuples {
			statements[i] = statement([]string{tuple})
		}
		return statements
	}

	statements := []string{}
	var chunk []string
	fixed := utf16Len(prefix) + 1 + utf16Len(suffix) + 1
	size := 0 // chunk 里各行连同分隔符的长度
	for _, tuple := range tuples {
		grown := size + utf16Len(tuple)
		if len(chunk) > 0 {
			grown += len(", ")
		}
		if len(chunk) > 0 && (fixed+grown > ins.MaxStatementSize || (ins.MaxRows > 0 && len(chunk) == ins.MaxRows)) {
			statements = append(statements, statement(chunk))
			chunk, grown = nil, utf16Len(tuple)
		}
		chunk, size = append(chunk, tuple), grown
	}
	if len(chunk) > 0 {
		statements = append(statements, statement(chunk))
	}
	return statements
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// ImportStatements 在一条连接上查表的列与方言的字面量规则，再按导入模式生成语句；只生成，不执行。
// 没有列时不碰数据库。
func (s *Session) ImportStatements(ctx context.Context, database, table string, req dbdriver.ImportRequest) ([]string, error) {
	tables, err := s.tables()
	if err != nil {
		return nil, err
	}
	writer, isWriter := s.dialect.(RowWriter)
	importer, isImporter := s.dialect.(Importer)
	if !isWriter || !isImporter {
		return nil, fmt.Errorf("%w: this driver does not generate import statements", dbdriver.ErrUnsupported)
	}
	switch req.Mode {
	case dbdriver.ImportAppend, dbdriver.ImportUpdate, dbdriver.ImportAppendUpdate,
		dbdriver.ImportAppendSkip, dbdriver.ImportDelete, dbdriver.ImportCopy:
	default:
		return nil, fmt.Errorf("unknown import mode %q", req.Mode)
	}
	for i, row := range req.Rows {
		if len(row) != len(req.Columns) {
			return nil, fmt.Errorf("import row %d has %d values for %d columns", i+1, len(row), len(req.Columns))
		}
	}
	if len(req.Columns) == 0 {
		return []string{}, nil
	}
	return onConn(ctx, s, func(conn *sql.Conn) ([]string, error) {
		columns, err := tables.TableColumns(ctx, conn, database, table)
		if err != nil {
			return nil, fmt.Errorf("query columns: %w", err)
		}
		literals, err := writer.Literals(ctx, conn)
		if err != nil {
			return nil, err
		}
		byName := make(map[string]TableColumn, len(columns))
		for _, column := range columns {
			byName[column.Name] = column
		}
		for _, column := range req.Columns {
			if _, ok := byName[column]; !ok {
				return nil, fmt.Errorf("column %q is not in table %s", column, table)
			}
		}
		rows, err := importLiterals(req, byName, literals)
		if err != nil {
			return nil, err
		}
		plan := importPlan{
			tableRef: tables.TableRef(database, table),
			quote:    tables.QuoteIdent,
			columns:  byName,
			req:      req,
			rows:     rows,
		}
		return plan.statements(importer), nil
	})
}

// importLiterals 把各行的值按目标列写成字面量。
func importLiterals(req dbdriver.ImportRequest, columns map[string]TableColumn, literals Literals) ([][]string, error) {
	out := make([][]string, len(req.Rows))
	for i, row := range req.Rows {
		out[i] = make([]string, len(row))
		for j, value := range row {
			columnType := columns[req.Columns[j]].Type
			var literal string
			var err error
			switch value.Kind {
			case dbdriver.ImportNull:
				literal = "NULL"
			case dbdriver.ImportText:
				literal, err = literals.Literal(value.Text, columnType)
			case dbdriver.ImportBinary:
				var b []byte
				if b, err = hex.DecodeString(value.Hex); err != nil {
					return nil, fmt.Errorf("import row %d column %q: binary value is not hex: %w", i+1, req.Columns[j], err)
				}
				literal, err = literals.Literal(b, columnType)
			default:
				return nil, fmt.Errorf("import row %d column %q: unknown value kind %q", i+1, req.Columns[j], value.Kind)
			}
			if err != nil {
				return nil, err
			}
			out[i][j] = literal
		}
	}
	return out, nil
}

// importPlan 是一次导入在表上的全部输入：列已核对、值已写成字面量。
type importPlan struct {
	tableRef string
	quote    func(string) string
	columns  map[string]TableColumn
	req      dbdriver.ImportRequest
	rows     [][]string
}

func (p importPlan) statements(importer Importer) []string {
	var keys, updates []int // 列在 req.Columns 里的下标
	identity := false
	for i, column := range p.req.Columns {
		identity = identity || p.columns[column].overridesIdentity()
		switch {
		case slices.Contains(p.req.Keys, column):
			keys = append(keys, i)
		case !p.columns[column].IdentityAlways:
			updates = append(updates, i)
		}
	}

	switch p.req.Mode {
	case dbdriver.ImportUpdate:
		if len(keys) == 0 || len(updates) == 0 {
			return []string{}
		}
		statements := make([]string, len(p.rows))
		for r, row := range p.rows {
			set := make([]string, len(updates))
			for i, c := range updates {
				set[i] = p.quote(p.req.Columns[c]) + " = " + row[c]
			}
			statements[r] = fmt.Sprintf("UPDATE %s SET %s WHERE %s;", p.tableRef, strings.Join(set, ", "), p.where(keys, row))
		}
		return statements
	case dbdriver.ImportDelete:
		if len(keys) == 0 {
			return []string{}
		}
		statements := make([]string, len(p.rows))
		for r, row := range p.rows {
			statements[r] = fmt.Sprintf("DELETE FROM %s WHERE %s;", p.tableRef, p.where(keys, row))
		}
		return statements
	}

	insert := ImportInsert{
		TableRef: p.tableRef,
		Columns:  p.quoted(nil),
		Rows:     p.rows,
		Identity: identity,
	}
	if p.req.ExtendedInsert {
		insert.MaxStatementSize = int(max(1, p.req.MaxStatementSizeKB) * 1024)
	}
	switch p.req.Mode {
	case dbdriver.ImportAppendUpdate, dbdriver.ImportAppendSkip:
		if len(keys) == 0 {
			return []string{}
		}
		insert.Keys = p.quoted(keys)
		insert.Conflict = ConflictSkip
		if p.req.Mode == dbdriver.ImportAppendUpdate && len(updates) > 0 {
			insert.Conflict, insert.Updates = ConflictUpdate, p.quoted(updates)
		}
	}
	statements := importer.ImportInsertSQL(insert)
	if p.req.Mode == dbdriver.ImportCopy {
		statements = append([]string{"DELETE FROM " + p.tableRef + ";"}, statements...)
	}
	return statements
}

// quoted 是下标对应的列（已加引号）；indexes 为 nil 表示全部列。
func (p importPlan) quoted(indexes []int) []string {
	if indexes == nil {
		out := make([]string, len(p.req.Columns))
		for i, column := range p.req.Columns {
			out[i] = p.quote(column)
		}
		return out
	}
	out := make([]string, len(indexes))
	for i, c := range indexes {
		out[i] = p.quote(p.req.Columns[c])
	}
	return out
}

// where 是按键列的值定位一行的条件（不含关键字）：NULL 写成 IS NULL（字面量 NULL 只来自空值，
// 文本都带引号）。
func (p importPlan) where(keys []int, row []string) string {
	where := make([]string, len(keys))
	for i, c := range keys {
		column := p.quote(p.req.Columns[c])
		if row[c] == "NULL" {
			where[i] = column + " IS NULL"
			continue
		}
		where[i] = column + " = " + row[c]
	}
	return strings.Join(where, " AND ")
}
