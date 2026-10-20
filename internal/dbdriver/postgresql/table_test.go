package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb/sqldbtest"
)

const (
	primaryKeysSQL = "SELECT kcu.column_name FROM information_schema.table_constraints tc " +
		"JOIN information_schema.key_column_usage kcu " +
		"ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema " +
		"WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'PRIMARY KEY' " +
		"ORDER BY kcu.ordinal_position"
	columnsSQL = "SELECT column_name, data_type, udt_name, is_nullable, column_default, " +
		"col_description(format('%I.%I', table_schema, table_name)::regclass, ordinal_position::int) AS column_comment, " +
		"(is_identity = 'YES' OR column_default LIKE 'nextval(%') AS is_auto_increment, " +
		"(identity_generation = 'ALWAYS') AS is_identity_always " +
		"FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position"
)

// 打开表的 4 条语句顺序固定：主键、列、总行数、第一页。裸表名按 public 查
// information_schema，取数时不加 schema（由 search_path 解析）；库名不进语句。
func TestOpenTable(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery(primaryKeysSQL).WithArgs("public", "users").
		WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))
	mock.ExpectQuery(columnsSQL).WithArgs("public", "users").
		WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default", "is_auto_increment", "is_identity_always"}).
			AddRow("id", "integer", "int4", "NO", "nextval('seq')", true, false).
			AddRow("name", "text", "text", "YES", nil, false, false))
	mock.ExpectQuery(`SELECT COUNT(*) FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(5))
	mock.ExpectQuery(`SELECT * FROM "users" LIMIT 20 OFFSET 0`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice"))

	opened, err := session.OpenTable(context.Background(), "mydb", "users", 20)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	assert.Equal(t, int64(5), opened.TotalCount)
	assert.Equal(t, map[string]string{"id": "integer", "name": "text"}, opened.ColumnTypes)
	assert.Equal(t, []dbdriver.TableColumnRule{
		{Name: "id", HasDefault: true, AutoIncrement: true},
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

// serial（默认值 nextval）与 GENERATED BY DEFAULT AS IDENTITY 都是自增、可 UPDATE；
// GENERATED ALWAYS AS IDENTITY 还带「只能由数据库生成」标志。
func TestTableColumnsReportIdentityAndSerial(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery(columnsSQL).WithArgs("public", "users").
		WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default", "column_comment", "is_auto_increment", "is_identity_always"}).
			AddRow("serial_id", "integer", "int4", "NO", "nextval('users_serial_id_seq'::regclass)", nil, true, false).
			AddRow("by_default", "integer", "int4", "NO", nil, nil, true, false).
			AddRow("always_id", "integer", "int4", "NO", nil, nil, true, true).
			AddRow("name", "text", "text", "YES", nil, nil, false, false))
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	columns, err := dialect{}.TableColumns(context.Background(), conn, "mydb", "users")
	require.NoError(t, err)

	got := map[string][2]bool{}
	for _, c := range columns {
		got[c.Name] = [2]bool{c.AutoIncrement, c.IdentityAlways}
	}
	assert.Equal(t, map[string][2]bool{
		"serial_id": {true, false}, "by_default": {true, false}, "always_id": {true, true}, "name": {false, false},
	}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}
