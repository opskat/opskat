package query_svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// fakeSession 打开假驱动的一条会话：它不是 *sql.DB，导出只能经会话的表浏览方法取数。
func fakeSession(t *testing.T, fake *dbdrivertest.Fake) *dbdrivertest.FakeSession {
	t.Helper()
	session := dbdrivertest.FakeSessions(fake)(t)
	t.Cleanup(func() { _ = session.Close() })
	return session.(*dbdrivertest.FakeSession)
}

func exportToXlsx(t *testing.T, session dbdriver.Session, request TableExportRequest) (*ParsedXlsxTable, error) {
	t.Helper()
	var buf bytes.Buffer
	if err := ExportTableRowsToXlsx(context.Background(), session, "reporting", "users", request, &buf); err != nil {
		return nil, err
	}
	return ParseXlsx(bytes.NewReader(buf.Bytes()), "")
}

// 驱动给出的行原样写进工作簿；取数是带结构化筛选与排序的表浏览请求，没有任何语句。
func TestExportTableRowsToXlsxWritesTheDriversRows(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Columns = []string{"id", "name", "note"}
	fake.Rows = [][]any{{1, "alice", "hi"}, {2, "bob", nil}, {3, "carol", "x"}}
	session := fakeSession(t, fake)
	filters := []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpContains, Value: "a"}}
	sorts := []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}}

	parsed, err := exportToXlsx(t, session, TableExportRequest{Filters: filters, Sorts: sorts, Scope: ExportScopeAll})
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name", "note"}, parsed.Headers)
	// 数字读回来是格式化后的文本；NULL 是空单元格。
	assert.Equal(t, [][]string{{"1", "alice", "hi"}, {"2", "bob", ""}, {"3", "carol", "x"}}, parsed.Rows)

	calls := session.TableCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, dbdrivertest.TableCall{
		Op: "stream", Database: "reporting", Table: "users",
		Query: dbdriver.TableQuery{Filters: filters, Sorts: sorts},
	}, calls[0])
	assert.Empty(t, session.Statements())
}

// 当前页：只取请求里的那一页。
func TestExportTableRowsToXlsxPageScope(t *testing.T) {
	session := fakeSession(t, dbdrivertest.NewFake())

	parsed, err := exportToXlsx(t, session, TableExportRequest{Scope: ExportScopePage, Page: 1, PageSize: 2})
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"3", "carol"}}, parsed.Rows)
	require.Len(t, session.TableCalls(), 1)
	assert.Equal(t, dbdriver.Page{Index: 1, Size: 2}, session.TableCalls()[0].Page)
}

// 整表是对驱动的一次读取（一个游标），而不是分成多页的多次查询：没有稳定顺序时分页会重复或漏行。
func TestExportTableRowsToXlsxAllScopeReadsTheTableInOneCall(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Columns = []string{"id"}
	fake.Rows = nil
	total := 2005
	for i := 0; i < total; i++ {
		fake.Rows = append(fake.Rows, []any{i})
	}
	session := fakeSession(t, fake)

	parsed, err := exportToXlsx(t, session, TableExportRequest{Scope: ExportScopeAll})
	require.NoError(t, err)
	require.Len(t, parsed.Rows, total)
	assert.Equal(t, []string{"0"}, parsed.Rows[0])
	assert.Equal(t, []string{fmt.Sprint(total - 1)}, parsed.Rows[total-1])
	require.Len(t, session.TableCalls(), 1)
	assert.Equal(t, "stream", session.TableCalls()[0].Op)
}

// 选了列：只写这些列，按选的顺序；没有行时表头仍在。
func TestExportTableRowsToXlsxProjectsSelectedColumns(t *testing.T) {
	fake := dbdrivertest.NewFake()
	session := fakeSession(t, fake)

	parsed, err := exportToXlsx(t, session, TableExportRequest{Scope: ExportScopeAll, Columns: []string{"name", "id"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"name", "id"}, parsed.Headers)
	assert.Equal(t, []string{"alice", "1"}, parsed.Rows[0])

	_, err = exportToXlsx(t, session, TableExportRequest{Scope: ExportScopeAll, Columns: []string{"nope"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")

	fake.Rows = nil
	parsed, err = exportToXlsx(t, session, TableExportRequest{Scope: ExportScopeAll})
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, parsed.Headers)
	assert.Empty(t, parsed.Rows)
}

func TestExportTableRowsToXlsxRejectsAnUnknownScope(t *testing.T) {
	session := fakeSession(t, dbdrivertest.NewFake())
	_, err := exportToXlsx(t, session, TableExportRequest{Scope: "everything"})
	require.Error(t, err)
	assert.Empty(t, session.TableCalls())
}

// 驱动的错误（数据库错误，或驱动不提供表浏览的「不支持」）原样返回，不执行任何语句。
func TestExportTableRowsToXlsxReturnsTheDriversError(t *testing.T) {
	for name, tableErr := range map[string]error{
		"database error": errors.New("fakedb: relation does not exist"),
		"unsupported":    fmt.Errorf("%w: no table rows", dbdriver.ErrUnsupported),
	} {
		t.Run(name, func(t *testing.T) {
			fake := dbdrivertest.NewFake()
			fake.TableErr = tableErr
			session := fakeSession(t, fake)
			_, err := exportToXlsx(t, session, TableExportRequest{Scope: ExportScopeAll})
			require.ErrorIs(t, err, tableErr)
			assert.Empty(t, session.Statements())
		})
	}
}

// 扩展驱动的会话跨边界带回的数字是 json.Number（保住 BIGINT 的位数）：写进工作簿仍是数字
// 单元格，与内置驱动的整数、浮点一样，不是文本。
func TestExportTableRowsToXlsxWritesNumbersFromAnExtensionDriverAsNumbers(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Columns = []string{"id", "ratio", "huge"}
	fake.Rows = [][]any{{json.Number("9007199254740993"), json.Number("0.25"), json.Number("123456789012345678901234567890")}}
	session := fakeSession(t, fake)

	var buf bytes.Buffer
	require.NoError(t, ExportTableRowsToXlsx(context.Background(), session, "reporting", "users",
		TableExportRequest{Scope: ExportScopeAll}, &buf))
	f, err := excelize.OpenReader(&buf)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	for _, cell := range []string{"A2", "B2"} {
		typ, err := f.GetCellType(xlsxSheetName, cell)
		require.NoError(t, err)
		// 数字单元格不带字符串类型（excelize 读回 Unset 或 Number）；文本是 InlineString。
		assert.Contains(t, []excelize.CellType{excelize.CellTypeUnset, excelize.CellTypeNumber}, typ, "cell %s", cell)
	}
	id, err := f.GetCellValue(xlsxSheetName, "A2", excelize.Options{RawCellValue: true})
	require.NoError(t, err)
	assert.Equal(t, "9007199254740993", id, "an integer keeps every digit")
	huge, err := f.GetCellValue(xlsxSheetName, "C2")
	require.NoError(t, err)
	assert.Equal(t, "123456789012345678901234567890", huge, "a number no cell type holds exactly stays its digits")
}
