package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func identityColumns(mock sqlmock.Sqlmock, schema, table string) {
	mock.ExpectQuery(columnsSQL).WithArgs(schema, table).
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "IS_AUTO_INCREMENT", "IS_IDENTITY_ALWAYS"}).
			AddRow("id", "int", "NO", nil, 1, 1).
			AddRow("name", "nvarchar", "YES", nil, 0, 0).
			AddRow("note", "varchar", "YES", nil, 0, 0))
}

// 新增行：用户给自增列填了值时，语句临时打开这张表的 IDENTITY_INSERT、写完关闭，出错也关闭
// 并原样抛出；没给自增列值的行不碰 IDENTITY_INSERT；一列都没填的行整行取默认值。
func TestGridEditInsertsWriteIdentityValuesForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	identityColumns(mock, "dbo", "users")

	statements, err := session.GridEditStatements(context.Background(), "appdb", "dbo.users", dbdriver.GridEditRequest{
		Columns:     []string{"id", "name", "note"},
		PrimaryKeys: []string{"id"},
		Inserts: []dbdriver.GridInsert{
			{Values: []dbdriver.ColumnValue{{Column: "id", Value: "7"}, {Column: "name", Value: "中文"}}},
			{Values: []dbdriver.ColumnValue{{Column: "name", Value: "Alice"}}},
			{},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"SET IDENTITY_INSERT [dbo].[users] ON; " +
			"BEGIN TRY INSERT INTO [dbo].[users] ([id], [name]) VALUES ('7', N'中文'); END TRY " +
			"BEGIN CATCH SET IDENTITY_INSERT [dbo].[users] OFF; THROW; END CATCH; " +
			"SET IDENTITY_INSERT [dbo].[users] OFF;",
		"INSERT INTO [dbo].[users] ([name]) VALUES (N'Alice');",
		"INSERT INTO [dbo].[users] DEFAULT VALUES;",
	}, statements.Inserts)
	assert.Empty(t, statements.Updates)
	assert.Empty(t, statements.Deletes)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 往视图新增行、给视图里的 IDENTITY 列填了值：不开关 IDENTITY_INSERT（对视图开关报 8105、盖住
// 真实原因），按普通插入生成，报错交给数据库。
func TestGridEditInsertIntoAViewIsAPlainInsertForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(columnsSQL).WithArgs("dbo", "v_users").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "IS_AUTO_INCREMENT", "IS_IDENTITY_ALWAYS", "IS_NO_IDENTITY_OVERRIDE"}).
			AddRow("id", "int", "NO", nil, 1, 1, 1).
			AddRow("name", "nvarchar", "YES", nil, 0, 0, 0))

	statements, err := session.GridEditStatements(context.Background(), "appdb", "dbo.v_users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name"},
		Inserts: []dbdriver.GridInsert{{Values: []dbdriver.ColumnValue{{Column: "id", Value: "7"}, {Column: "name", Value: "中文"}}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"INSERT INTO [dbo].[v_users] ([id], [name]) VALUES ('7', N'中文');"}, statements.Inserts)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改动与删除只动一行：T-SQL 没有 LIMIT，有没有主键都用 TOP (1)；按主键定位，没有主键时按原行
// 的全部列定位（NULL 写成 IS NULL）。与改动前前端生成的文本逐字一致（原 tableSql.test.ts 的断言）。
func TestGridEditUpdatesAndDeletesOneRowForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	plainColumns := func(table string) {
		expectColumns(mock, "dbo", table, [2]string{"id", "int"}, [2]string{"name", "varchar"})
	}
	row := map[string]any{"id": float64(7), "name": "alice"}
	set := []dbdriver.ColumnValue{{Column: "name", Value: "x"}}

	plainColumns("users")
	statements, err := session.GridEditStatements(ctx, "appdb", "dbo.users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name"}, PrimaryKeys: []string{"id"},
		Updates: []dbdriver.GridUpdate{{Row: row, Set: set}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE TOP (1) [dbo].[users] SET [name] = 'x' WHERE [id] = '7';"}, statements.Updates)

	plainColumns("users")
	statements, err = session.GridEditStatements(ctx, "appdb", "dbo.users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name"},
		Updates: []dbdriver.GridUpdate{{Row: map[string]any{"id": float64(7), "name": nil}, Set: set}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE TOP (1) [dbo].[users] SET [name] = 'x' WHERE [id] = '7' AND [name] IS NULL;"}, statements.Updates)

	plainColumns("users")
	statements, err = session.GridEditStatements(ctx, "appdb", "users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name"}, PrimaryKeys: []string{"id"}, Deletes: []map[string]any{row},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"DELETE TOP (1) FROM [users] WHERE [id] = '7';"}, statements.Deletes)

	plainColumns("users")
	statements, err = session.GridEditStatements(ctx, "appdb", "users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name"}, Deletes: []map[string]any{row},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"DELETE TOP (1) FROM [users] WHERE [id] = '7' AND [name] = 'alice';"}, statements.Deletes)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改 nvarchar 列、按 nvarchar 列定位时文本都带 N 前缀。
func TestGridEditUpdateWritesUnicodeTextForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	identityColumns(mock, "dbo", "users")

	statements, err := session.GridEditStatements(context.Background(), "appdb", "dbo.users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name", "note"},
		Updates: []dbdriver.GridUpdate{{
			Row: map[string]any{"id": float64(1), "name": "中文", "note": "n"},
			Set: []dbdriver.ColumnValue{{Column: "name", Value: "Ünï"}, {Column: "note", Value: "m"}},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"UPDATE TOP (1) [dbo].[users] SET [name] = N'Ünï', [note] = 'm' WHERE [id] = '1' AND [name] = N'中文' AND [note] = 'n';",
	}, statements.Updates)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 请求里的列不在表中（表已不存在时一列都没有）：生成失败，不给出任何语句。
func TestGridEditRefusesColumnsNotInTheTableForSQLServer(t *testing.T) {
	session, mock := newMock(t)
	identityColumns(mock, "dbo", "users")

	_, err := session.GridEditStatements(context.Background(), "appdb", "dbo.users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name", "note"},
		Inserts: []dbdriver.GridInsert{{Values: []dbdriver.ColumnValue{{Column: "ghost", Value: "x"}}}},
	})
	require.ErrorContains(t, err, "ghost")
	require.NoError(t, mock.ExpectationsWereMet())
}
