package mssql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestSchemaStatementsForSQLServer(t *testing.T) {
	session, _ := newMock(t)
	ctx := context.Background()

	statements, err := session.CreateTableStatements(ctx, "appdb", dbdriver.CreateTableRequest{
		Name: "dbo.users",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "int", Nullable: false},
			{Name: "name", Type: "nvarchar(50)", Nullable: true, DefaultValue: "anon"},
			{Name: "a]b", Type: "int", Nullable: true},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE [dbo].[users] (\n  [id] int NOT NULL,\n  [name] nvarchar(50) DEFAULT 'anon',\n  [a]]b] int\n)"}, statements)

	statements, err = session.DropTableStatements(ctx, "appdb", "dbo.users")
	require.NoError(t, err)
	assert.Equal(t, []string{"DROP TABLE [dbo].[users]"}, statements)

	statements, err = session.TruncateTableStatements(ctx, "appdb", "dbo.users")
	require.NoError(t, err)
	assert.Equal(t, []string{"TRUNCATE TABLE [dbo].[users]"}, statements)
}

// SQL Server 没有 CHARACTER SET：只声明排序规则（默认空），字符集选项被拒绝而不是悄悄忽略。
func TestCreateDatabaseStatementsForSQLServer(t *testing.T) {
	session, _ := newMock(t)
	ctx := context.Background()

	assert.Equal(t, []dbdriver.DatabaseOption{{Key: dbdriver.OptionCollation, Default: ""}}, descriptor.CreateDatabaseOptions)

	statements, err := session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{
		Name: "reports", Options: map[string]string{"collation": "Chinese_PRC_CI_AS"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE DATABASE [reports] COLLATE Chinese_PRC_CI_AS"}, statements)

	statements, err = session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "re]ports", Options: map[string]string{"collation": ""}})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE DATABASE [re]]ports]"}, statements)

	_, err = session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "reports", Options: map[string]string{"charset": "utf8"}})
	assert.Error(t, err)
}
