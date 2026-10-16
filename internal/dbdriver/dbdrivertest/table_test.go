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
	RunTableConformance(t, fake, FakeSessions(fake), "app", "users")

	// 声明了复制为 SQL：行语句也要能生成。
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapCopySQL)
	RunTableConformance(t, fake, FakeSessions(fake), "app", "users")
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
	stmts func([]string, error) ([]string, error)
}

func (b brokenTables) RowStatements(ctx context.Context, database, table string, req dbdriver.RowStatementsRequest) ([]string, error) {
	statements, err := b.Session.RowStatements(ctx, database, table, req)
	if b.stmts != nil {
		return b.stmts(statements, err)
	}
	return statements, err
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
		"declared row statements refused": {brokenTables{stmts: func([]string, error) ([]string, error) {
			return nil, unsupported
		}}, "RowStatements"},
		"row statements fail": {brokenTables{stmts: func([]string, error) ([]string, error) {
			return nil, errors.New("boom")
		}}, "boom"},
		"a statement short": {brokenTables{stmts: func(statements []string, err error) ([]string, error) {
			if len(statements) == 0 {
				return statements, err
			}
			return statements[1:], err
		}}, "one per row"},
		"row statements without rows are null": {brokenTables{stmts: func(statements []string, err error) ([]string, error) {
			if len(statements) == 0 {
				return nil, err
			}
			return statements, err
		}}, "null"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := NewFake()
			fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapExportSQL)
			tt.broken.Session = openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
			problems := tableProblems(fake.Desc, tt.broken, "app", "users")
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

// 行语句由假会话直接给出：每行一条一眼能与真实方言区分的语句，收到的请求原样记下，不执行任何
// 语句；没声明复制 / 导出为 SQL 的假驱动拒绝，且什么也没记下。
func TestFakeSessionGeneratesRowStatementsWithoutExecuting(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
	ctx := context.Background()
	request := dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"name"},
		Rows:        []map[string]any{{"id": float64(1), "name": "alice"}, {"id": float64(2), "name": "bob"}},
		PrimaryKeys: []string{"id"},
	}

	_, err := session.RowStatements(ctx, "app", "users", request)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, session.TableCalls())

	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapCopySQL)
	statements, err := session.RowStatements(ctx, "app", "users", request)
	require.NoError(t, err)
	assert.Equal(t, []string{"FAKE UPDATE ROW 1 OF app.users", "FAKE UPDATE ROW 2 OF app.users"}, statements)
	assert.Equal(t, []TableCall{{Op: "row_statements", Database: "app", Table: "users", RowStatements: &request}}, session.TableCalls())
	assert.Empty(t, session.Statements())
}

// 整批执行：收到的请求原样记下，语句按次序执行（记进 Statements），空白语句跳过；StatementErrors
// 里的语句失败，整批的那次停在它并报告回滚，出错继续的那次接着执行。
func TestFakeSessionExecutesBatches(t *testing.T) {
	fake := NewFake()
	fake.StatementErrors = map[string]error{"BAD": errors.New("fakedb: rejected")}
	session := openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
	ctx := context.Background()

	atomic := dbdriver.BatchRequest{Statements: []string{"A", " ", "BAD", "C"}, Atomic: true, ContinueOnError: true, Database: "app", Table: "users"}
	result, err := session.ExecuteBatch(ctx, atomic)
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.BatchResult{
		Executed:   []dbdriver.BatchStatement{{Index: 0, AffectedRows: 3}, {Index: 2, Error: "fakedb: rejected"}},
		RolledBack: true,
	}, result)

	continuing := dbdriver.BatchRequest{Statements: []string{"BAD", "C"}, ContinueOnError: true, Database: "app", Table: "users"}
	result, err = session.ExecuteBatch(ctx, continuing)
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.BatchResult{
		Executed: []dbdriver.BatchStatement{{Index: 0, Error: "fakedb: rejected"}, {Index: 1, AffectedRows: 3}},
	}, result)

	assert.Equal(t, []TableCall{
		{Op: "batch", Database: "app", Table: "users", Batch: &atomic},
		{Op: "batch", Database: "app", Table: "users", Batch: &continuing},
	}, session.TableCalls())
	assert.Equal(t, []string{"A", "BAD", "BAD", "C"}, session.Statements())
}

