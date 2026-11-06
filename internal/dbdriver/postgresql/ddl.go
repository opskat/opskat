package postgresql

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 建表语句：PostgreSQL 不存原文，从系统目录重建。表名是 "schema.table"（裸名按 public），
// 列与主键都按表真正所在的 schema 查，语句里的表名也带这个 schema。列类型用 format_type
// 取数据库自己的完整写法（长度、精度），pg_catalog 之外的类型一律带它所在的 schema——
// format_type 只给 search_path 外的类型加 schema，在 search_path 内的由这里补上，结果不随
// 会话的 search_path 变化。默认值取 information_schema（生成列没有默认值），各版本通用。
// 视图输出其定义（CREATE VIEW）。
var _ sqldb.DDL = dialect{}

const ddlColumnsQuery = "SELECT a.attname, " +
	"CASE WHEN tn.nspname = 'pg_catalog' OR NOT pg_type_is_visible(a.atttypid) " +
	"THEN format_type(a.atttypid, a.atttypmod) " +
	"ELSE quote_ident(tn.nspname) || '.' || format_type(a.atttypid, a.atttypmod) END, " +
	"NOT a.attnotnull, col.column_default, c.relkind::text " +
	"FROM pg_class c " +
	"JOIN pg_namespace n ON n.oid = c.relnamespace " +
	"JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped " +
	"JOIN pg_type t ON t.oid = a.atttypid " +
	"JOIN pg_namespace tn ON tn.oid = t.typnamespace " +
	"LEFT JOIN information_schema.columns col " +
	"ON col.table_schema = n.nspname AND col.table_name = c.relname AND col.column_name = a.attname " +
	"WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'v') " +
	"ORDER BY a.attnum"

// viewDefQuery 取视图定义：建表语句与对象源码共用。format('%I.%I', ...) 给标识符加引号，
// 混合大小写或带特殊字符的视图名才能解析到正确的 regclass。format 的参数是 variadic "any"，
// 占位符必须显式 ::text，否则服务端报 could not determine data type of parameter（42P18）。
const viewDefQuery = "SELECT pg_get_viewdef(format('%I.%I', $1::text, $2::text)::regclass, true)"

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
	if relkind == "v" {
		var def string
		if err := conn.QueryRowContext(ctx, viewDefQuery, schema, tbl).Scan(&def); err != nil {
			return "", err
		}
		// 美化输出的定义以空白开头，去掉后接在 AS 之后。
		return "CREATE VIEW " + tableRef + " AS " + strings.TrimLeft(def, " \t\n"), nil
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
