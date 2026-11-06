package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb/sqldbtest"
)

const (
	primaryKeysSQL = "SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc " +
		"JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu " +
		"ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA " +
		"WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' " +
		"AND tc.TABLE_SCHEMA = @p1 AND tc.TABLE_NAME = @p2 " +
		"ORDER BY kcu.ORDINAL_POSITION"
	columnsSQL = "SELECT c.COLUMN_NAME, c.DATA_TYPE, c.IS_NULLABLE, c.COLUMN_DEFAULT, " +
		"CAST(ep.value AS NVARCHAR(MAX)) AS COLUMN_COMMENT, " +
		"COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)), c.COLUMN_NAME, 'IsIdentity') AS IS_AUTO_INCREMENT, " +
		"COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)), c.COLUMN_NAME, 'IsIdentity') AS IS_IDENTITY_ALWAYS, " +
		"COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME), N'V'), c.COLUMN_NAME, 'IsIdentity') AS IS_NO_IDENTITY_OVERRIDE " +
		"FROM INFORMATION_SCHEMA.COLUMNS c " +
		"LEFT JOIN sys.extended_properties ep ON ep.class = 1 AND ep.name = 'MS_Description' " +
		"AND ep.major_id = OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)) " +
		"AND ep.minor_id = COLUMNPROPERTY(OBJECT_ID(QUOTENAME(c.TABLE_SCHEMA) + '.' + QUOTENAME(c.TABLE_NAME)), c.COLUMN_NAME, 'ColumnId') " +
		"WHERE c.TABLE_SCHEMA = @p1 AND c.TABLE_NAME = @p2 " +
		"ORDER BY c.ORDINAL_POSITION"
)

// 表标识来自侧边栏的 schema.table（如 "dbo.users"）：按 schema + table 两段过滤
// INFORMATION_SCHEMA，避免同名表跨 schema 时列 / 主键混淆；取数用 [schema].[table]，
// 库名不进语句（[db].[table] 会被当成 schema.object）。第一页用 TOP。
func TestOpenTable(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery(primaryKeysSQL).WithArgs("dbo", "users").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("id"))
	mock.ExpectQuery(columnsSQL).WithArgs("dbo", "users").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "IS_AUTO_INCREMENT", "IS_IDENTITY_ALWAYS"}).
			AddRow("id", "int", "NO", nil, 1, 1).
			AddRow("name", "varchar", "YES", "('anon')", 0, 0))
	mock.ExpectQuery("SELECT COUNT(*) FROM [dbo].[users]").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
	mock.ExpectQuery("SELECT TOP 10 * FROM [dbo].[users]").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice").AddRow(2, "bob"))

	opened, err := session.OpenTable(context.Background(), "appdb", "dbo.users", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	assert.Equal(t, []string{"id", "name"}, opened.FirstPage.Columns)
	assert.Equal(t, map[string]string{"id": "int", "name": "varchar"}, opened.ColumnTypes)
	assert.Equal(t, []dbdriver.TableColumnRule{
		{Name: "id", AutoIncrement: true},
		{Name: "name", Nullable: true, HasDefault: true},
	}, opened.ColumnRules)
	assert.Equal(t, int64(2), opened.TotalCount)
	assert.Len(t, opened.FirstPage.Rows, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 裸表名：元数据按 dbo 查，取数不加 schema。
func TestOpenTableBareNameDefaultsToDbo(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery(primaryKeysSQL).WithArgs("dbo", "users").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("id"))
	mock.ExpectQuery(columnsSQL).WithArgs("dbo", "users").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT"}).
			AddRow("id", "int", "NO", nil))
	mock.ExpectQuery("SELECT COUNT(*) FROM [users]").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	mock.ExpectQuery("SELECT TOP 10 * FROM [users]").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	opened, err := session.OpenTable(context.Background(), "appdb", "users", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	assert.Empty(t, opened.FirstPage.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 翻页：没有 LIMIT，用 OFFSET ... ROWS FETCH NEXT ... ROWS ONLY；这个语法要求 ORDER BY，
// 没有排序键时用 (SELECT NULL) 占位。标识符加方括号（内部的右方括号成对转义）。
func TestTableRows(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	row := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id"}).AddRow(1) }
	filter := []dbdriver.FilterItem{
		{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "a]b", Operator: dbdriver.OpEqual, Value: "O'Reilly"},
	}

	mock.ExpectQuery("SELECT * FROM [dbo].[t] ORDER BY (SELECT NULL) OFFSET 0 ROWS FETCH NEXT 50 ROWS ONLY").WillReturnRows(row())
	_, err := session.TableRows(ctx, "appdb", "dbo.t", dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 50})
	require.NoError(t, err)

	mock.ExpectQuery("SELECT * FROM [dbo].[t] WHERE [a]]b] = 'O''Reilly' ORDER BY (SELECT NULL) OFFSET 50 ROWS FETCH NEXT 50 ROWS ONLY").
		WillReturnRows(row())
	_, err = session.TableRows(ctx, "appdb", "dbo.t", dbdriver.TableQuery{Filters: filter}, dbdriver.Page{Index: 1, Size: 50})
	require.NoError(t, err)

	mock.ExpectQuery("SELECT * FROM [dbo].[t] WHERE [a]]b] = 'O''Reilly' ORDER BY [id] ASC, [a]]b] DESC OFFSET 100 ROWS FETCH NEXT 50 ROWS ONLY").
		WillReturnRows(row())
	rows, err := session.TableRows(ctx, "appdb", "dbo.t", dbdriver.TableQuery{
		Filters: filter,
		Sorts: []dbdriver.SortItem{
			{Column: "id", Direction: dbdriver.SortAscending},
			{Column: "a]b", Direction: dbdriver.SortDescending},
		},
	}, dbdriver.Page{Index: 2, Size: 50})
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(1)}}, rows.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilterSemantics(t *testing.T) {
	sqldbtest.RunFilterSemantics(t, sqldbtest.Dialect{
		NewSession: newSession,
		Quote:      func(name string) string { return "[" + name + "]" },
		Database:   "appdb",
		Table:      "dbo.users",
		TableRef:   "[dbo].[users]",
	})
}

// IDENTITY 列既不必在新增行里填，也不能被 UPDATE，显式插入还要 IDENTITY_INSERT：自增与
// 「只能由数据库生成」两个标志都置上；普通列都不置。
func TestTableColumnsReportIdentity(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(columnsSQL).WithArgs("dbo", "users").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT", "IS_AUTO_INCREMENT", "IS_IDENTITY_ALWAYS"}).
			AddRow("id", "int", "NO", nil, nil, 1, 1).
			AddRow("name", "varchar", "YES", nil, nil, 0, 0).
			AddRow("note", "varchar", "YES", nil, nil, nil, nil))
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	columns, err := dialect{}.TableColumns(context.Background(), conn, "appdb", "dbo.users")
	require.NoError(t, err)

	require.Len(t, columns, 3)
	assert.True(t, columns[0].AutoIncrement)
	assert.True(t, columns[0].IdentityAlways)
	assert.False(t, columns[1].AutoIncrement || columns[1].IdentityAlways)
	assert.False(t, columns[2].AutoIncrement || columns[2].IdentityAlways)
	require.NoError(t, mock.ExpectationsWereMet())
}
