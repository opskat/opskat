package dbdriver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statementDumpSource 是还会自己写出表的 INSERT 的库（TableStatementSource）：pages 里有的表按给定的页
// 交出语句（页读完之后 Next 以 nextErr 失败），其余的表经 RowInsertStatements 与 PageStatements，读的是同一个
// dumpSource 的行。它记下收到的请求。
type statementDumpSource struct {
	*dumpSource
	pages    map[string][]TableStatementPage
	nextErr  map[string]error
	requests []TableStatementsRequest
}

func (s *statementDumpSource) TableStatements(ctx context.Context, database, table string, req TableStatementsRequest) (TableStatementPages, error) {
	s.calls = append(s.calls, "statements "+database+"."+table)
	s.requests = append(s.requests, req)
	if pages, ok := s.pages[table]; ok {
		return &scriptedPages{source: s.dumpSource, pages: pages, err: s.nextErr[table]}, nil
	}
	cursor, err := RowInsertStatements(ctx, s.dumpSource, database, table, req)
	if err != nil {
		return nil, err
	}
	return PageStatements(cursor, req), nil
}

type scriptedPages struct {
	source *dumpSource
	pages  []TableStatementPage
	err    error
}

func (p *scriptedPages) Next() (*TableStatementPage, error) {
	p.source.calls = append(p.source.calls, "next page")
	if len(p.pages) == 0 {
		return nil, p.err
	}
	page := p.pages[0]
	p.pages = p.pages[1:]
	return &page, nil
}

func (p *scriptedPages) Close() error {
	p.source.calls = append(p.source.calls, "close pages")
	return nil
}

// 会话自己写语句时，文件与经游标、writer 写出的一字不差：同样的语句、同样的批分隔符位置（一块 500 行，大的
// 行按页分开也不影响），值的写法也一样（二进制、超过 2^53 的整数、写不进 JSON 的浮点数）。转储交给会话的
// 请求带着方言给出的列与 InsertModifier，表名不带库名，只读这张表自己的行。
func TestDumpThroughTableStatementsWritesWhatTheCursorPathWrites(t *testing.T) {
	newSource := func() *dumpSource {
		source := newDumpSource()
		users := make([][]any, 2*dumpChunkRows+1)
		for i := range users {
			users[i] = []any{int64(i), fmt.Sprintf("user %d", i)}
		}
		users[7] = []any{int64(9007199254740993), "\x00\xff\x10"}
		users[8] = []any{math.NaN(), []byte{0xfe, 0x01}}
		source.rows["users"] = users
		source.ddl["docs"] = "CREATE TABLE docs (id int, body text)"
		source.columns["docs"] = []string{"id", "body"}
		for i := range 10 {
			source.rows["docs"] = append(source.rows["docs"], []any{int64(i), strings.Repeat(string(rune('a'+i)), 1<<20)})
		}
		return source
	}
	dialect := func() *dumpDialect {
		return &dumpDialect{
			file:           DumpFileScript{BatchTerminator: "GO"},
			dataColumn:     map[string][]string{"users": {"id", "name"}},
			insertModifier: map[string]string{"users": "OVERRIDING SYSTEM VALUE"},
		}
	}
	dump := func(source DumpSource) string {
		var out bytes.Buffer
		_, err := Dump(context.Background(), DumpRequest{
			Database: "shop", Tables: []string{"users", "docs", "orders"}, WithData: true, Header: dumpHeader(), Writer: &out,
		}, source, dialect())
		require.NoError(t, err)
		return out.String()
	}

	viaCursor := dump(newSource())
	statements := &statementDumpSource{dumpSource: newSource()}
	viaSession := dump(statements)

	assert.True(t, viaSession == viaCursor, "the files differ")
	assert.Equal(t, []TableStatementsRequest{
		{Columns: []string{"id", "name"}, InsertModifier: "OVERRIDING SYSTEM VALUE", OmitDatabase: true, OwnRows: true, PageBytes: DumpPageBytes, BatchRows: dumpChunkRows},
		{OmitDatabase: true, OwnRows: true, PageBytes: DumpPageBytes, BatchRows: dumpChunkRows},
		{OmitDatabase: true, OwnRows: true, PageBytes: DumpPageBytes, BatchRows: dumpChunkRows},
	}, statements.requests)
}

