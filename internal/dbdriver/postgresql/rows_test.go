package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func expectColumns(mock sqlmock.Sqlmock, schema, table string, columns ...[2]string) {
	rows := sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default"})
	for _, c := range columns {
		rows.AddRow(c[0], c[1], c[1], "YES", nil)
	}
	mock.ExpectQuery(columnsSQL).WithArgs(schema, table).WillReturnRows(rows)
}

// 复制 / 导出为 SQL：表以 "schema"."table" 引用，bytea 写成 '\x…'::bytea，其余照旧；UPDATE
// 不带 LIMIT。
func TestRowStatementsForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	expectColumns(mock, "public", "users", [2]string{"id", "integer"}, [2]string{"name", "text"}, [2]string{"avatar", "bytea"})

	statements, err := dbdrivertest.RowStatements(ctx, session, "mydb", "public.users", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"id", "name", "avatar"},
		Rows:    []map[string]any{{"id": float64(1), "name": `O'Reilly \n`, "avatar": "\xde\xad"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		`INSERT INTO "public"."users" ("id", "name", "avatar") VALUES ('1', 'O''Reilly \n', '\xdead'::bytea);`,
	}, statements)

	expectColumns(mock, "public", "users", [2]string{"id", "integer"}, [2]string{"name", "text"})
	statements, err = dbdrivertest.RowStatements(ctx, session, "mydb", "public.users", dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"id", "name"},
		Rows:        []map[string]any{{"id": float64(1), "name": nil}},
		PrimaryKeys: []string{"id"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`UPDATE "public"."users" SET "id" = '1', "name" = NULL WHERE "id" = '1';`}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 名字里带点的表（public 里的 "a.b"）：浏览时只在第一个点处拆开，按 schema=public、table=a.b
// 查目录，取数引用 "public"."a.b"；生成的语句同样把它当一个名字。
func TestDottedTableNameIsOneNameForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()

	mock.ExpectQuery(primaryKeysSQL).WithArgs("public", "a.b").
		WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))
	expectColumns(mock, "public", "a.b", [2]string{"id", "integer"})
	mock.ExpectQuery(`SELECT COUNT(*) FROM "public"."a.b"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	mock.ExpectQuery(`SELECT * FROM "public"."a.b" LIMIT 10 OFFSET 0`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	opened, err := session.OpenTable(ctx, "mydb", "public.a.b", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)

	expectColumns(mock, "public", "a.b", [2]string{"id", "integer"})
	statements, err := dbdrivertest.RowStatements(ctx, session, "mydb", "public.a.b", dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id"}, Rows: []map[string]any{{"id": float64(1)}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{`INSERT INTO "public"."a.b" ("id") VALUES ('1');`}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}
