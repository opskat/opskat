package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// tablesDialect 是一个只为测试而设的完整方言：标识符用尖括号，好与任何真实方言区分。
type tablesDialect struct{ LimitOffset }

func angle(name string) string { return "<" + name + ">" }

func (tablesDialect) QuoteIdent(name string) string { return angle(name) }

func (tablesDialect) TableRef(database, table string) string {
	return angle(database) + "." + angle(table)
}

func (tablesDialect) PrimaryKeys(ctx context.Context, conn *sql.Conn, _, _ string) ([]string, error) {
	return QueryPrimaryKeys(ctx, conn, "name", "PRIMARY KEYS")
}

func (tablesDialect) TableColumns(ctx context.Context, conn *sql.Conn, _, _ string) ([]TableColumn, error) {
	return QueryTableColumns(ctx, conn, "COLUMNS")
}

func newTablesSession(t *testing.T) (*Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSession(db, tablesDialect{}), mock
}

// 方言没有提供表浏览时，打开表、翻页、计数明确报「不支持」，且不碰数据库（mock 没有预期
// 任何语句）。
func TestTableBrowsingWithoutTablesIsUnsupported(t *testing.T) {
	session, mock := newMockSession(t)
	ctx := context.Background()

	_, err := session.OpenTable(ctx, "db", "t", 10)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.TableRows(ctx, "db", "t", dbdriver.TableQuery{}, dbdriver.Page{Size: 10})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.CountTableRows(ctx, "db", "t", nil)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenTableAssemblesTheFirstScreen(t *testing.T) {
	session, mock := newTablesSession(t)
	mock.ExpectQuery("PRIMARY KEYS").WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("id"))
	mock.ExpectQuery("COLUMNS").WillReturnRows(
		sqlmock.NewRows([]string{"name", "type", "is_nullable", "dflt_value", "Extra"}).
			AddRow("id", "int", "NO", nil, "auto_increment").
			AddRow("name", "text", "YES", "anon", "").
			AddRow("blob", "", "yes", nil, nil))
	mock.ExpectQuery("SELECT COUNT(*) FROM <db>.<t>").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(42))
	mock.ExpectQuery("SELECT * FROM <db>.<t> LIMIT 2 OFFSET 0").WillReturnRows(
		sqlmock.NewRows([]string{"id", "name", "blob"}).AddRow(1, "alice", []byte("x")).AddRow(2, nil, nil))

	opened, err := session.OpenTable(context.Background(), "db", "t", 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"id"}, opened.PrimaryKeys)
	// 说不出类型的列不进类型表；可空性不分大小写；默认值非 NULL 才算有默认值。
	assert.Equal(t, map[string]string{"id": "int", "name": "text"}, opened.ColumnTypes)
	assert.Equal(t, []dbdriver.TableColumnRule{
		{Name: "id", AutoIncrement: true},
		{Name: "name", Nullable: true, HasDefault: true},
		{Name: "blob", Nullable: true},
	}, opened.ColumnRules)
	assert.Equal(t, int64(42), opened.TotalCount)
	// 第一页的列序来自查询结果；字节串转成字符串、NULL 保持为 nil。
	assert.Equal(t, []string{"id", "name", "blob"}, opened.FirstPage.Columns)
	assert.Equal(t, [][]any{{int64(1), "alice", "x"}, {int64(2), nil, nil}}, opened.FirstPage.Rows)
	assert.Nil(t, opened.FirstPage.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 四步中任何一步失败，整次打开失败，数据库的错误原样带出。
func TestOpenTableFailsAsAWholeWithTheDatabaseError(t *testing.T) {
	boom := errors.New("relation does not exist")
	steps := []struct {
		query string
		rows  *sqlmock.Rows
	}{
		{"PRIMARY KEYS", sqlmock.NewRows([]string{"name"})},
		{"COLUMNS", sqlmock.NewRows([]string{"name"})},
		{"SELECT COUNT(*) FROM <db>.<t>", sqlmock.NewRows([]string{"c"}).AddRow(0)},
		{"SELECT * FROM <db>.<t> LIMIT 10 OFFSET 0", sqlmock.NewRows([]string{"id"})},
	}
	for failing := range steps {
		t.Run(steps[failing].query, func(t *testing.T) {
			session, mock := newTablesSession(t)
			for i, step := range steps[:failing+1] {
				if i == failing {
					mock.ExpectQuery(step.query).WillReturnError(boom)
				} else {
					mock.ExpectQuery(step.query).WillReturnRows(step.rows)
				}
			}
			opened, err := session.OpenTable(context.Background(), "db", "t", 10)
			require.ErrorIs(t, err, boom)
			assert.Nil(t, opened)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestTableRowsPagesWithFiltersAndSorts(t *testing.T) {
	session, mock := newTablesSession(t)
	mock.ExpectQuery("SELECT * FROM <db>.<t> WHERE <name> = 'A' ORDER BY <id> ASC, <name> DESC LIMIT 50 OFFSET 100").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(7, []byte("A")))

	rows, err := session.TableRows(context.Background(), "db", "t", dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpEqual, Value: "A"}},
		Sorts: []dbdriver.SortItem{
			{Column: "id", Direction: dbdriver.SortAscending},
			{Column: "name", Direction: dbdriver.SortDescending},
		},
	}, dbdriver.Page{Index: 2, Size: 50})
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, rows.Columns)
	assert.Equal(t, [][]any{{int64(7), "A"}}, rows.Rows)
	assert.Nil(t, rows.Total, "the total comes from CountTableRows")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestTableRowsAndCountReturnTheDatabaseError(t *testing.T) {
	session, mock := newTablesSession(t)
	boom := errors.New("column does not exist")
	ctx := context.Background()

	mock.ExpectQuery("SELECT * FROM <db>.<t> LIMIT 10 OFFSET 0").WillReturnError(boom)
	_, err := session.TableRows(ctx, "db", "t", dbdriver.TableQuery{}, dbdriver.Page{Size: 10})
	require.ErrorIs(t, err, boom)

	mock.ExpectQuery("SELECT COUNT(*) FROM <db>.<t>").WillReturnError(boom)
	_, err = session.CountTableRows(ctx, "db", "t", nil)
	require.ErrorIs(t, err, boom)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 看不懂的请求在生成语句之前就被拒绝：什么都不发给数据库。
func TestMalformedTableQueriesAreRejectedBeforeAnyStatement(t *testing.T) {
	condition := dbdriver.FilterItem{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpEqual, Value: "A"}
	with := func(mutate func(*dbdriver.FilterItem)) []dbdriver.FilterItem {
		item := condition
		mutate(&item)
		return []dbdriver.FilterItem{item, condition}
	}
	for name, tt := range map[string]struct {
		query dbdriver.TableQuery
		want  string
	}{
		"unknown operator": {dbdriver.TableQuery{Filters: with(func(i *dbdriver.FilterItem) { i.Operator = "~*" })}, `unknown filter operator "~*"`},
		"missing operator": {dbdriver.TableQuery{Filters: with(func(i *dbdriver.FilterItem) { i.Operator = "" })}, `unknown filter operator ""`},
		"unknown kind":     {dbdriver.TableQuery{Filters: with(func(i *dbdriver.FilterItem) { i.Kind = "raw" })}, `unknown filter item kind "raw"`},
		"unknown join":     {dbdriver.TableQuery{Filters: with(func(i *dbdriver.FilterItem) { i.Join = "xor" })}, `unknown filter join "xor"`},
		"a value that is not a scalar or a list": {
			dbdriver.TableQuery{Filters: with(func(i *dbdriver.FilterItem) { i.Value = map[string]any{"a": 1} })}, "unsupported value"},
		"unknown sort direction": {
			dbdriver.TableQuery{Sorts: []dbdriver.SortItem{{Column: "id", Direction: "sideways"}}}, `unknown sort direction "sideways"`},
	} {
		t.Run(name, func(t *testing.T) {
			session, mock := newTablesSession(t)
			_, err := session.TableRows(context.Background(), "db", "t", tt.query, dbdriver.Page{Size: 10})
			require.ErrorContains(t, err, tt.want)
			if len(tt.query.Filters) > 0 {
				_, err = session.CountTableRows(context.Background(), "db", "t", tt.query.Filters)
				require.ErrorContains(t, err, tt.want)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// 数字照前端显示它的样子写进字面量：筛选的值经 JSON 到达时都是浮点数。
func TestFilterNumbersAreWrittenAsTheGridShowsThem(t *testing.T) {
	for want, value := range map[string]any{
		"'42'":                    float64(42),
		"'-7'":                    float64(-7),
		"'0'":                     float64(0),
		"'1.5'":                   1.5,
		"'0.000001'":              1e-6,
		"'1e-7'":                  1e-7,
		"'1.5e-7'":                1.5e-7,
		"'123456789012345680000'": 123456789012345678901.0,
		"'1e+21'":                 1e21,
		"'9007199254740993'":      int64(9007199254740993),
		"'3'":                     3,
	} {
		where, err := FilterWhere([]dbdriver.FilterItem{
			{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "n", Operator: dbdriver.OpEqual, Value: value},
		}, angle)
		require.NoError(t, err)
		assert.Equal(t, "<n> = "+want, where)
	}
}

// 整表读取只发一条语句：不带 LIMIT / OFFSET，行从同一个游标里逐行取出。
func TestStreamTableRowsIsOneStatementWithoutPaging(t *testing.T) {
	session, mock := newTablesSession(t)
	rows := sqlmock.NewRows([]string{"id", "name"})
	for i := 0; i < 2500; i++ {
		rows.AddRow(i, []byte("n"))
	}
	mock.ExpectQuery("SELECT * FROM <db>.<t> WHERE <name> = 'A' ORDER BY <id> DESC").WillReturnRows(rows)

	stream, err := session.StreamTableRows(context.Background(), "db", "t", dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpEqual, Value: "A"}},
		Sorts:   []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, stream.Columns())
	count := 0
	for stream.Next() {
		if count == 0 {
			assert.Equal(t, []any{int64(0), "n"}, stream.Row())
		}
		count++
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	assert.Equal(t, 2500, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStreamTableRowsReturnsTheDatabaseErrorAndRejectsMissingTables(t *testing.T) {
	session, mock := newTablesSession(t)
	boom := errors.New("column does not exist")
	mock.ExpectQuery("SELECT * FROM <db>.<t>").WillReturnError(boom)
	_, err := session.StreamTableRows(context.Background(), "db", "t", dbdriver.TableQuery{})
	require.ErrorIs(t, err, boom)

	plain, plainMock := newMockSession(t)
	_, err = plain.StreamTableRows(context.Background(), "db", "t", dbdriver.TableQuery{})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, plainMock.ExpectationsWereMet())
}

// 列注释取自结果集的注释列，没有就是空串；默认值 NULL（没有默认值）与空串默认值可区分。
func TestColumnMetasCarryCommentAndDefault(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("COLUMNS").WillReturnRows(
		sqlmock.NewRows([]string{"name", "type", "is_nullable", "dflt_value", "column_comment"}).
			AddRow("id", "int", "NO", nil, "primary id").
			AddRow("tag", "text", "YES", "", nil).
			AddRow("name", "text", "YES", []byte("anon"), ""))
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	columns, err := QueryTableColumns(context.Background(), conn, "COLUMNS")
	require.NoError(t, err)
	metas := ColumnMetas(columns)

	empty, anon := "", "anon"
	assert.Equal(t, "primary id", metas[0].Comment)
	assert.Nil(t, metas[0].Default)
	assert.Equal(t, "", metas[1].Comment)
	assert.Equal(t, &empty, metas[1].Default)
	assert.Equal(t, &anon, metas[2].Default)
}

// 驱动的列查询可以用 is_auto_increment / is_identity_always 两列报告自增：前者「新增行不必
// 填」，后者「数据库拒绝对它显式写值」。布尔、整数与文本三种常见表示都认；NULL 为假。
func TestQueryTableColumnsReadsAutoIncrementFlags(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("COLUMNS").WillReturnRows(
		sqlmock.NewRows([]string{"name", "is_auto_increment", "is_identity_always"}).
			AddRow("plain", false, false).
			AddRow("serial", true, false).
			AddRow("always", true, true).
			AddRow("mssql", int64(1), int64(1)).
			AddRow("null", nil, nil).
			AddRow("text", "YES", "NO"))
	conn, err := db.Conn(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	columns, err := QueryTableColumns(context.Background(), conn, "COLUMNS")
	require.NoError(t, err)

	got := map[string][2]bool{}
	for _, c := range columns {
		got[c.Name] = [2]bool{c.AutoIncrement, c.IdentityAlways}
	}
	assert.Equal(t, map[string][2]bool{
		"plain": {false, false}, "serial": {true, false}, "always": {true, true},
		"mssql": {true, true}, "null": {false, false}, "text": {true, false},
	}, got)
}
