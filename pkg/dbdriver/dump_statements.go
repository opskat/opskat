package dbdriver

import (
	"context"
	"errors"
	"io"
)

// DumpPageBytes 是转储按页取一张表的语句时，一页语句文本的大约字节数（TableStatementsRequest.PageBytes）。
const DumpPageBytes = 4 << 20

// TableStatementSource 是 DumpSource 可选的能力：会话自己读出一张表的行、写成它们的 INSERT，按页交给转储
// 写进文件。DumpSource 实现了它时转储用它写每张表的数据，值不经过转储编排；没实现时照旧经游标
// （StreamTableRows）读行、经 writer（PrepareRowStatements）写语句。写出的语句与经游标、writer 的写法一样
// （每行一条「复制为 INSERT」的 INSERT，见 RowInsertStatements），只有写不进一条语句的行，实现可以写成依次
// 执行的几条。
type TableStatementSource interface {
	// TableStatements 打开一张表的语句流（table 用 ListTables 给出的形式），按 req 写语句、分页。调用方读完或
	// 放弃都必须 Close 返回的流。
	TableStatements(ctx context.Context, database, table string, req TableStatementsRequest) (TableStatementPages, error)
}

// TableStatementsRequest 是 TableStatementSource.TableStatements 要写的语句与分页方式。
type TableStatementsRequest struct {
	// Columns 是 INSERT 写入的列，按次序；nil 表示读出的全部列（同 DumpObjectScript.DataColumns）。
	Columns []string `json:"columns"`
	// InsertModifier、OmitDatabase 同 RowStatementsRequest 的。
	InsertModifier string `json:"insertModifier,omitempty"`
	OmitDatabase   bool   `json:"omitDatabase,omitempty"`
	// OwnRows 同 TableQuery.OwnRows：只读这张表自己的行。
	OwnRows bool `json:"ownRows,omitempty"`
	// PageBytes 是一页语句文本的大约字节数：一页在再加一条就超过它之前结束，但至少有一条语句（一条语句不拆开，
	// 比它大的语句单独成一页）。
	PageBytes int `json:"pageBytes"`
	// BatchRows 是一块数据的行数：写完的行数每到它的整数倍，那一页就在这一行的最后一条语句之后结束，转储在块与
	// 块之间写批分隔符（DumpFileScript.BatchTerminator），与经游标、writer 写出的位置一样。
	BatchRows int `json:"batchRows"`
}

// TableStatementPages 是一张表按页给出的语句（TableStatementSource.TableStatements 打开的）。
type TableStatementPages interface {
	// Next 给出下一页。Done 的那一页之后不再调用；返回 error 时整张表失败。
	Next() (*TableStatementPage, error)
	// Close 释放流占用的游标与连接。
	io.Closer
}

// TableStatementPage 是一页语句。
type TableStatementPage struct {
	// Statements 是写进文件的语句原文（含结束符），按次序，每条写一行。不是最后一页时至少有一条。
	Statements []string `json:"statements"`
	// Rows 是在这一页写完的行数：一行的语句可以分在几页里，在它最后一条语句的那一页才算写完，所以可以是 0。
	Rows int `json:"rows"`
	// Done 表示表里的行都已写完，这是最后一页（可以没有语句）。
	Done bool `json:"done"`
}

// StatementCursor 是逐条给出一张表的语句的游标，PageStatements 把它分成页。读取方式同 RowStream：Next 前进，
// Statement 给出当前语句，Next 返回 false 后用 Err 区分读完与出错。
type StatementCursor interface {
	Next() bool
	// Statement 是当前语句的原文（含结束符）。
	Statement() string
	// RowEnd 表示当前语句是它那一行的最后一条。最后一条语句总是一行的结束。
	RowEnd() bool
	Err() error
	io.Closer
}

// PageStatements 把 cursor 的语句按 req 的 PageBytes 与 BatchRows 分成页（见 TableStatementsRequest）：一页在再加
// 一条语句就超过 PageBytes 之前结束（那一条留到下一页），至少有一条；写完的行数到 BatchRows 的整数倍时在那一行
// 之后结束；cursor 读完的那一页是最后一页。cursor 出错、或读完时最后一行没有结束，Next 返回 error。Close 关闭
// cursor。
func PageStatements(cursor StatementCursor, req TableStatementsRequest) TableStatementPages {
	return &statementPager{cursor: cursor, pageBytes: req.PageBytes, batchRows: req.BatchRows}
}