// 每一页写出它的语句后报一次进度，带上到目前为止写完的行数：一行的语句分在几页里时，中间那页写完的行数是 0。
// 会话的语句流读完就关掉，转储自己不再读游标、不再备 writer。
func TestDumpThroughTableStatementsReportsProgressPerPage(t *testing.T) {
	source := &statementDumpSource{dumpSource: newDumpSource(), pages: map[string][]TableStatementPage{"users": {
		{Statements: []string{"INSERT 1;", "INSERT 2;"}, Rows: 2},
		{Statements: []string{"BEGIN 3 part 1; END;"}},
		{Statements: []string{"BEGIN 3 part 2; END;", "INSERT 4;"}, Rows: 2, Done: true},
	}}}
	var out bytes.Buffer
	var rows []int64
	var finished []DumpedObject

	result, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users"}, WithData: true, Header: dumpHeader(), Writer: &out,
		Progress: func(p DumpProgress) {
			if p.Finished != nil {
				finished = append(finished, *p.Finished)
			} else if p.Rows > 0 || len(rows) > 0 {
				rows = append(rows, p.Rows)
			}
		},
	}, source, &dumpDialect{plain: true})
	require.NoError(t, err)

	assert.Contains(t, out.String(), "-- Records of users\n-- ----------------------------\nINSERT 1;\nINSERT 2;\nBEGIN 3 part 1; END;\nBEGIN 3 part 2; END;\nINSERT 4;\n")
	assert.Equal(t, []int64{2, 2, 4}, rows)
	assert.Equal(t, []DumpedObject{{Name: "users", Kind: DumpTable, Data: true, Rows: 4}}, finished)
	assert.EqualValues(t, 4, result.Rows)
	assert.Equal(t, []string{"objects shop", "ddl shop.users", "statements shop.users", "next page", "next page", "next page", "close pages"}, source.calls)
}

