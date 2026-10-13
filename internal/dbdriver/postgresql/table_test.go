package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/sqldb/sqldbtest"
)

const (
	primaryKeysSQL = "SELECT kcu.column_name FROM information_schema.table_constraints tc " +
		"JOIN information_schema.key_column_usage kcu " +
		"ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema " +
		"WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'PRIMARY KEY' " +
		"ORDER BY kcu.ordinal_position"
	columnsSQL = "SELECT column_name, data_type, udt_name, is_nullable, column_default " +
		"FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position"
)

// 打开表的 4 条语句顺序固定：主键、列、总行数、第一页。裸表名按 public 查
// information_schema，取数时不加 schema（由 search_path 解析）；库名不进语句。
func TestOpenTable(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery(primaryKeysSQL).WithArgs("public", "users").
		WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))
	mock.ExpectQuery(columnsSQL).WithArgs("public", "users").
		WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default"}).
			AddRow("id", "integer", "int4", "NO", "nextval('seq')").
			AddRow("name", "text", "text", "YES", nil))
	mock.ExpectQuery(`SELECT COUNT(*) FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(5))
	mock.ExpectQuery(`SELECT * FROM "users" LIMIT 20 OFFSET 0`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice"))

	opened, err := session.OpenTable(context.Background(), "mydb", "users", 20)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	assert.Equal(t, int64(5), opened.TotalCount)
	assert.Equal(t, map[string]string{"id": "integer", "name": "text"}, opened.ColumnTypes)
	assert.Equal(t, []dbdriver.TableColumnRule{
		{Name: "id", HasDefault: true},
		{Name: "name", Nullable: true},
	}, opened.ColumnRules)
	assert.Equal(t, []string{"id", "name"}, opened.FirstPage.Columns)
	assert.Len(t, opened.FirstPage.Rows, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 收到 schema.table 时按 schema 拆开查 information_schema（table_schema='reporting' 且
// table_name='events'），而不是把 'reporting.events' 整体当表名；取数时两段各自加引号。
func TestOpenTableSchemaQualified(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery(primaryKeysSQL).WithArgs("reporting", "events").
		WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))
	mock.ExpectQuery(columnsSQL).WithArgs("reporting", "events").
		WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default"}).
			AddRow("id", "integer", "int4", "NO", nil))
	mock.ExpectQuery(`SELECT COUNT(*) FROM "reporting"."events"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(3))
	mock.ExpectQuery(`SELECT * FROM "reporting"."events" LIMIT 10 OFFSET 0`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))

	opened, err := session.OpenTable(context.Background(), "mydb", "reporting.events", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	assert.Equal(t, int64(3), opened.TotalCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 翻页：标识符加双引号（内部的双引号成对转义），分页用 LIMIT / OFFSET。
func TestTableRows(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	row := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id"}).AddRow(1) }

	mock.ExpectQuery(`SELECT * FROM "reporting"."events" LIMIT 20 OFFSET 40`).WillReturnRows(row())
	_, err := session.TableRows(ctx, "mydb", "reporting.events", dbdriver.TableQuery{}, dbdriver.Page{Index: 2, Size: 20})
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT * FROM "audit""logs" WHERE "id""part" = 'O''Reilly' ORDER BY "id""part" ASC, "name" DESC LIMIT 20 OFFSET 0`).
		WillReturnRows(row())
	rows, err := session.TableRows(ctx, "mydb", `audit"logs`, dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{
			{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: `id"part`, Operator: dbdriver.OpEqual, Value: "O'Reilly"},
		},
		Sorts: []dbdriver.SortItem{
			{Column: `id"part`, Direction: dbdriver.SortAscending},
			{Column: "name", Direction: dbdriver.SortDescending},
		},
	}, dbdriver.Page{Index: 0, Size: 20})
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(1)}}, rows.Rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilterSemantics(t *testing.T) {
	sqldbtest.RunFilterSemantics(t, sqldbtest.Dialect{
		NewSession: newSession,
		Quote:      func(name string) string { return `"` + name + `"` },
		Database:   "mydb",
		Table:      "reporting.events",
		TableRef:   `"reporting"."events"`,
	})
}
