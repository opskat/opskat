package postgresql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestSchemaStatementsForPostgreSQL(t *testing.T) {
	session, _ := newMock(t)
	ctx := context.Background()

	statements, err := session.CreateTableStatements(ctx, "appdb", dbdriver.CreateTableRequest{
		Name: "users",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "serial", Nullable: false},
			{Name: "email", Type: "text", Nullable: true},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE \"users\" (\n  \"id\" serial NOT NULL,\n  \"email\" text\n)"}, statements)

	statements, err = session.CreateTableStatements(ctx, "app", dbdriver.CreateTableRequest{
		Name: `we"ird`, Columns: []dbdriver.NewColumn{{Name: `col"1`, Type: "text", Nullable: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE \"we\"\"ird\" (\n  \"col\"\"1\" text\n)"}, statements)

	statements, err = session.DropTableStatements(ctx, "appdb", "public.users")
	require.NoError(t, err)
	assert.Equal(t, []string{`DROP TABLE "public"."users"`}, statements)

	statements, err = session.TruncateTableStatements(ctx, "appdb", "public.users")
	require.NoError(t, err)
	assert.Equal(t, []string{`TRUNCATE TABLE "public"."users"`}, statements)
}

func TestCreateDatabaseStatementsForPostgreSQL(t *testing.T) {
	session, _ := newMock(t)
	ctx := context.Background()

	statements, err := session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{
		Name: "reports", Options: map[string]string{"charset": "UTF8", "collation": "en_US.UTF-8"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`CREATE DATABASE "reports" WITH ENCODING = 'UTF8' LC_COLLATE = 'en_US.UTF-8' LC_CTYPE = 'en_US.UTF-8'`}, statements)

	statements, err = session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "reports", Options: map[string]string{"charset": "LATIN1"}})
	require.NoError(t, err)
	assert.Equal(t, []string{`CREATE DATABASE "reports" WITH ENCODING = 'LATIN1'`}, statements)

	statements, err = session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "reports"})
	require.NoError(t, err)
	assert.Equal(t, []string{`CREATE DATABASE "reports"`}, statements)
}

// 对话框不改选项时提交的就是描述里的默认值：编码为 UTF8，不写 LC_COLLATE / LC_CTYPE，沿用模板库的
// 排序规则（模板库是 en_US.utf8 时写 en_US.UTF-8 会报 22023）。
func TestCreateDatabaseWithDefaultOptionsKeepsTemplateCollationForPostgreSQL(t *testing.T) {
	session, _ := newMock(t)
	options := make(map[string]string, len(descriptor.CreateDatabaseOptions))
	for _, option := range descriptor.CreateDatabaseOptions {
		options[option.Key] = option.Default
	}

	statements, err := session.CreateDatabaseStatements(context.Background(), dbdriver.CreateDatabaseRequest{Name: "reports", Options: options})
	require.NoError(t, err)
	assert.Equal(t, []string{`CREATE DATABASE "reports" WITH ENCODING = 'UTF8'`}, statements)
}
