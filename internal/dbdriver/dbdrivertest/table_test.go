package dbdrivertest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestFakeDriverPassesTableConformance(t *testing.T) {
	fake := NewFake()
	RunTableConformance(t, fake, FakeSessions(fake), "app", "users")

	// 声明了复制为 SQL 与导入：行语句与导入语句也要能生成。
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapCopySQL, dbdriver.CapImport)
	RunTableConformance(t, fake, FakeSessions(fake), "app", "users")
}

// 假驱动的表浏览不经过任何语句：结果直接来自内存里的表，收到的结构化请求原样记下。
func TestFakeSessionBrowsesItsTableWithoutStatements(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
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
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
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
	// prepare 非 nil 时备 writer 以它失败。
	prepare error
	// touchesDatabase 让备好的 writer 生成时还要用会话。
	touchesDatabase bool
	// imports 改写导入语句的结果。
	imports func([]string, error) ([]string, error)
}

func (b brokenTables) ImportStatements(ctx context.Context, database, table string, req dbdriver.ImportRequest) ([]string, error) {
	statements, err := b.Session.ImportStatements(ctx, database, table, req)
	if b.imports != nil {
		return b.imports(statements, err)
	}
	return statements, err
}

func (b brokenTables) PrepareRowStatements(ctx context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	if b.prepare != nil {
		return nil, b.prepare
	}
	writer, err := b.Session.PrepareRowStatements(ctx, database, table)
	if err != nil {
		return nil, err
	}
	return brokenRowWriter{writer: writer, tables: b, database: database, table: table}, nil
}

// brokenRowWriter 改写 writer 生成的结果；tables.touchesDatabase 时它像没备好一切的 writer
// 那样每次生成都回会话上查一次表。
type brokenRowWriter struct {
	writer          dbdriver.RowStatementWriter
	tables          brokenTables
	database, table string
}

func (w brokenRowWriter) RowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	if w.tables.touchesDatabase {
		if _, err := w.tables.Session.CountTableRows(context.Background(), w.database, w.table, nil); err != nil {
			return nil, err
		}
	}
	statements, err := w.writer.RowStatements(req)
	if w.tables.stmts != nil {
		return w.tables.stmts(statements, err)
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
		"count fails":                              {brokenTables{count: func(int64, error) (int64, error) { return 0, unsupported }}, "CountTableRows"},
		"count disagrees with the opened table":    {brokenTables{count: func(int64, error) (int64, error) { return 99, nil }}, "99"},
		"declared row statements refused":          {brokenTables{prepare: unsupported}, "PrepareRowStatements"},
		"preparing row statements fails":           {brokenTables{prepare: errors.New("no columns")}, "no columns"},
		"prepared row writer touches the database": {brokenTables{touchesDatabase: true}, "must not touch the database"},
		"row statements fail": {brokenTables{stmts: func([]string, error) ([]string, error) {
			return nil, errors.New("boom")
		}}, "boom"},
		"a statement short": {brokenTables{stmts: func(statements []string, err error) ([]string, error) {
			if len(statements) == 0 {
				return statements, err
			}
			return statements[1:], err
		}}, "one per row"},
		"categories for an unknown column": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) {
			o.ColumnCategories = map[string]dbdriver.ColumnCategory{"ghost": dbdriver.CategoryText}
		})}, "ghost"},
		"an unknown category": {brokenTables{open: alterOpened(func(o *dbdriver.OpenedTable) {
			o.ColumnCategories = map[string]dbdriver.ColumnCategory{"id": "weird"}
		})}, "weird"},
		"declared import refused": {brokenTables{imports: func([]string, error) ([]string, error) {
			return nil, unsupported
		}}, "ImportStatements"},
		"an import statement short": {brokenTables{imports: func(statements []string, err error) ([]string, error) {
			if len(statements) == 0 {
				return statements, err
			}
			return statements[1:], err
		}}, "ImportStatements"},
		"import without rows is null": {brokenTables{imports: func(statements []string, err error) ([]string, error) {
			if len(statements) == 0 {
				return nil, err
			}
			return statements, err
		}}, "ImportStatements without rows"},
		"row statements without rows are null": {brokenTables{stmts: func(statements []string, err error) ([]string, error) {
			if len(statements) == 0 {
				return nil, err
			}
			return statements, err
		}}, "null"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := NewFake()
			fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapExportSQL, dbdriver.CapImport)
			tt.broken.Session = openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
			problems := tableProblems(fake.Desc, tt.broken, "app", "users")
			require.NotEmpty(t, problems)
			assert.Contains(t, strings.Join(problems, "\n"), tt.want)
		})
	}
}

