package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// dialect 是 MySQL 的方言：分页用共用的 LIMIT / OFFSET，浏览用 information_schema 与 SHOW。
// 库名写在连接串里，会话所连的库就是 DATABASE()。
type dialect struct{ sqldb.LimitOffset }

var _ sqldb.Catalog = dialect{}

func (dialect) Descriptor() dbdriver.Descriptor { return descriptor }

// quoteIdent 给标识符加反引号，内部的反引号成对转义。
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// quoteTableRef 拼成 `db`.`table`。
func quoteTableRef(database, table string) string {
	return quoteIdent(database) + "." + quoteIdent(table)
}

func (dialect) ListDatabases(ctx context.Context, conn *sql.Conn) ([]string, error) {
	return sqldb.QueryNames(ctx, conn, "SHOW DATABASES")
}

func (dialect) ListTables(ctx context.Context, conn *sql.Conn, database string) ([]string, error) {
	return sqldb.QueryNames(ctx, conn, "SHOW TABLES FROM "+quoteIdent(database))
}

func (d dialect) TableMetadata(ctx context.Context, conn *sql.Conn, database, table string) (*dbdriver.TableMetadata, error) {
	columns, err := d.TableColumns(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("columns", err)
	}
	primaryKeys, err := d.PrimaryKeys(ctx, conn, database, table)
	if err != nil {
		return nil, sqldb.Wrap("primary keys", err)
	}
	indexes, err := d.indexes(ctx, conn, table)
	if err != nil {
		return nil, sqldb.Wrap("indexes", err)
	}
	foreignKeys, err := d.foreignKeys(ctx, conn, table)
	if err != nil {
		return nil, sqldb.Wrap("foreign keys", err)
	}
	return sqldb.AssembleMetadata(sqldb.ColumnMetas(columns), primaryKeys, indexes, foreignKeys), nil
}

// 表达式（函数）索引（MySQL 8.0.13 起）的键在 STATISTICS 里 COLUMN_NAME 为 NULL、表达式在 EXPRESSION 列。
// EXPRESSION 列在 5.7 上不存在，所以只在读到 NULL 列名时（只有 8.0.13 起会有）再查一次表达式。
const expressionIndexPartsSQL = "SELECT INDEX_NAME, SEQ_IN_INDEX, EXPRESSION FROM information_schema.STATISTICS " +
	"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME IS NULL ORDER BY INDEX_NAME, SEQ_IN_INDEX"

// indexPart 是 STATISTICS 的一行：索引的一个键。
type indexPart struct {
	name   string
	unique bool
	column sql.NullString
}

func (dialect) indexes(ctx context.Context, conn *sql.Conn, table string) ([]dbdriver.IndexMeta, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX", table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var parts []indexPart
	hasExpressions := false
	for rows.Next() {
		var part indexPart
		var nonUnique int
		if err := rows.Scan(&part.name, &nonUnique, &part.column); err != nil {
			return nil, err
		}
		part.unique = nonUnique == 0
		hasExpressions = hasExpressions || !part.column.Valid
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var expressions map[string]map[int]string
	if hasExpressions {
		if expressions, err = expressionIndexParts(ctx, conn, table); err != nil {
			return nil, err
		}
	}

	var indexes sqldb.IndexBuilder
	seq := map[string]int{}
	for _, part := range parts {
		seq[part.name]++
		column := part.column.String
		if !part.column.Valid {
			expression, ok := expressions[part.name][seq[part.name]]
			if !ok {
				return nil, fmt.Errorf("index %q: key part %d has neither a column nor an expression", part.name, seq[part.name])
			}
			column = expression
		}
		indexes.Add(part.name, part.unique, column)
	}
	return indexes.Indexes(), nil
}

// expressionIndexParts 读表达式键：索引名 → 键在索引里的序号（从 1 起）→ 表达式原文。
func expressionIndexParts(ctx context.Context, conn *sql.Conn, table string) (map[string]map[int]string, error) {
	records, err := sqldb.QueryRecords(ctx, conn, expressionIndexPartsSQL, table)
	if err != nil {
		return nil, err
	}
	out := map[string]map[int]string{}
	for _, record := range records {
		name := sqldb.PickString(record, "INDEX_NAME")
		seq, err := strconv.Atoi(sqldb.PickString(record, "SEQ_IN_INDEX"))
		if err != nil {
			return nil, fmt.Errorf("index %q: key part number: %w", name, err)
		}
		if out[name] == nil {
			out[name] = map[int]string{}
		}
		out[name][seq] = sqldb.PickString(record, "EXPRESSION")
	}
	return out, nil
}

// foreignKeysSQL 读外键的列映射，并带上 ON UPDATE / ON DELETE 的动作（最后两列，见 sqldb.QueryForeignKeys）。
const foreignKeysSQL = "SELECT k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE " +
	"FROM information_schema.KEY_COLUMN_USAGE k " +
	"LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME " +
	"WHERE k.TABLE_SCHEMA = DATABASE() AND k.TABLE_NAME = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL " +
	"ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION"

func (dialect) foreignKeys(ctx context.Context, conn *sql.Conn, table string) ([]dbdriver.ForeignKeyMeta, error) {
	return sqldb.QueryForeignKeys(ctx, conn, foreignKeysSQL, table)
}

func (dialect) ListObjects(ctx context.Context, conn *sql.Conn, _ string) (*dbdriver.DatabaseObjects, error) {
	out := &dbdriver.DatabaseObjects{}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT TABLE_NAME FROM information_schema.VIEWS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME",
		dbdriver.ObjectView, &out.Views); err != nil {
		return nil, err
	}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = 'PROCEDURE' ORDER BY ROUTINE_NAME",
		dbdriver.ObjectProcedure, &out.Procedures); err != nil {
		return nil, err
	}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = 'FUNCTION' ORDER BY ROUTINE_NAME",
		dbdriver.ObjectFunction, &out.Functions); err != nil {
		return nil, err
	}
	if err := sqldb.CollectObjects(ctx, conn,
		"SELECT TRIGGER_NAME FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY TRIGGER_NAME",
		dbdriver.ObjectTrigger, &out.Triggers); err != nil {
		return nil, err
	}
	return out, nil
}

// createStatements 是各类对象取定义用的 SHOW CREATE 语句前缀。
var createStatements = map[string]string{
	dbdriver.ObjectView:      "SHOW CREATE VIEW ",
	dbdriver.ObjectProcedure: "SHOW CREATE PROCEDURE ",
	dbdriver.ObjectFunction:  "SHOW CREATE FUNCTION ",
	dbdriver.ObjectTrigger:   "SHOW CREATE TRIGGER ",
}

func (dialect) ObjectSource(ctx context.Context, conn *sql.Conn, _, objType, name, _ string) (string, error) {
	rows, err := conn.QueryContext(ctx, createStatements[objType]+quoteIdent(name))
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("object not found: %s", name)
	}
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	// SHOW CREATE ... 把 DDL 放在名为 "Create <Type>" 的列里。
	row := sqldb.ZipRow(cols, values)
	for _, k := range []string{"Create View", "Create Procedure", "Create Function", "Create Trigger", "SQL Original Statement"} {
		if s := sqldb.PickString(row, k); s != "" {
			return s, nil
		}
	}
	// 退而求其次：最后一列按惯例是定义。
	return sqldb.PickString(row, cols[len(cols)-1]), nil
}
