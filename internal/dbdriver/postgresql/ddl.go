package postgresql

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 建表语句：PostgreSQL 不存原文，从系统目录重建。表名是 "schema.table"（裸名按 public），
// 列与主键都按表真正所在的 schema 查，语句里的表名也带这个 schema。列类型用 format_type
// 取数据库自己的完整写法（长度、精度、不在 search_path 内的自定义类型带 schema）。
// 视图输出其定义（CREATE VIEW），物化视图输出 CREATE MATERIALIZED VIEW。
var _ sqldb.DDL = dialect{}

const ddlColumnsQuery = "SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull, " +
	"CASE WHEN a.attgenerated = '' THEN pg_get_expr(d.adbin, d.adrelid) END, c.relkind::text " +
	"FROM pg_class c " +
	"JOIN pg_namespace n ON n.oid = c.relnamespace " +
	"JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped " +
	"LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum " +
	"WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'f', 'v', 'm') " +
	"ORDER BY a.attnum"

const viewDefQuery = "SELECT pg_get_viewdef(format('%I.%I', $1, $2)::regclass, true)"

func (d dialect) TableDDL(ctx context.Context, conn *sql.Conn, database, table string) (string, error) {
	schema, tbl := splitSchemaTable(table)
	columns, relkind, err := queryDDLColumns(ctx, conn, schema, tbl)
	if err != nil {
		return "", err
	}
	if len(columns) == 0 {
		return "", nil
	}
	tableRef := quoteIdent(schema) + "." + quoteIdent(tbl)
	if relkind == "v" || relkind == "m" {
		var def string
		if err := conn.QueryRowContext(ctx, viewDefQuery, schema, tbl).Scan(&def); err != nil {
			return "", err
		}
		keyword := "VIEW"
		if relkind == "m" {
			keyword = "MATERIALIZED VIEW"
		}
		return "CREATE " + keyword + " " + tableRef + " AS " + def, nil
	}
	primaryKeys, err := d.PrimaryKeys(ctx, conn, database, table)
	if err != nil {
		return "", err
	}
	return sqldb.BuildCreateTable(tableRef, columns, primaryKeys, quoteIdent), nil
}

// queryDDLColumns 读一张表 / 视图的列与它的 relkind；对象不存在时列为空。
func queryDDLColumns(ctx context.Context, conn *sql.Conn, schema, table string) ([]sqldb.DDLColumn, string, error) {
	rows, err := conn.QueryContext(ctx, ddlColumnsQuery, schema, table)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	var columns []sqldb.DDLColumn
	var relkind string
	for rows.Next() {
		var name, dataType string
		var nullable bool
		var columnDefault sql.NullString
		if err := rows.Scan(&name, &dataType, &nullable, &columnDefault, &relkind); err != nil {
			return nil, "", err
		}
		columns = append(columns, sqldb.DDLColumn{
			Name: name, Type: dataType, Nullable: nullable, Default: columnDefault.String,
		})
	}
	return columns, relkind, rows.Err()
}
