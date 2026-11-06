package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb/sqldbtest"
)

func newMockSession(db *sql.DB) dbdriver.Session { return sqldb.NewSession(db, dialect{}) }

func newMock(t *testing.T) (dbdriver.Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return newMockSession(db), mock
}

func TestSQLitePassesTableConformance(t *testing.T) {
	d, err := dbregistry.Lookup(asset_entity.DriverSQLite)
	require.NoError(t, err)

	dbdrivertest.RunTableConformance(t, d, func(t *testing.T) dbdriver.Session {
		session, err := d.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "table.db"),
		}})
		require.NoError(t, err)
		for _, statement := range []string{
			"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)",
			"INSERT INTO t (name) VALUES ('a'), ('b'), ('c')",
		} {
			_, err = session.Execute(context.Background(), statement)
			require.NoError(t, err)
		}
		return session
	}, "main", "t")
}

// 打开表用 pragma_table_info 查主键与列；库名为空时不加 schema 限定。
func TestOpenTableStatements(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery("SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk").WithArgs("users").
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("id"))
	mock.ExpectQuery(`SELECT name, type, CASE "notnull" WHEN 0 THEN 'YES' ELSE 'NO' END AS is_nullable, dflt_value FROM pragma_table_info(?)`).
		WithArgs("users").
		WillReturnRows(sqlmock.NewRows([]string{"name", "type", "is_nullable", "dflt_value"}).
			AddRow("id", "INTEGER", "NO", nil).
			AddRow("name", "TEXT", "YES", nil))
	mock.ExpectQuery(`SELECT COUNT(*) FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
	mock.ExpectQuery(`SELECT * FROM "users" LIMIT 10 OFFSET 0`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "alice").AddRow(2, "bob"))

	opened, err := session.OpenTable(context.Background(), "", "users", 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	assert.Equal(t, []string{"id", "name"}, opened.FirstPage.Columns)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 在真实的 SQLite 连接上：列信息、约束、主键、总行数与第一页。
func TestOpenTableOnARealDatabase(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL DEFAULT 'anon', memo TEXT)`,
		`INSERT INTO users (name) VALUES ('alice'), ('bob'), ('carol')`,
	)

	opened, err := session.OpenTable(context.Background(), "main", "users", 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	assert.Equal(t, map[string]string{"id": "INTEGER", "name": "TEXT", "memo": "TEXT"}, opened.ColumnTypes)
	assert.Equal(t, []dbdriver.TableColumnRule{
		{Name: "id", Nullable: true},
		{Name: "name", HasDefault: true},
		{Name: "memo", Nullable: true},
	}, opened.ColumnRules)
	assert.Equal(t, int64(3), opened.TotalCount)
	assert.Equal(t, []string{"id", "name", "memo"}, opened.FirstPage.Columns)
	assert.Equal(t, [][]any{{int64(1), "alice", nil}, {int64(2), "bob", nil}}, opened.FirstPage.Rows)
}

