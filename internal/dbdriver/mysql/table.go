package mysql

import (
	"context"
	"database/sql"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 表浏览：表以 `db`.`table` 引用，主键与列定义用 SHOW KEYS / SHOW FULL COLUMNS（带列注释），分页用共用的
// LIMIT / OFFSET。
var _ sqldb.Tables = dialect{}

func (dialect) QuoteIdent(name string) string { return quoteIdent(name) }

func (dialect) TableRef(database, table string) string { return quoteTableRef(database, table) }

func (dialect) PrimaryKeys(ctx context.Context, conn *sql.Conn, database, table string) ([]string, error) {
	return sqldb.QueryPrimaryKeys(ctx, conn, "Column_name",
		"SHOW KEYS FROM "+quoteTableRef(database, table)+" WHERE Key_name = 'PRIMARY'")
}

func (dialect) TableColumns(ctx context.Context, conn *sql.Conn, database, table string) ([]sqldb.TableColumn, error) {
	return sqldb.QueryTableColumns(ctx, conn, "SHOW FULL COLUMNS FROM "+quoteTableRef(database, table))
}
