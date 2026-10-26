package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// SQLite 没有 TRUNCATE，清空发 DELETE FROM；没有新建数据库。
func TestSchemaStatementsForSQLite(t *testing.T) {
	session, _ := newMock(t)
	ctx := context.Background()

	statements, err := session.CreateTableStatements(ctx, "main", dbdriver.CreateTableRequest{
		Name: "users",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "INTEGER", Nullable: false},
			{Name: "name", Type: "TEXT", Nullable: true, DefaultValue: "anon"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE \"main\".\"users\" (\n  \"id\" INTEGER NOT NULL,\n  \"name\" TEXT DEFAULT 'anon'\n)"}, statements)

	statements, err = session.DropTableStatements(ctx, "main", "users")
	require.NoError(t, err)
	assert.Equal(t, []string{`DROP TABLE "main"."users"`}, statements)

	statements, err = session.TruncateTableStatements(ctx, "main", "users")
	require.NoError(t, err)
	assert.Equal(t, []string{`DELETE FROM "main"."users"`}, statements)

	_, err = session.CreateDatabaseStatements(ctx, dbdriver.CreateDatabaseRequest{Name: "x"})
	assert.ErrorIs(t, err, dbdriver.ErrUnsupported)
}

// 在真实的 SQLite 上执行生成的语句：建表、清空、删表都生效。
func TestSchemaStatementsRunOnARealSQLiteDatabase(t *testing.T) {
	session := newDatabase(t)
	ctx := context.Background()
	run := func(statements []string, err error) {
		t.Helper()
		require.NoError(t, err)
		for _, statement := range statements {
			_, err := session.Execute(ctx, statement)
			require.NoError(t, err, statement)
		}
	}

	run(session.CreateTableStatements(ctx, "main", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "INTEGER", Nullable: false}, {Name: "n", Type: "INTEGER", Nullable: true, DefaultValue: "5"}},
	}))
	_, err := session.Execute(ctx, `INSERT INTO t (id) VALUES (1), (2)`)
	require.NoError(t, err)

	run(session.TruncateTableStatements(ctx, "main", "t"))
	res, err := session.Execute(ctx, `SELECT COUNT(*) FROM t`)
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(0)}}, res.Rows.Rows)

	run(session.DropTableStatements(ctx, "main", "t"))
	_, err = session.Execute(ctx, `SELECT * FROM t`)
	assert.Error(t, err)
}

func TestSQLiteDeclaresNoCreateDatabaseOptions(t *testing.T) {
	assert.Empty(t, descriptor.CreateDatabaseOptions)
}