// 整表读取是一次对会话的调用，行原样交出，不经过任何语句。
func TestFakeSessionStreamsItsTableInOneCall(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
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
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
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
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
	ctx := context.Background()
	request := dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"name"},
		Rows:        []map[string]any{{"id": float64(1), "name": "alice"}, {"id": float64(2), "name": "bob"}},
		PrimaryKeys: []string{"id"},
	}

	_, err := session.PrepareRowStatements(ctx, "app", "users")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, session.TableCalls())

	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapCopySQL)
	writer, err := session.PrepareRowStatements(ctx, "app", "users")
	require.NoError(t, err)
	statements, err := writer.RowStatements(request)
	require.NoError(t, err)
	assert.Equal(t, []string{"FAKE UPDATE ROW 1 OF app.users", "FAKE UPDATE ROW 2 OF app.users"}, statements)
	assert.Equal(t, []TableCall{
		{Op: "prepare_row_statements", Database: "app", Table: "users"},
		{Op: "row_statements", Database: "app", Table: "users", RowStatements: &request},
	}, session.TableCalls())
	assert.Empty(t, session.Statements())
}

// 整批执行：收到的请求原样记下，语句按次序执行（记进 Statements），空白语句跳过；StatementErrors
// 里的语句失败，整批的那次停在它并报告回滚，出错继续的那次接着执行。
func TestFakeSessionExecutesBatches(t *testing.T) {
	fake := NewFake()
	fake.StatementErrors = map[string]error{"BAD": errors.New("fakedb: rejected")}
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
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
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
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
			session := brokenBatch{Session: openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"}), batch: tt.batch}
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
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
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
			session := brokenGridEdit{Session: openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"}), grid: tt.grid}
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

// 建表、删表、清空、建库的语句由假会话直接给出：每个操作一条一眼能与真实方言区分的语句，收到的请求
// 原样记下，不执行任何语句；没声明对应功能的假驱动拒绝，且什么也没记下。
func TestFakeSessionGeneratesSchemaStatementsWithoutExecuting(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
	ctx := context.Background()
	create := dbdriver.CreateTableRequest{Name: "users", Columns: []dbdriver.NewColumn{{Name: "id", Type: "INT"}}}
	createDB := dbdriver.CreateDatabaseRequest{Name: "reports", Options: map[string]string{"collation": "c"}}

	call := func() []error {
		_, e1 := session.CreateTableStatements(ctx, "app", create)
		_, e2 := session.DropTableStatements(ctx, "app", "users")
		_, e3 := session.TruncateTableStatements(ctx, "app", "users")
		_, e4 := session.CreateDatabaseStatements(ctx, createDB)
		return []error{e1, e2, e3, e4}
	}
	for _, err := range call() {
		require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	}
	assert.Empty(t, session.TableCalls())

	fake.Desc.Capabilities = append(fake.Desc.Capabilities,
		dbdriver.CapCreateTable, dbdriver.CapDropTable, dbdriver.CapTruncateTable, dbdriver.CapCreateDatabase)
	for _, err := range call() {
		require.NoError(t, err)
	}
	assert.Equal(t, []TableCall{
		{Op: "create_table", Database: "app", Table: "users", CreateTable: &create},
		{Op: "drop_table", Database: "app", Table: "users"},
		{Op: "truncate_table", Database: "app", Table: "users"},
		{Op: "create_database", Table: "reports", CreateDatabase: &createDB},
	}, session.TableCalls())
	assert.Empty(t, session.Statements())
}

// 改表：假会话把表元数据当作这张表给出，原样交回时没有语句，改了什么就给一条一眼能与真实方言区分的
// 语句；收到的请求原样记下，不执行任何语句。没声明改表的假驱动拒绝，且什么也没记下。
func TestFakeSessionAltersTablesWithoutExecuting(t *testing.T) {
	fake := NewFake()
	session := openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
	ctx := context.Background()
	rename := dbdriver.AlterTableRequest{Name: "people"}

	_, err := session.AlterTableModel(ctx, "app", "users")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = session.AlterTableStatements(ctx, "app", "users", rename)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Empty(t, session.TableCalls())

	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapAlterTable)
	model, err := session.AlterTableModel(ctx, "app", "users")
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableModel{Name: "users", Columns: []dbdriver.ColumnDefinition{
		{Name: "id", Type: "int", Comment: "primary key"},
		{Name: "name", Type: "text", Nullable: true, DefaultValue: "anon"},
	}}, model)
	result, err := session.AlterTableStatements(ctx, "app", "users", rename)
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{Statements: []string{"FAKE ALTER TABLE app.users"}, Table: "people"}, result)
	assert.Equal(t, []TableCall{
		{Op: "alter_model", Database: "app", Table: "users"},
		{Op: "alter_table", Database: "app", Table: "users", AlterTable: &rename},
	}, session.TableCalls())
	assert.Empty(t, session.Statements())

	fake.Desc.AlterTableAttributes = []dbdriver.AlterAttribute{dbdriver.AlterColumnType}
	RunTableConformance(t, fake, FakeSessions(fake), "app", "users")
}

