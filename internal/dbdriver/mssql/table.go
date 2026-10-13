package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/dbdriver/sqldb"
)

// 表浏览：库已由连接限定，表以 [schema].[table] 引用——不能写成两段式的 [db].[table]，
// T-SQL 会把它当成 schema.object。主键与列定义查 INFORMATION_SCHEMA（裸名按 dbo）。
var _ sqldb.Tables = dialect{}

// quoteIdent 给标识符加方括号，内部的右方括号成对转义。
func quoteIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

func (dialect) QuoteIdent(name string) string { return quoteIdent(name) }

func (dialect) TableRef(_, table string) string { return sqldb.QuoteQualified(table, quoteIdent) }

func (dialect) PrimaryKeys(ctx context.Context, conn *sql.Conn, _, table string) ([]string, error) {
	schema, tbl := splitSchemaTable(table)
	return sqldb.QueryPrimaryKeys(ctx, conn, "COLUMN_NAME",
		"SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc "+
			"JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu "+
			"ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA "+
			"WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' "+
			"AND tc.TABLE_SCHEMA = @p1 AND tc.TABLE_NAME = @p2 "+
			"ORDER BY kcu.ORDINAL_POSITION", sql.Named("p1", schema), sql.Named("p2", tbl))
}

func (dialect) TableColumns(ctx context.Context, conn *sql.Conn, _, table string) ([]sqldb.TableColumn, error) {
	schema, tbl := splitSchemaTable(table)
	return sqldb.QueryTableColumns(ctx, conn,
		"SELECT COLUMN_NAME, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT "+
			"FROM INFORMATION_SCHEMA.COLUMNS "+
			"WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2 "+
			"ORDER BY ORDINAL_POSITION", sql.Named("p1", schema), sql.Named("p2", tbl))
}

// FirstPageSQL：打开表时用 TOP 取前几行。
func (dialect) FirstPageSQL(tableRef string, limit int) string {
	return fmt.Sprintf("SELECT TOP %d * FROM %s", limit, tableRef)
}

// TablePageSQL：OFFSET / FETCH 要求 ORDER BY，没有排序键时用 (SELECT NULL) 占位。
func (dialect) TablePageSQL(sel sqldb.TableSelect) string {
	orderBy := sel.OrderBy
	if orderBy == "" {
		orderBy = "(SELECT NULL)"
	}
	return fmt.Sprintf("SELECT * FROM %s%s ORDER BY %s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY",
		sel.TableRef, sqldb.WhereClause(sel.Where), orderBy, sel.Offset, sel.Limit)
}
