package sqlite

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 表浏览：库名是 schema 名，有值时表以 "schema"."table" 引用，为空时只引用表名（没有 schema
// 分组，带点的名字整体是表名）；主键与列定义用 pragma_table_info，分页用共用的 LIMIT / OFFSET。
var _ sqldb.Tables = dialect{}

func (dialect) QuoteIdent(name string) string { return quoteIdent(name) }

func (dialect) TableRef(database, table string) string {
	return qualify(database, quoteIdent(table))
}

func (dialect) PrimaryKeys(ctx context.Context, conn *sql.Conn, database, table string) ([]string, error) {
	return sqldb.QueryPrimaryKeys(ctx, conn, "name",
		"SELECT name FROM "+qualify(database, "pragma_table_info(?)")+" WHERE pk > 0 ORDER BY pk", table)
}

func (dialect) TableColumns(ctx context.Context, conn *sql.Conn, database, table string) ([]sqldb.TableColumn, error) {
	return sqldb.QueryTableColumns(ctx, conn,
		`SELECT name, type, CASE "notnull" WHEN 0 THEN 'YES' ELSE 'NO' END AS is_nullable, dflt_value FROM `+
			qualify(database, "pragma_table_info(?)"), table)
}