// brokenAlter 在假会话的改表之上按需改坏一处。
type brokenAlter struct {
	dbdriver.Session
	model func(*dbdriver.AlterTableModel) *dbdriver.AlterTableModel
	stmts func(*dbdriver.AlterTableStatements) *dbdriver.AlterTableStatements
}

func (b brokenAlter) AlterTableModel(ctx context.Context, database, table string) (*dbdriver.AlterTableModel, error) {
	model, err := b.Session.AlterTableModel(ctx, database, table)
	if b.model != nil && err == nil {
		return b.model(model), nil
	}
	return model, err
}

func (b brokenAlter) AlterTableStatements(ctx context.Context, database, table string, req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
	result, err := b.Session.AlterTableStatements(ctx, database, table, req)
	if b.stmts != nil && err == nil {
		return b.stmts(result), nil
	}
	return result, err
}

// 改表的一致性检查也得能失败：声明了却给不出成形的结果，或没声明却放行。
func TestTableConformanceReportsBrokenAlterTable(t *testing.T) {
	for name, tt := range map[string]struct {
		broken brokenAlter
		want   string
	}{
		"model names a column the table does not have": {brokenAlter{model: func(m *dbdriver.AlterTableModel) *dbdriver.AlterTableModel {
			m.Columns = append(m.Columns, dbdriver.ColumnDefinition{Name: "ghost"})
			return m
		}}, "ghost"},
		"model name is not the end of the table name": {brokenAlter{model: func(m *dbdriver.AlterTableModel) *dbdriver.AlterTableModel {
			m.Name = "app.users"
			return m
		}}, "app.users"},
		"statements for an unchanged table": {brokenAlter{stmts: func(r *dbdriver.AlterTableStatements) *dbdriver.AlterTableStatements {
			r.Statements = []string{"ALTER TABLE users"}
			return r
		}}, "nothing changed"},
		"statements are null": {brokenAlter{stmts: func(r *dbdriver.AlterTableStatements) *dbdriver.AlterTableStatements {
			r.Statements = nil
			return r
		}}, "null"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := NewFake()
			fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapAlterTable)
			tt.broken.Session = openFake(t, fake, &dbdriver.Config{Driver: "fakedb", Host: "h"})
			problems := tableProblems(fake.Desc, tt.broken, "app", "users")
			require.NotEmpty(t, problems)
			assert.Contains(t, strings.Join(problems, "\n"), tt.want)
		})
	}

	served := NewFake()
	served.Desc.Capabilities = append(served.Desc.Capabilities, dbdriver.CapAlterTable)
	session := openFake(t, served, &dbdriver.Config{Driver: "fakedb", Host: "h"})
	problems := strings.Join(undeclaredObjectProblems(context.Background(), NewFake().Desc, session), "\n")
	assert.Contains(t, problems, "AlterTableModel")
	assert.Contains(t, problems, "AlterTableStatements")
}
