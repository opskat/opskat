package dbdrivertest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestFakeDriverPassesTableConformance(t *testing.T) {
	fake := NewFake()
	RunTableConformance(t, FakeSessions(fake), "app", "users")
}

// 假驱动的表浏览不经过任何语句：结果直接来自内存里的表，收到的结构化请求原样记下。
func TestFakeSessionBrowsesItsTableWithoutStatements(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
	ctx := context.Background()
	query := dbdriver.TableQuery{
		Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpIsNotNull}},
		Sorts:   []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}},
	}

	opened, err := session.OpenTable(ctx, "app", "users", 2)
	require.NoError(t, err)
	assert.Equal(t, fake.PrimaryKeys, opened.PrimaryKeys)
	assert.Equal(t, fake.ColumnTypes, opened.ColumnTypes)
	assert.Equal(t, fake.ColumnRules, opened.ColumnRules)
	assert.Equal(t, int64(3), opened.TotalCount)
	assert.Equal(t, fake.Rows[:2], opened.FirstPage.Rows)

	rows, err := session.TableRows(ctx, "app", "users", query, dbdriver.Page{Index: 1, Size: 2})
	require.NoError(t, err)
	assert.Equal(t, fake.Rows[2:], rows.Rows)

	total, err := session.CountTableRows(ctx, "app", "users", query.Filters)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)

	assert.Equal(t, []TableCall{
		{Op: "open", Database: "app", Table: "users", Page: dbdriver.Page{Size: 2}},
		{Op: "rows", Database: "app", Table: "users", Query: query, Page: dbdriver.Page{Index: 1, Size: 2}},
		{Op: "count", Database: "app", Table: "users", Query: dbdriver.TableQuery{Filters: query.Filters}},
	}, session.TableCalls())
	assert.Empty(t, session.Statements())
}

func TestFakeSessionFailsTableBrowsingWithItsError(t *testing.T) {
	fake := NewFake()
	fake.TableErr = errors.New("fakedb: no such table")
	session := openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
	ctx := context.Background()

	_, err := session.OpenTable(ctx, "app", "users", 2)
	require.ErrorIs(t, err, fake.TableErr)
	_, err = session.TableRows(ctx, "app", "users", dbdriver.TableQuery{}, dbdriver.Page{Size: 2})
	require.ErrorIs(t, err, fake.TableErr)
	_, err = session.CountTableRows(ctx, "app", "users", nil)
	require.ErrorIs(t, err, fake.TableErr)
	assert.Empty(t, session.TableCalls())
}

// --- 表浏览的一致性检查也得能失败 ---

// brokenTables 在假会话的表浏览之上按需改坏一处。
type brokenTables struct {
	dbdriver.Session
	open  func(*dbdriver.OpenedTable, error) (*dbdriver.OpenedTable, error)
	rows  func(*dbdriver.RowSet, error) (*dbdriver.RowSet, error)
	count func(int64, error) (int64, error)
}

func (b brokenTables) OpenTable(ctx context.Context, database, table string, pageSize int) (*dbdriver.OpenedTable, error) {
	opened, err := b.Session.OpenTable(ctx, database, table, pageSize)
	if b.open != nil {
		return b.open(opened, err)
	}
	return opened, err
}

func (b brokenTables) TableRows(ctx context.Context, database, table string, query dbdriver.TableQuery, page dbdriver.Page) (*dbdriver.RowSet, error) {
	rows, err := b.Session.TableRows(ctx, database, table, query, page)
	if b.rows != nil {
		return b.rows(rows, err)
	}
	return rows, err
}

func (b brokenTables) CountTableRows(ctx context.Context, database, table string, filters []dbdriver.FilterItem) (int64, error) {
	total, err := b.Session.CountTableRows(ctx, database, table, filters)
	if b.count != nil {
		return b.count(total, err)
	}
	return total, err
}

