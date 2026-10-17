package postgresql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
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

func TestPostgreSQLDeclaresCreateDatabaseOptions(t *testing.T) {
	assert.Equal(t, []dbdriver.DatabaseOption{
		{Key: dbdriver.OptionCharset, Default: "UTF8"},
		{Key: dbdriver.OptionCollation, Default: "en_US.UTF-8"},
	}, descriptor.CreateDatabaseOptions)
}