// 没声明能关闭外键检查的假驱动拒绝关闭它，且什么也不记、不执行；声明了就照常执行。
func TestFakeSessionRefusesUndeclaredForeignKeyToggle(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
	request := dbdriver.BatchRequest{Statements: []string{"A"}, DisableForeignKeyChecks: true, Table: "users"}

	_, err := session.ExecuteBatch(context.Background(), request)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, session.TableCalls())
	assert.Empty(t, session.Statements())

	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapImport)
	fake.Desc.CanDisableForeignKeyChecks = true
	_, err = session.ExecuteBatch(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, []string{"A"}, session.Statements())
}

// brokenBatch 让整批执行的回答由测试改坏。
type brokenBatch struct {
	dbdriver.Session
	batch func(dbdriver.BatchRequest, *dbdriver.BatchResult, error) (*dbdriver.BatchResult, error)
}

func (b brokenBatch) ExecuteBatch(ctx context.Context, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	result, err := b.Session.ExecuteBatch(ctx, req)
	return b.batch(req, result, err)
}

// 整批执行的一致性检查也得能失败：声明了关闭外键检查却拒绝、整批失败却没回滚、失败没报告、
// 出错继续却停下。
func TestTableConformanceReportsBrokenBatches(t *testing.T) {
	for name, tt := range map[string]struct {
		batch func(dbdriver.BatchRequest, *dbdriver.BatchResult, error) (*dbdriver.BatchResult, error)
		want  string
	}{
		"declared foreign key toggle refused": {func(req dbdriver.BatchRequest, r *dbdriver.BatchResult, err error) (*dbdriver.BatchResult, error) {
			if req.DisableForeignKeyChecks {
				return nil, fmt.Errorf("%w: no", dbdriver.ErrUnsupported)
			}
			return r, err
		}, "ExecuteBatch"},
		"atomic batch not rolled back": {func(_ dbdriver.BatchRequest, r *dbdriver.BatchResult, err error) (*dbdriver.BatchResult, error) {
			if r != nil {
				r.RolledBack = false
			}
			return r, err
		}, "roll"},
		"failure not reported": {func(_ dbdriver.BatchRequest, r *dbdriver.BatchResult, err error) (*dbdriver.BatchResult, error) {
			if r != nil {
				for i := range r.Executed {
					r.Executed[i].Error = ""
				}
			}
			return r, err
		}, "fail"},
		"continue on error stops": {func(req dbdriver.BatchRequest, r *dbdriver.BatchResult, err error) (*dbdriver.BatchResult, error) {
			if r != nil && !req.Atomic && len(r.Executed) > 1 {
				r.Executed = r.Executed[:1]
			}
			return r, err
		}, "continue"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := NewFake()
			fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapImport)
			fake.Desc.CanDisableForeignKeyChecks = true
			session := brokenBatch{Session: openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"}), batch: tt.batch}
			problems := tableProblems(fake.Desc, session, "app", "users")
			require.NotEmpty(t, problems)
			assert.Contains(t, strings.Join(problems, "\n"), tt.want)
		})
	}
}

