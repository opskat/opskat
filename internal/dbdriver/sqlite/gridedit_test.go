package sqlite

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 表格编辑：有主键时按主键定位；没有主键时用 rowid 把全列匹配到的几行收敛成物理上的一行；一列
// 都没填的新增行写 DEFAULT VALUES。与改动前前端生成的文本逐字一致（原 tableEdit.test.ts /
// tableSql.test.ts 的断言）。
func TestGridEditStatementsForSQLite(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	columns := func() {
		mock.ExpectQuery(columnsSQL + `"main".pragma_table_info(?)`).WithArgs("users").WillReturnRows(
			sqlmock.NewRows([]string{"name", "type", "is_nullable", "dflt_value"}).
				AddRow("id", "INTEGER", "NO", nil).AddRow("name", "TEXT", "YES", nil))
	}
	row := map[string]any{"id": float64(7), "name": "alice"}
	set := []dbdriver.ColumnValue{{Column: "name", Value: "x"}}

	columns()
	statements, err := session.GridEditStatements(ctx, "main", "users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name"}, PrimaryKeys: []string{"id"},
		Inserts: []dbdriver.GridInsert{{}},
		Updates: []dbdriver.GridUpdate{{Row: row, Set: set}},
		Deletes: []map[string]any{row},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.GridEditStatements{
		Inserts: []string{`INSERT INTO "main"."users" DEFAULT VALUES;`},
		Updates: []string{`UPDATE "main"."users" SET "name" = 'x' WHERE "id" = '7';`},
		Deletes: []string{`DELETE FROM "main"."users" WHERE "id" = '7';`},
	}, statements)

	columns()
	statements, err = session.GridEditStatements(ctx, "main", "users", dbdriver.GridEditRequest{
		Columns: []string{"id", "name"},
		Updates: []dbdriver.GridUpdate{{Row: row, Set: set}},
		Deletes: []map[string]any{row},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		`UPDATE "main"."users" SET "name" = 'x' WHERE rowid = (SELECT rowid FROM "main"."users" WHERE "id" = '7' AND "name" = 'alice' LIMIT 1);`,
	}, statements.Updates)
	assert.Equal(t, []string{
		`DELETE FROM "main"."users" WHERE rowid = (SELECT rowid FROM "main"."users" WHERE "id" = '7' AND "name" = 'alice' LIMIT 1);`,
	}, statements.Deletes)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 在真实的 SQLite 上：没有主键、两行一模一样的表，改动与删除都只动其中一行；整行取默认值的新增
// 行写得进去。
func TestGridEditTouchesOneOfIdenticalRowsOnARealDatabase(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE twins (name TEXT, qty INTEGER DEFAULT 5)`,
		`INSERT INTO twins VALUES ('a', 1), ('a', 1)`)
	ctx := context.Background()
	row := map[string]any{"name": "a", "qty": float64(1)}

	statements, err := session.GridEditStatements(ctx, "main", "twins", dbdriver.GridEditRequest{
		Columns: []string{"name", "qty"},
		Inserts: []dbdriver.GridInsert{{}},
		Updates: []dbdriver.GridUpdate{{Row: row, Set: []dbdriver.ColumnValue{{Column: "qty", Value: "2"}}}},
	})
	require.NoError(t, err)
	for _, statement := range append(statements.Inserts, statements.Updates...) {
		res, err := session.Execute(ctx, statement)
		require.NoError(t, err, statement)
		assert.Equal(t, int64(1), res.AffectedRows, statement)
	}

	statements, err = session.GridEditStatements(ctx, "main", "twins", dbdriver.GridEditRequest{
		Columns: []string{"name", "qty"}, Deletes: []map[string]any{{"name": "a", "qty": float64(2)}},
	})
	require.NoError(t, err)
	res, err := session.Execute(ctx, statements.Deletes[0])
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.AffectedRows)

	res, err = session.Execute(ctx, `SELECT name, qty FROM twins ORDER BY rowid`)
	require.NoError(t, err)
	assert.Equal(t, [][]any{{"a", int64(1)}, {nil, int64(5)}}, res.Rows.Rows)
}