type statementPager struct {
	cursor               StatementCursor
	pageBytes, batchRows int
	// pending 是读出了、但放不进上一页的那条语句。
	pending *heldStatement
	// inBatch 是这一块里已经写完的行数；midRow 表示最后给出的语句不是它那一行的最后一条。
	inBatch int
	midRow  bool
}

type heldStatement struct {
	text   string
	rowEnd bool
}

func (p *statementPager) Next() (*TableStatementPage, error) {
	page := &TableStatementPage{Statements: []string{}}
	size := 0
	for {
		if p.pending == nil {
			if !p.cursor.Next() {
				if err := p.cursor.Err(); err != nil {
					return nil, err
				}
				if p.midRow {
					return nil, errors.New("the table's statements ended in the middle of a row")
				}
				page.Done = true
				return page, nil
			}
			p.pending = &heldStatement{text: p.cursor.Statement(), rowEnd: p.cursor.RowEnd()}
		}
		if len(page.Statements) > 0 && size+len(p.pending.text) > p.pageBytes {
			return page, nil
		}
		statement := p.pending
		p.pending = nil
		page.Statements = append(page.Statements, statement.text)
		size += len(statement.text)
		p.midRow = !statement.rowEnd
		if !statement.rowEnd {
			continue
		}
		page.Rows++
		if p.inBatch++; p.inBatch == p.batchRows {
			p.inBatch = 0
			return page, nil
		}
	}
}

func (p *statementPager) Close() error { return p.cursor.Close() }

// TableRowSource 是读一张表的行、为它写行语句用到的会话操作（Session 都有）。
type TableRowSource interface {
	PrepareRowStatements(ctx context.Context, database, table string) (RowStatementWriter, error)
	StreamTableRows(ctx context.Context, database, table string, query TableQuery) (RowStream, error)
}

// RowInsertStatements 是经 source 的游标与 writer 写一张表的语句：没有 TableStatementSource 的会话转储时写的那些
// （每行一条，与「复制为 INSERT」对这些行给出的一字不差，见 InsertStatements），一行一行地读、一行一行地写。
// writer 在游标打开之前备好（游标占着连接时，远端 SQLite 只有一条，不能再去查列类型）；Close 关闭游标。
func RowInsertStatements(ctx context.Context, source TableRowSource, database, table string, req TableStatementsRequest) (StatementCursor, error) {
	writer, err := source.PrepareRowStatements(ctx, database, table)
	if err != nil {
		return nil, err
	}
	rows, err := source.StreamTableRows(ctx, database, table, TableQuery{OwnRows: req.OwnRows})
	if err != nil {
		return nil, err
	}
	columns := req.Columns
	if columns == nil {
		columns = rows.Columns()
	}
	return &rowInsertCursor{rows: rows, writer: writer, req: RowStatementsRequest{
		Kind: RowInsert, Columns: columns, OmitDatabase: req.OmitDatabase, InsertModifier: req.InsertModifier,
	}}, nil
}

// rowInsertCursor 逐行读游标、为每一行写语句。
type rowInsertCursor struct {
	rows   RowStream
	writer RowStatementWriter
	req    RowStatementsRequest
	// statements 是当前这一行还没给出的语句（writer 每行给一条）。
	statements []string
	current    string
	err        error
}

func (c *rowInsertCursor) Next() bool {
	for len(c.statements) == 0 {
		if c.err != nil || !c.rows.Next() {
			return false
		}
		c.statements, c.err = InsertStatements(c.writer, c.req, []map[string]any{rowMap(c.rows.Columns(), c.rows.Row())})
		if c.err != nil {
			return false
		}
	}
	c.current, c.statements = c.statements[0], c.statements[1:]
	return true
}

func (c *rowInsertCursor) Statement() string { return c.current }
func (c *rowInsertCursor) RowEnd() bool      { return len(c.statements) == 0 }

func (c *rowInsertCursor) Err() error {
	if c.err != nil {
		return c.err
	}
	return c.rows.Err()
}

func (c *rowInsertCursor) Close() error { return c.rows.Close() }