// 页与页之间被取消（ctx）时停下，错误是取消本身；不再要下一页，语句流照样关掉。
func TestDumpThroughTableStatementsStopsWhenCanceledBetweenPages(t *testing.T) {
	page := TableStatementPage{Statements: []string{"INSERT 1;"}, Rows: 1}
	source := &statementDumpSource{dumpSource: newDumpSource(), pages: map[string][]TableStatementPage{"users": {page, page, page}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := Dump(ctx, DumpRequest{
		Database: "shop", Tables: []string{"users"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
		Progress: func(p DumpProgress) {
			if p.Rows > 0 {
				cancel()
			}
		},
	}, source, &dumpDialect{})
	require.ErrorIs(t, err, context.Canceled)
	var objectErr *DumpObjectError
	require.ErrorAs(t, err, &objectErr)
	assert.Equal(t, "users", objectErr.Object)
	assert.Equal(t, []string{"objects shop", "ddl shop.users", "statements shop.users", "next page", "close pages"}, source.calls)
}

// 会话给不出下一页时停在这张表上：错误点名这张表，语句流照样关掉，后面的表不再读。
func TestDumpThroughTableStatementsStopsAtAFailedPage(t *testing.T) {
	source := &statementDumpSource{
		dumpSource: newDumpSource(),
		pages:      map[string][]TableStatementPage{"users": {{Statements: []string{"INSERT 1;"}, Rows: 1}}},
		nextErr:    map[string]error{"users": errors.New("connection reset")},
	}

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users", "orders"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, &dumpDialect{})
	var objectErr *DumpObjectError
	require.ErrorAs(t, err, &objectErr)
	assert.Equal(t, "users", objectErr.Object)
	assert.ErrorContains(t, err, "connection reset")
	assert.Contains(t, source.calls, "close pages")
	assert.NotContains(t, source.calls, "statements shop.orders")
}

// listCursor 逐条给出 statements，读完之后以 err 结束。
type listCursor struct {
	statements []cursorStatement
	err        error
	current    cursorStatement
	closed     bool
}

type cursorStatement struct {
	text   string
	rowEnd bool
}

// rowsOf 是每行一条语句的行。
func rowsOf(texts ...string) []cursorStatement {
	out := make([]cursorStatement, len(texts))
	for i, text := range texts {
		out[i] = cursorStatement{text: text, rowEnd: true}
	}
	return out
}

func (c *listCursor) Next() bool {
	if len(c.statements) == 0 {
		return false
	}
	c.current, c.statements = c.statements[0], c.statements[1:]
	return true
}
func (c *listCursor) Statement() string { return c.current.text }
func (c *listCursor) RowEnd() bool      { return c.current.rowEnd }
func (c *listCursor) Err() error {
	if len(c.statements) == 0 {
		return c.err
	}
	return nil
}
func (c *listCursor) Close() error {
	c.closed = true
	return nil
}

// readPages 读完 pages（最多 20 页），给出各页与最后的错误。
func readPages(t *testing.T, pages TableStatementPages) ([]TableStatementPage, error) {
	t.Helper()
	var out []TableStatementPage
	for range 20 {
		page, err := pages.Next()
		if err != nil {
			return out, err
		}
		out = append(out, *page)
		if page.Done {
			return out, nil
		}
	}
	t.Fatal("the pages never ended")
	return nil, nil
}

func TestPageStatements(t *testing.T) {
	req := TableStatementsRequest{PageBytes: 10, BatchRows: 100}
	x := func(n int) string { return strings.Repeat("x", n) }

	t.Run("a page ends before the statement that would take it past the size", func(t *testing.T) {
		pages, err := readPages(t, PageStatements(&listCursor{statements: rowsOf("aaaa", "bbbb", "cccc")}, req))
		require.NoError(t, err)
		assert.Equal(t, []TableStatementPage{
			{Statements: []string{"aaaa", "bbbb"}, Rows: 2},
			{Statements: []string{"cccc"}, Rows: 1, Done: true},
		}, pages)
	})

	t.Run("a statement larger than a page comes whole, alone", func(t *testing.T) {
		pages, err := readPages(t, PageStatements(&listCursor{statements: rowsOf(x(25), "y")}, req))
		require.NoError(t, err)
		assert.Equal(t, []TableStatementPage{
			{Statements: []string{x(25)}, Rows: 1},
			{Statements: []string{"y"}, Rows: 1, Done: true},
		}, pages)
	})

	t.Run("a row of several statements may span pages and counts where its last statement is", func(t *testing.T) {
		cursor := &listCursor{statements: []cursorStatement{{text: "p1:" + x(3)}, {text: "p2:" + x(3)}, {text: "p3:" + x(3), rowEnd: true}, {text: "z", rowEnd: true}}}
		pages, err := readPages(t, PageStatements(cursor, req))
		require.NoError(t, err)
		assert.Equal(t, []TableStatementPage{
			{Statements: []string{"p1:" + x(3)}},
			{Statements: []string{"p2:" + x(3)}},
			{Statements: []string{"p3:" + x(3), "z"}, Rows: 2, Done: true},
		}, pages)
	})

	t.Run("a page ends where a batch of rows does", func(t *testing.T) {
		batched := TableStatementsRequest{PageBytes: 1000, BatchRows: 2}
		pages, err := readPages(t, PageStatements(&listCursor{statements: rowsOf("1", "2", "3", "4", "5")}, batched))
		require.NoError(t, err)
		assert.Equal(t, []TableStatementPage{
			{Statements: []string{"1", "2"}, Rows: 2},
			{Statements: []string{"3", "4"}, Rows: 2},
			{Statements: []string{"5"}, Rows: 1, Done: true},
		}, pages)

		pages, err = readPages(t, PageStatements(&listCursor{statements: rowsOf("1", "2")}, batched))
		require.NoError(t, err)
		assert.Equal(t, []TableStatementPage{{Statements: []string{"1", "2"}, Rows: 2}, {Statements: []string{}, Done: true}}, pages)
	})

	t.Run("an empty table is one empty last page", func(t *testing.T) {
		pages, err := readPages(t, PageStatements(&listCursor{}, req))
		require.NoError(t, err)
		assert.Equal(t, []TableStatementPage{{Statements: []string{}, Done: true}}, pages)
	})

	t.Run("a failing cursor fails the page, and closing closes it", func(t *testing.T) {
		cursor := &listCursor{statements: rowsOf("a"), err: errors.New("connection reset")}
		pages := PageStatements(cursor, req)
		_, err := readPages(t, pages)
		require.ErrorContains(t, err, "connection reset")
		require.NoError(t, pages.Close())
		assert.True(t, cursor.closed)
	})

	t.Run("statements ending in the middle of a row fail", func(t *testing.T) {
		_, err := readPages(t, PageStatements(&listCursor{statements: []cursorStatement{{text: "part 1"}}}, req))
		require.Error(t, err)
	})
}

// 经游标与 writer 写语句时，writer 在游标打开之前备好（远端 SQLite 只有一条连接，游标占着它时不能再查列
// 类型）；关掉语句游标就关掉了表的游标。
func TestRowInsertStatementsPreparesTheWriterBeforeTheCursor(t *testing.T) {
	source := newDumpSource()
	cursor, err := RowInsertStatements(context.Background(), source, "shop", "users", TableStatementsRequest{OmitDatabase: true})
	require.NoError(t, err)
	var statements []string
	for cursor.Next() {
		assert.True(t, cursor.RowEnd())
		statements = append(statements, cursor.Statement())
	}
	require.NoError(t, cursor.Err())
	require.NoError(t, cursor.Close())

	assert.Equal(t, []string{"INSERT INTO users (id, name) VALUES (1, alice);", "INSERT INTO users (id, name) VALUES (2, <nil>);"}, statements)
	assert.Equal(t, []string{"prepare shop.users", "stream shop.users", "close stream"}, source.calls)
}

// 不是最后一页却没有语句的页违反了约定（转储会一直要下一页）：停在这张表上。
func TestDumpThroughTableStatementsRefusesAnEmptyPageBeforeTheEnd(t *testing.T) {
	source := &statementDumpSource{dumpSource: newDumpSource(), pages: map[string][]TableStatementPage{"users": {
		{Statements: []string{}}, {Statements: []string{}, Done: true},
	}}}

	_, err := Dump(context.Background(), DumpRequest{
		Database: "shop", Tables: []string{"users"}, WithData: true, Header: dumpHeader(), Writer: &bytes.Buffer{},
	}, source, &dumpDialect{})
	var objectErr *DumpObjectError
	require.ErrorAs(t, err, &objectErr)
	assert.Equal(t, "users", objectErr.Object)
	assert.Contains(t, source.calls, "close pages")
}