func TestTableConformanceReportsBrokenSessions(t *testing.T) {
	unsupported := fmt.Errorf("%w: tables", dbdriver.ErrUnsupported)
	alterOpened := func(mutate func(*dbdriver.OpenedTable)) func(*dbdriver.OpenedTable, error) (*dbdriver.OpenedTable, error) {
		return func(opened *dbdriver.OpenedTable, err error) (*dbdriver.OpenedTable, error) {
			mutate(opened)
			return opened, err
		}
	}
	alterRows := func(mutate func(*dbdriver.RowSet)) func(*dbdriver.RowSet, error) (*dbdriver.RowSet, error) {
		return func(rows *dbdriver.RowSet, err error) (*dbdriver.RowSet, error) {
			mutate(rows)
			return rows, err
		}
	}
	for name, tt := range map[string]struct {
		broken brokenTables
		want   string
	}{
		"open refused": {brokenTables{open: func(*dbdriver.OpenedTable, error) (*dbdriver.OpenedTable, error) {
			return nil, unsupported
		}}, "OpenTable"},
		"open returns nothing": {brokenTables{open: func(*dbdriver.OpenedTable, error) (*dbdriver.OpenedTable, error) {
			return nil, nil
		}}, "no result"},
		"open without a first page": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) { o.FirstPage = nil })}, "first page"},
		"first page without columns": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) {
			o.FirstPage = &dbdriver.RowSet{}
		})}, "columns"},
		"first page larger than asked": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) {
			o.FirstPage = &dbdriver.RowSet{Columns: []string{"id", "name"}, Rows: make([][]any, ConformancePageSize+1)}
			for i := range o.FirstPage.Rows {
				o.FirstPage.Rows[i] = []any{i, "x"}
			}
			o.TotalCount = int64(len(o.FirstPage.Rows))
		})}, "more than"},
		"total smaller than the first page": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) { o.TotalCount = 1 })}, "total 1"},
		"types for an unknown column": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) {
			o.ColumnTypes = map[string]string{"ghost": "int"}
		})}, "ghost"},
		"rules for an unknown column": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) {
			o.ColumnRules = []dbdriver.TableColumnRule{{Name: "ghost"}}
		})}, "ghost"},
		"primary key on an unknown column": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) {
			o.PrimaryKeys = []string{"ghost"}
		})}, "ghost"},
		"rows fail": {brokenTables{rows: func(*dbdriver.RowSet, error) (*dbdriver.RowSet, error) {
			return nil, errors.New("boom")
		}}, "boom"},
		"rows return nothing": {brokenTables{rows: func(*dbdriver.RowSet, error) (*dbdriver.RowSet, error) {
			return nil, nil
		}}, "no row set"},
		"row shorter than the columns": {brokenTables{rows: alterRows(func(r *dbdriver.RowSet) { r.Rows = [][]any{{1}} })}, "values"},
		"page reports a total": {brokenTables{rows: alterRows(func(r *dbdriver.RowSet) {
			total := int64(3)
			r.Total = &total
		})}, "must not report a total"},
		"page columns differ from the opened table": {brokenTables{rows: alterRows(func(r *dbdriver.RowSet) {
			r.Columns = []string{"name", "id"}
		})}, "differ"},
		"unfiltered page differs from the first page": {brokenTables{rows: alterRows(func(r *dbdriver.RowSet) {
			r.Rows = r.Rows[:1]
		})}, "first page"},
		"count fails":                           {brokenTables{count: func(int64, error) (int64, error) { return 0, unsupported }}, "CountTableRows"},
		"count disagrees with the opened table": {brokenTables{count: func(int64, error) (int64, error) { return 99, nil }}, "99"},
	} {
		t.Run(name, func(t *testing.T) {
			tt.broken.Session = openFake(t, NewFake(), &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
			problems := tableProblems(tt.broken, "app", "users")
			require.NotEmpty(t, problems)
			assert.Contains(t, strings.Join(problems, "\n"), tt.want)
		})
	}
}

// 整表读取是一次对会话的调用，行原样交出，不经过任何语句。
func TestFakeSessionStreamsItsTableInOneCall(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
	query := dbdriver.TableQuery{Sorts: []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}}}

	stream, err := session.StreamTableRows(context.Background(), "app", "users", query)
	require.NoError(t, err)
	assert.Equal(t, fake.Columns, stream.Columns())
	var got [][]any
	for stream.Next() {
		got = append(got, stream.Row())
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	assert.Equal(t, fake.Rows, got)
	assert.Equal(t, []TableCall{{Op: "stream", Database: "app", Table: "users", Query: query}}, session.TableCalls())
	assert.Empty(t, session.Statements())

	fake.TableErr = errors.New("fakedb: no such table")
	_, err = session.StreamTableRows(context.Background(), "app", "users", query)
	require.ErrorIs(t, err, fake.TableErr)
}

// 建表语句与起始查询语句都由假会话直接回答，不经过任何语句；没声明 CapViewDDL 的假驱动
// 拒绝查看建表语句，且什么也没记下。
func TestFakeSessionServesDDLAndStarterQueryWithoutStatements(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
	ctx := context.Background()

	_, err := session.TableDDL(ctx, "app", "users")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)

	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapViewDDL)
	ddl, err := session.TableDDL(ctx, "app", "users")
	require.NoError(t, err)
	assert.Equal(t, fake.DDL, ddl)

	starter, err := session.StarterQuery(ctx, "app", "users", 50)
	require.NoError(t, err)
	assert.Equal(t, "FAKE SELECT FIRST 50 FROM app.users", starter)

	assert.Equal(t, []TableCall{
		{Op: "ddl", Database: "app", Table: "users"},
		{Op: "starter", Database: "app", Table: "users", Page: dbdriver.Page{Size: 50}},
	}, session.TableCalls())
	assert.Empty(t, session.Statements())
}
