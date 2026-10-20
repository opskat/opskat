package mysql

import (
	"context"
	"database/sql"
	"fmt"
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

func (dialect) indexes(ctx context.Context, conn *sql.Conn, table string) ([]dbdriver.IndexMeta, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX", table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var indexes sqldb.IndexBuilder
	for rows.Next() {
		var name, col string
		var nonUnique int
		if err := rows.Scan(&name, &nonUnique, &col); err != nil {
			return nil, err
		}
		indexes.Add(name, nonUnique == 0, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return indexes.Indexes(), nil
}

func (dialect) foreignKeys(ctx context.Context, conn *sql.Conn, table string) ([]dbdriver.ForeignKeyMeta, error) {
	return sqldb.QueryForeignKeys(ctx, conn,
		"SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME "+
			"FROM information_schema.KEY_COLUMN_USAGE "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL "+
			"ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION", table)
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
