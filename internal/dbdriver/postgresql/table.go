package postgresql

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 表浏览：库已由连接限定，表以 "schema"."table" 引用（裸名不加 schema，由 search_path 解析），
// 主键与列定义查 information_schema（裸名按 public），分页用共用的 LIMIT / OFFSET。
var _ sqldb.Tables = dialect{}

// quoteIdent 给标识符加双引号，内部的双引号成对转义。
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (dialect) QuoteIdent(name string) string { return quoteIdent(name) }

func (dialect) TableRef(_, table string) string { return sqldb.QuoteQualified(table, quoteIdent) }

func (dialect) PrimaryKeys(ctx context.Context, conn *sql.Conn, _, table string) ([]string, error) {
	schema, tbl := splitSchemaTable(table)
	return sqldb.QueryPrimaryKeys(ctx, conn, "column_name",
		"SELECT kcu.column_name FROM information_schema.table_constraints tc "+
			"JOIN information_schema.key_column_usage kcu "+
			"ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema "+
			"WHERE tc.table_schema = $1"+
			" AND tc.table_name = $2"+
			" AND tc.constraint_type = 'PRIMARY KEY' ORDER BY kcu.ordinal_position", schema, tbl)
}

func (dialect) TableColumns(ctx context.Context, conn *sql.Conn, _, table string) ([]sqldb.TableColumn, error) {
	schema, tbl := splitSchemaTable(table)
	return sqldb.QueryTableColumns(ctx, conn, columnsQuery, schema, tbl)
}

// columnsQuery 按 schema 与表名读列定义，TableColumns 与建表语句共用。
const columnsQuery = "SELECT column_name, data_type, udt_name, is_nullable, column_default " +
	"FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position"