// 表格编辑的语句由假会话直接给出：新增、改动、删除各一组，每个一条一眼能与真实方言区分的语句，
// 收到的请求原样记下，不执行任何语句；没声明 grid_edit 的假驱动拒绝，且什么也没记下。
func TestFakeSessionGeneratesGridEditStatementsWithoutExecuting(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"})
	ctx := context.Background()
	request := dbdriver.GridEditRequest{
		Columns:     []string{"id", "name"},
		PrimaryKeys: []string{"id"},
		Inserts:     []dbdriver.GridInsert{{Values: []dbdriver.ColumnValue{{Column: "name", Value: "dan"}}}, {}},
		Updates:     []dbdriver.GridUpdate{{Row: map[string]any{"id": float64(1), "name": "alice"}, Set: []dbdriver.ColumnValue{{Column: "name", Value: "ally"}}}},
		Deletes:     []map[string]any{{"id": float64(2), "name": "bob"}},
	}

	_, err := session.GridEditStatements(ctx, "app", "users", request)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, session.TableCalls())

	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapGridEdit)
	statements, err := session.GridEditStatements(ctx, "app", "users", request)
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.GridEditStatements{
		Inserts: []string{"FAKE INSERT ROW 1 OF app.users", "FAKE INSERT ROW 2 OF app.users"},
		Updates: []string{"FAKE UPDATE ROW 1 OF app.users"},
		Deletes: []string{"FAKE DELETE ROW 1 OF app.users"},
	}, statements)
	assert.Equal(t, []TableCall{{Op: "grid_edit", Database: "app", Table: "users", GridEdit: &request}}, session.TableCalls())
	assert.Empty(t, session.Statements())
}

// brokenGridEdit 让表格编辑语句的回答由测试改坏。
type brokenGridEdit struct {
	dbdriver.Session
	grid func(*dbdriver.GridEditStatements, error) (*dbdriver.GridEditStatements, error)
}

func (b brokenGridEdit) GridEditStatements(ctx context.Context, database, table string, req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	return b.grid(b.Session.GridEditStatements(ctx, database, table, req))
}

// 表格编辑的一致性检查也得能失败：声明了却拒绝、生成失败、少给一条、空请求给出 null。
func TestTableConformanceReportsBrokenGridEdits(t *testing.T) {
	for name, tt := range map[string]struct {
		grid func(*dbdriver.GridEditStatements, error) (*dbdriver.GridEditStatements, error)
		want string
	}{
		"declared grid edit refused": {func(*dbdriver.GridEditStatements, error) (*dbdriver.GridEditStatements, error) {
			return nil, fmt.Errorf("%w: no", dbdriver.ErrUnsupported)
		}, "GridEditStatements"},
		"grid edit fails": {func(*dbdriver.GridEditStatements, error) (*dbdriver.GridEditStatements, error) {
			return nil, errors.New("boom")
		}, "boom"},
		"an update short": {func(s *dbdriver.GridEditStatements, err error) (*dbdriver.GridEditStatements, error) {
			if s != nil && len(s.Updates) > 0 {
				s.Updates = s.Updates[1:]
			}
			return s, err
		}, "updates"},
		"empty request gives null": {func(s *dbdriver.GridEditStatements, err error) (*dbdriver.GridEditStatements, error) {
			if s != nil && len(s.Deletes) == 0 {
				s.Deletes = nil
			}
			return s, err
		}, "null"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := NewFake()
			fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapGridEdit)
			session := brokenGridEdit{Session: openFake(t, fake, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"}), grid: tt.grid}
			problems := tableProblems(fake.Desc, session, "app", "users")
			require.NotEmpty(t, problems)
			assert.Contains(t, strings.Join(problems, "\n"), tt.want)
		})
	}
}

func TestFakeDriverWithGridEditPassesTableConformance(t *testing.T) {
	fake := NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapGridEdit)
	fake.Desc.LocatesRowWithoutPrimaryKey = true
	RunTableConformance(t, fake, FakeSessions(fake), "app", "users")
}

func TestFakeDriverWithForeignKeyTogglePassesTableConformance(t *testing.T) {
	fake := NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapImport)
	fake.Desc.CanDisableForeignKeyChecks = true
	RunTableConformance(t, fake, FakeSessions(fake), "app", "users")
}
