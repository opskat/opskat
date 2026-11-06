package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// 表浏览：库已由连接限定，表以 [schema].[table] 引用——不能写成两段式的 [db].[table]，
// T-SQL 会把它当成 schema.object。主键与列定义查 INFORMATION_SCHEMA（裸名按 dbo）。
var _ sqldb.Tables = dialect{}

// quoteIdent 给标识符加方括号，内部的右方括号成对转义。
func quoteIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

func (dialect) QuoteIdent(name string) string { return quoteIdent(name) }

func (dialect) TableRef(_, table string) string { return sqldb.QuoteSchemaTable(table, quoteIdent) }

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

// 列注释存在 sys.extended_properties，minor_id 是 column_id；ORDINAL_POSITION 在删过列后会与它错开，
// 所以按列名取 ColumnId 来对。IDENTITY 列是自增，也是 IdentityAlways（不能 UPDATE，显式插入要
// IDENTITY_INSERT）；视图的列在 COLUMNPROPERTY 里同样报告为 IsIdentity，但对视图 SET
// IDENTITY_INSERT 报 8105、盖住真实的错误，所以视图的 IDENTITY 列另置 NoIdentityOverride
// （OBJECT_ID 的 N'V'），插入按普通插入写。
func (dialect) TableColumns(ctx context.Context, conn *sql.Conn, _, table string) ([]sqldb.TableColumn, error) {
	schema, tbl := splitSchemaTable(table)
	return sqldb.QueryTableColumns(ctx, conn,
		"SELECT c.COLUMN_NAME, c.DATA_TYPE, c.IS_NULLABLE, c.COLUMN_DEFAULT, "+
			"CAST(ep.value AS NVARCHAR(MAX)) AS COLUMN_COMMENT, "+
			"COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)), c.COLUMN_NAME, 'IsIdentity') AS IS_AUTO_INCREMENT, "+
			"COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)), c.COLUMN_NAME, 'IsIdentity') AS IS_IDENTITY_ALWAYS, "+
			"COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME), N'V'), c.COLUMN_NAME, 'IsIdentity') AS IS_NO_IDENTITY_OVERRIDE "+
			"FROM INFORMATION_SCHEMA.COLUMNS c "+
			"LEFT JOIN sys.extended_properties ep ON ep.class = 1 AND ep.name = 'MS_Description' "+
			"AND ep.major_id = OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)) "+
			"AND ep.minor_id = COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)), c.COLUMN_NAME, 'ColumnId') "+
			"WHERE c.TABLE_SCHEMA = @p1 AND c.TABLE_NAME = @p2 "+
			"ORDER BY c.ORDINAL_POSITION", sql.Named("p1", schema), sql.Named("p2", tbl))
}

// FirstPageSQL：打开表时用 TOP 取前几行。
func (dialect) FirstPageSQL(tableRef string, limit int) string {
	return fmt.Sprintf("SELECT TOP %d * FROM %s", limit, tableRef)
}

// StarterSQL：起始查询语句同样用 TOP。
func (dialect) StarterSQL(tableRef string, limit int) string {
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