// 翻页的语句：库名有值时表以 "schema"."table" 引用，为空时只引用表名（SQLite 没有 schema
// 分组，带点的名字整体是表名）；标识符加双引号，分页用 LIMIT / OFFSET。
func TestTableRowsStatements(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	row := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id"}).AddRow(1) }

	mock.ExpectQuery(`SELECT * FROM "main"."t" WHERE "a""b" = '1' ORDER BY "id" ASC LIMIT 50 OFFSET 0`).WillReturnRows(row())
	_, err := session.TableRows(ctx, "main", "t", dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{
			{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: `a"b`, Operator: dbdriver.OpEqual, Value: "1"},
		},
		Sorts: []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortAscending}},
	}, dbdriver.Page{Index: 0, Size: 50})
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT * FROM "main"."a.b" LIMIT 50 OFFSET 100`).WillReturnRows(row())
	_, err = session.TableRows(ctx, "main", "a.b", dbdriver.TableQuery{}, dbdriver.Page{Index: 2, Size: 50})
	require.NoError(t, err)

	mock.ExpectQuery(`SELECT * FROM "aux.t" LIMIT 50 OFFSET 0`).WillReturnRows(row())
	_, err = session.TableRows(ctx, "", "aux.t", dbdriver.TableQuery{}, dbdriver.Page{Index: 0, Size: 50})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 在真实的 SQLite 连接上：筛选、排序、翻页给出的行与顺序，以及符合筛选条件的总行数。
func TestTableRowsAndCountOnARealDatabase(t *testing.T) {
	session := newDatabase(t,
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT, age INTEGER, memo TEXT)`,
		`INSERT INTO people (id, name, age, memo) VALUES
			(1, 'alice', 30, NULL), (2, 'bob', 17, ''), (3, 'carol', 45, 'vip'), (4, 'dave', 30, 'O''Reilly'), (5, 'alan', 22, NULL)`,
	)
	ctx := context.Background()
	condition := func(column string, op dbdriver.FilterOperator, value any) dbdriver.FilterItem {
		return dbdriver.FilterItem{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: column, Operator: op, Value: value}
	}
	ids := func(query dbdriver.TableQuery, page dbdriver.Page) []any {
		t.Helper()
		rows, err := session.TableRows(ctx, "main", "people", query, page)
		require.NoError(t, err)
		require.Equal(t, []string{"id", "name", "age", "memo"}, rows.Columns)
		out := make([]any, 0, len(rows.Rows))
		for _, row := range rows.Rows {
			out = append(out, row[0])
		}
		return out
	}
	count := func(filters []dbdriver.FilterItem) int64 {
		t.Helper()
		total, err := session.CountTableRows(ctx, "main", "people", filters)
		require.NoError(t, err)
		return total
	}
	all := dbdriver.Page{Index: 0, Size: 100}
	byAgeThenNameDesc := []dbdriver.SortItem{
		{Column: "age", Direction: dbdriver.SortAscending},
		{Column: "name", Direction: dbdriver.SortDescending},
	}

	// 不带条件：全部行；排序项按先后生效；翻页取对应的那一段，总数不随翻页变。
	assert.Equal(t, []any{int64(2), int64(5), int64(4), int64(1), int64(3)}, ids(dbdriver.TableQuery{Sorts: byAgeThenNameDesc}, all))
	assert.Equal(t, []any{int64(4), int64(1)}, ids(dbdriver.TableQuery{Sorts: byAgeThenNameDesc}, dbdriver.Page{Index: 1, Size: 2}))
	assert.Empty(t, ids(dbdriver.TableQuery{Sorts: byAgeThenNameDesc}, dbdriver.Page{Index: 9, Size: 2}))
	assert.Equal(t, int64(5), count(nil))

	// 数字的值写成字符串字面量，由数据库按列的类型比较。
	adults := []dbdriver.FilterItem{condition("age", dbdriver.OpGreaterOrEqual, float64(22))}
	byID := []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortAscending}}
	assert.Equal(t, []any{int64(1), int64(3), int64(4), int64(5)}, ids(dbdriver.TableQuery{Filters: adults, Sorts: byID}, all))
	assert.Equal(t, int64(4), count(adults))

	// 带 OR 的条件加一个取反的组：(name 以 al 开头 OR memo 为空) AND NOT (age 在 30 到 50 之间)。
	mixed := []dbdriver.FilterItem{
		{Kind: dbdriver.FilterGroup, Join: dbdriver.JoinAnd, Items: []dbdriver.FilterItem{
			{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinOr, Column: "name", Operator: dbdriver.OpBeginsWith, Value: "al"},
			condition("memo", dbdriver.OpIsEmpty, nil),
		}},
		{Kind: dbdriver.FilterGroup, Join: dbdriver.JoinAnd, Negated: true, Items: []dbdriver.FilterItem{
			condition("age", dbdriver.OpBetween, []any{float64(30), float64(50)}),
		}},
	}
	assert.Equal(t, []any{int64(5), int64(2)}, ids(dbdriver.TableQuery{
		Filters: mixed, Sorts: []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}},
	}, all))
	assert.Equal(t, int64(2), count(mixed))

	// 值里的单引号不会截断字面量；列表、NULL 的判断。
	assert.Equal(t, int64(1), count([]dbdriver.FilterItem{condition("memo", dbdriver.OpEqual, "O'Reilly")}))
	assert.Equal(t, int64(2), count([]dbdriver.FilterItem{condition("name", dbdriver.OpInList, "bob, carol")}))
	assert.Equal(t, int64(2), count([]dbdriver.FilterItem{condition("memo", dbdriver.OpEqual, nil)}))
	assert.Equal(t, int64(3), count([]dbdriver.FilterItem{condition("memo", dbdriver.OpNotEqual, nil)}))
}

func TestFilterSemantics(t *testing.T) {
	sqldbtest.RunFilterSemantics(t, sqldbtest.Dialect{
		NewSession: newMockSession,
		Quote:      func(name string) string { return `"` + name + `"` },
		Database:   "main",
		Table:      "users",
		TableRef:   `"main"."users"`,
	})
}
