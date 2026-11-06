package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// rowsDialect 在测试方言之上写行语句：ntext 列加 N 前缀，UPDATE 以 ONE ROW 收尾，好与任何
// 真实方言区分。
type rowsDialect struct{ tablesDialect }

func (rowsDialect) Literals(context.Context, *sql.Conn) (Literals, error) {
	return Literals{UnicodeText: func(columnType string) bool { return columnType == "ntext" }}, nil
}

func (rowsDialect) UpdateRowSQL(tableRef, set, where string) string {
	return "UPDATE " + tableRef + " SET " + set + " WHERE " + where + " ONE ROW;"
}

func newRowsSession(t *testing.T) (*Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSession(db, rowsDialect{}), mock
}

func expectColumnTypes(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("COLUMNS").WillReturnRows(sqlmock.NewRows([]string{"column_name", "data_type"}).
		AddRow("id", "int").AddRow("name", "ntext").AddRow("memo", "text"))
}

// 每行一条 INSERT：列名按方言加引号，值按列的类型写成字面量，行里缺的列是 NULL。
func TestRowStatementsInsertsOneStatementPerRow(t *testing.T) {
	session, mock := newRowsSession(t)
	expectColumnTypes(mock)

	statements, err := dbdrivertest.RowStatements(context.Background(), session, "db", "t", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowInsert,
		Columns: []string{"id", "name", "memo"},
		Rows: []map[string]any{
			{"id": float64(1), "name": "中文", "memo": nil},
			{"id": float64(2), "name": "O'Reilly"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"INSERT INTO <db>.<t> (<id>, <name>, <memo>) VALUES ('1', N'中文', NULL);",
		"INSERT INTO <db>.<t> (<id>, <name>, <memo>) VALUES ('2', N'O''Reilly', NULL);",
	}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// UPDATE 按主键定位那一行（定位的值同样按列类型写），没有主键时按全部列定位，NULL 用 IS NULL；
// 只改一行的写法是方言的。
func TestRowStatementsUpdatesLocateTheRowByPrimaryKeyOrEveryColumn(t *testing.T) {
	session, mock := newRowsSession(t)
	expectColumnTypes(mock)
	statements, err := dbdrivertest.RowStatements(context.Background(), session, "db", "t", dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowUpdate,
		Columns:     []string{"memo"},
		Rows:        []map[string]any{{"id": float64(2), "name": "中文", "memo": "x"}},
		PrimaryKeys: []string{"id", "name"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE <db>.<t> SET <memo> = 'x' WHERE <id> = '2' AND <name> = N'中文' ONE ROW;"}, statements)

	expectColumnTypes(mock)
	statements, err = dbdrivertest.RowStatements(context.Background(), session, "db", "t", dbdriver.RowStatementsRequest{
		Kind:    dbdriver.RowUpdate,
		Columns: []string{"id", "memo"},
		Rows:    []map[string]any{{"id": float64(2), "memo": nil}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"UPDATE <db>.<t> SET <id> = '2', <memo> = NULL WHERE <id> = '2' AND <memo> IS NULL ONE ROW;"}, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 备好的 writer 不再碰数据库：会话关了也照样生成（整表导出为 SQL 时读游标占着会话唯一的连接）；
// 没有行时给出空列表（序列化成 []，不是 null）。
func TestPreparedRowStatementsNeedNoDatabase(t *testing.T) {
	session, mock := newRowsSession(t)
	expectColumnTypes(mock)
	writer, err := session.PrepareRowStatements(context.Background(), "db", "t")
	require.NoError(t, err)
	mock.ExpectClose()
	require.NoError(t, session.Close())

	statements, err := writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id", "name"}, Rows: []map[string]any{{"id": float64(1), "name": "中文"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"INSERT INTO <db>.<t> (<id>, <name>) VALUES ('1', N'中文');"}, statements)
	statements, err = writer.RowStatements(dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: []string{"id"}})
	require.NoError(t, err)
	assert.NotNil(t, statements)
	assert.Empty(t, statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 方言不写行语句时在碰数据库之前拒绝；查列失败时数据库的错误原样返回；请求的种类不认识、值写不成
// 字面量时生成报错。
func TestRowStatementsRefusalsAndErrors(t *testing.T) {
	ctx := context.Background()
	request := dbdriver.RowStatementsRequest{Kind: dbdriver.RowInsert, Columns: []string{"id"}, Rows: []map[string]any{{"id": 1}}}

	plain, plainMock := newTablesSession(t)
	_, err := plain.PrepareRowStatements(ctx, "db", "t")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, plainMock.ExpectationsWereMet())

	session, mock := newRowsSession(t)
	boom := errors.New("no such table")
	mock.ExpectQuery("COLUMNS").WillReturnError(boom)
	_, err = session.PrepareRowStatements(ctx, "db", "t")
	require.ErrorIs(t, err, boom)

	expectColumnTypes(mock)
	writer, err := session.PrepareRowStatements(ctx, "db", "t")
	require.NoError(t, err)
	_, err = writer.RowStatements(dbdriver.RowStatementsRequest{Kind: "upsert", Rows: request.Rows})
	require.ErrorContains(t, err, "upsert")
	_, err = writer.RowStatements(dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id"}, Rows: []map[string]any{{"id": []string{"x"}}},
	})
	require.ErrorContains(t, err, "unsupported value")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 带 schema 分组的驱动只在第一个点处拆开 schema 与表名：名字里带点的表作为一个整体。
func TestSplitSchemaTableAtTheFirstDot(t *testing.T) {
	for table, want := range map[string][2]string{
		"events":           {"public", "events"},
		"reporting.events": {"reporting", "events"},
		"public.a.b":       {"public", "a.b"},
		"":                 {"public", ""},
	} {
		schema, name := SplitSchemaTable(table, "public")
		assert.Equal(t, want, [2]string{schema, name}, "table %q", table)
	}
}

func TestQuoteSchemaTable(t *testing.T) {
	for name, want := range map[string]string{
		"users":            "<users>",
		"reporting.events": "<reporting>.<events>",
		"public.a.b":       "<public>.<a.b>",
	} {
		assert.Equal(t, want, QuoteSchemaTable(name, angle), "name %q", name)
	}
}
