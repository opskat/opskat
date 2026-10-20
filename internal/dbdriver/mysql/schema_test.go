package mysql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 建表 / 删表 / 清空 / 建库：与改动前前端生成的文本逐字一致（原 CreateTableDialog.test.ts、
// DatabaseTree.test.tsx 的断言）。
func TestSchemaStatementsForMySQL(t *testing.T) {
	session, _ := newMock(t)
	ctx := context.Background()

	statements, err := session.CreateTableStatements(ctx, "appdb", dbdriver.CreateTableRequest{
		Name: "users",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "BIGINT", Nullable: false},
			{Name: "age", Type: "INT", Nullable: true, DefaultValue: "18"},
			{Name: "name", Type: "VARCHAR(100)", Nullable: false, DefaultValue: "anon"},
			{Name: "active", Type: "BOOLEAN", Nullable: true, DefaultValue: "true"},
			{Name: "created_at", Type: "DATETIME", Nullable: false, DefaultValue: "CURRENT_TIMESTAMP"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE `appdb`.`users` (\n" +
		"  `id` BIGINT NOT NULL,\n" +
		"  `age` INT DEFAULT 18,\n" +
		"  `name` VARCHAR(100) NOT NULL DEFAULT 'anon',\n" +
		"  `active` BOOLEAN DEFAULT TRUE,\n" +
		"  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP\n)"}, statements)

	statements, err = session.CreateTableStatements(ctx, "app", dbdriver.CreateTableRequest{
		Name: "we`ird", Columns: []dbdriver.NewColumn{{Name: "col`1", Type: "INT", Nullable: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE `app`.`we``ird` (\n  `col``1` INT\n)"}, statements)

	statements, err = session.DropTableStatements(ctx, "appdb", "users")
	require.NoError(t, err)
	assert.Equal(t, []string{"DROP TABLE `appdb`.`users`"}, statements)

	statements, err = session.TruncateTableStatements(ctx, "appdb", "users")
	require.NoError(t, err)
	assert.Equal(t, []string{"TRUNCATE TABLE `appdb`.`users`"}, statements)
}

func TestCreateDatabaseStatementsForMySQL(t *testing.T) {
	session, _ := newMock(t)
	ctx := context.Background()

	statements, err := session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{
		Name: "reports", Options: map[string]string{"charset": "utf8mb4", "collation": "utf8mb4_0900_ai_ci"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE DATABASE `reports` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"}, statements)

	statements, err = session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "we`ird", Options: map[string]string{"charset": " ", "collation": ""}})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE DATABASE `we``ird`"}, statements)

	_, err = session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "x", Options: map[string]string{"charset": "utf8; DROP DATABASE y"}})
	assert.Error(t, err, "option values are interpolated into the statement, so unsafe ones are refused")
	_, err = session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "x", Options: map[string]string{"engine": "x"}})
	assert.Error(t, err, "options the driver did not declare are refused")
}

func TestMySQLDeclaresCreateDatabaseOptions(t *testing.T) {
	assert.Equal(t, []dbdriver.DatabaseOption{
		{Key: dbdriver.OptionCharset, Default: "utf8mb4"},
		{Key: dbdriver.OptionCollation, Default: "utf8mb4_0900_ai_ci"},
	}, descriptor.CreateDatabaseOptions)
}
