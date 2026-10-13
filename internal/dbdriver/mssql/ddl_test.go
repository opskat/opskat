package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ddlColumnsSQL = "SELECT COLUMN_NAME, DATA_TYPE, CHARACTER_MAXIMUM_LENGTH, IS_NULLABLE, COLUMN_DEFAULT " +
	"FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2 ORDER BY ORDINAL_POSITION"

func ddlColumnRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "CHARACTER_MAXIMUM_LENGTH", "IS_NULLABLE", "COLUMN_DEFAULT"})
}

func ddlColumns() *sqlmock.Rows {
	return ddlColumnRows().
		AddRow("id", "int", nil, "NO", "((0))").
		AddRow("name", "nvarchar", int64(50), "YES", nil).
		AddRow("bio", "nvarchar", int64(-1), "YES", nil).
		AddRow("code", "char", int64(0), "NO", nil)
}

// 从 INFORMATION_SCHEMA 重建：长度为正写成 type(n)，-1 写成 type(max)，NOT NULL、DEFAULT、主键
// 依次排列；表名按 [schema].[table] 加引号。
func TestTableDDL(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsSQL).WithArgs("sales", "orders").WillReturnRows(ddlColumns())
	mock.ExpectQuery(primaryKeysSQL).WithArgs("sales", "orders").
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}).AddRow("id"))

	ddl, err := session.TableDDL(context.Background(), "appdb", "sales.orders")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TABLE [sales].[orders] (\n"+
		"  [id] int NOT NULL DEFAULT ((0)),\n"+
		"  [name] nvarchar(50),\n"+
		"  [bio] nvarchar(max),\n"+
		"  [code] char NOT NULL,\n"+
		"  PRIMARY KEY ([id])\n);", ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 裸表名按 dbo 查询，语句里的表名保持原样（不补 schema），与改动前一致。
func TestTableDDLOfABareNameQueriesDbo(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsSQL).WithArgs("dbo", "users").WillReturnRows(ddlColumns())
	mock.ExpectQuery(primaryKeysSQL).WithArgs("dbo", "users").WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME"}))

	ddl, err := session.TableDDL(context.Background(), "appdb", "users")
	require.NoError(t, err)
	assert.Contains(t, ddl, "CREATE TABLE [users] (\n")
	assert.NotContains(t, ddl, "PRIMARY KEY")
}

func TestTableDDLOfAMissingTableIsEmpty(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsSQL).WithArgs("dbo", "gone").WillReturnRows(ddlColumnRows())

	ddl, err := session.TableDDL(context.Background(), "appdb", "gone")
	require.NoError(t, err)
	assert.Empty(t, ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStarterQueryUsesTop(t *testing.T) {
	session, _ := newMock(t)
	got, err := session.StarterQuery(context.Background(), "appdb", "dbo.users", 100)
	require.NoError(t, err)
	assert.Equal(t, "SELECT TOP 100 * FROM [dbo].[users]", got)
}
