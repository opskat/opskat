package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 打开改表对话框：列照 pragma_table_info 读出（与改动前对话框显示的一致），SQLite 没有注释。
func TestAlterTableModelForSQLite(t *testing.T) {
	session := newDatabase(t, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT 'anon', n REAL)`)

	model, err := session.AlterTableModel(context.Background(), "main", "users")
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableModel{
		Name: "users",
		Columns: []dbdriver.ColumnDefinition{
			{Name: "id", Type: "INTEGER", Nullable: true},
			{Name: "name", Type: "TEXT", Nullable: false, DefaultValue: "'anon'"},
			{Name: "n", Type: "REAL", Nullable: true},
		},
	}, model)

	_, err = session.AlterTableModel(context.Background(), "main", "missing")
	assert.Error(t, err)
}

// 只生成 SQLite 支持的改表语句：改表名、新增列、给列改名（与改动前前端生成的文本逐字一致，原
// AlterTableDialog.test.ts 的断言）；在真实的库上执行后表结构如预期。
func TestAlterTableStatementsRunOnARealSQLiteDatabase(t *testing.T) {
	session := newDatabase(t, `CREATE TABLE users (id INTEGER NOT NULL, name TEXT, gone TEXT)`)
	ctx := context.Background()

	result, err := session.AlterTableStatements(ctx, "main", "users", dbdriver.AlterTableRequest{
		Name: "users_v2",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "id", Name: "id", Type: "INTEGER", Nullable: false},
			{OriginalName: "name", Name: "full_name", Type: "TEXT", Nullable: true},
			{Name: "age", Type: "INTEGER", Nullable: true, DefaultValue: "18"},
			{Name: "tag", Type: "BLOB", Nullable: true, DefaultValue: "ab"},
		},
		Drops: []string{"gone"},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{
		Statements: []string{
			`ALTER TABLE "main"."users" RENAME TO "users_v2"`,
			`ALTER TABLE "main"."users_v2" ADD COLUMN "age" INTEGER DEFAULT 18`,
			`ALTER TABLE "main"."users_v2" ADD COLUMN "tag" BLOB DEFAULT 'ab'`,
			`ALTER TABLE "main"."users_v2" RENAME COLUMN "name" TO "full_name"`,
			`ALTER TABLE "main"."users_v2" DROP COLUMN "gone"`,
		},
		Table: "users_v2",
	}, result)

	for _, statement := range result.Statements {
		_, err := session.Execute(ctx, statement)
		require.NoError(t, err, statement)
	}
	res, err := session.Execute(ctx, `SELECT name, type, dflt_value FROM pragma_table_info('users_v2')`)
	require.NoError(t, err)
	assert.Equal(t, [][]any{{"id", "INTEGER", nil}, {"full_name", "TEXT", nil}, {"age", "INTEGER", "18"}, {"tag", "BLOB", "'ab'"}}, res.Rows.Rows)
}

// SQLite 的已有列只能改名或删除，没有注释：要改类型、可空、默认值、注释的请求生成失败，什么也不执行。
func TestAlterTableRefusesWhatSQLiteCannotAlter(t *testing.T) {
	ctx := context.Background()
	for name, req := range map[string]dbdriver.AlterTableRequest{
		"column type":        {Columns: []dbdriver.AlterColumn{{OriginalName: "id", Name: "id", Type: "BIGINT", Nullable: true}}},
		"column nullable":    {Columns: []dbdriver.AlterColumn{{OriginalName: "id", Name: "id", Type: "INTEGER", Nullable: false}}},
		"column default":     {Columns: []dbdriver.AlterColumn{{OriginalName: "id", Name: "id", Type: "INTEGER", Nullable: true, DefaultValue: "1"}}},
		"column comment":     {Columns: []dbdriver.AlterColumn{{OriginalName: "id", Name: "id", Type: "INTEGER", Nullable: true, Comment: "x"}}},
		"new column comment": {Columns: []dbdriver.AlterColumn{{OriginalName: "id", Name: "id", Type: "INTEGER", Nullable: true}, {Name: "n", Type: "TEXT", Comment: "x"}}},
		"table comment":      {Comment: "x", Columns: []dbdriver.AlterColumn{{OriginalName: "id", Name: "id", Type: "INTEGER", Nullable: true}}},
	} {
		t.Run(name, func(t *testing.T) {
			session := newDatabase(t, `CREATE TABLE users (id INTEGER)`)
			_, err := session.AlterTableStatements(ctx, "main", "users", req)
			assert.Error(t, err)
		})
	}
}
