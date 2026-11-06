package sqlite

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
)

const columnsSQL = `SELECT name, type, CASE "notnull" WHEN 0 THEN 'YES' ELSE 'NO' END AS is_nullable, dflt_value FROM `

// 复制 / 导出为 SQL：表以 "schema"."table" 引用，BLOB 列写成 X'…'，其余照旧；UPDATE 不带 LIMIT。
func TestRowStatementsForSQLite(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	columns := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"name", "type", "is_nullable", "dflt_value"}).
			AddRow("id", "INTEGER", "NO", nil).AddRow("name", "TEXT", "YES", nil).AddRow("data", "BLOB", "YES", nil)
	}

	mock.ExpectQuery(columnsSQL + `"main".pragma_table_info(?)`).WithArgs("users").WillReturnRows(columns())
	statements, err := dbdrivertest.RowStatements(ctx, session, "main", "users", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"id", "name", "data"},
		Rows:    []map[string]any{{"id": float64(1), "name": "O'Reilly", "data": "AB"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`INSERT INTO "main"."users" ("id", "name", "data") VALUES ('1', 'O''Reilly', X'4142');`}, statements)

	mock.ExpectQuery(columnsSQL + `"main".pragma_table_info(?)`).WithArgs("users").WillReturnRows(columns())
	statements, err = dbdrivertest.RowStatements(ctx, session, "main", "users", dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"id", "name"},
		Rows:        []map[string]any{{"id": float64(2), "name": "O'Reilly"}},
		PrimaryKeys: []string{"id"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`UPDATE "main"."users" SET "id" = '2', "name" = 'O''Reilly' WHERE "id" = '2';`}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 在真实的 SQLite 上：生成的 INSERT 执行后读回的值与给出的一致（二进制按字节，文本里的引号与
// 反斜杠原样）；名字里带点的表作为一个整体引用。
func TestRowStatementsRoundTripOnARealDatabase(t *testing.T) {
	session := newDatabase(t, `CREATE TABLE "a.b" (id INTEGER PRIMARY KEY, name TEXT, data BLOB)`)
	ctx := context.Background()

	statements, err := dbdrivertest.RowStatements(ctx, session, "main", "a.b", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"id", "name", "data"},
		Rows:    []map[string]any{{"id": float64(7), "name": `O'Reilly C:\new`, "data": "\x00\xffA"}},
	})
	require.NoError(t, err)
	require.Len(t, statements, 1)
	_, err = session.Execute(ctx, statements[0])
	require.NoError(t, err)

	res, err := session.Execute(ctx, `SELECT id, name, hex(data) FROM "a.b"`)
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(7), `O'Reilly C:\new`, "00FF41"}}, res.Rows.Rows)
}
