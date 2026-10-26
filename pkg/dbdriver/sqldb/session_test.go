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

func newMockSession(t *testing.T) (*Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSession(db, LimitOffset{}), mock
}

func TestExecuteReturnsQueryRowsAsPlainValues(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery("SELECT name, nullable FROM t").WillReturnRows(
		sqlmock.NewRows([]string{"name", "nullable"}).AddRow([]byte("alice"), nil),
	)

	res, err := session.Execute(context.Background(), "SELECT name, nullable FROM t")
	require.NoError(t, err)
	require.NotNil(t, res.Rows)
	assert.Equal(t, []string{"name", "nullable"}, res.Rows.Columns)
	// 字节串转成字符串、NULL 保持为 nil：结果可以直接序列化成 JSON。
	assert.Equal(t, [][]any{{"alice", nil}}, res.Rows.Rows)
	assert.Nil(t, res.Rows.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecuteReportsAffectedRowsForStatementsWithoutRows(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectExec("DELETE FROM t").WillReturnResult(sqlmock.NewResult(0, 4))

	res, err := session.Execute(context.Background(), "DELETE FROM t")
	require.NoError(t, err)
	assert.Nil(t, res.Rows)
	assert.Equal(t, int64(4), res.AffectedRows)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 这些前缀的语句返回结果集，按查询执行；其余按不返回行的语句执行。
func TestExecuteTreatsRowReturningStatementsAsQueries(t *testing.T) {
	for _, statement := range []string{
		"select 1", "  SHOW TABLES", "DESCRIBE t", "DESC t", "EXPLAIN SELECT 1",
		`PRAGMA table_info("t")`, "WITH x AS (SELECT 1) SELECT * FROM x",
	} {
		t.Run(statement, func(t *testing.T) {
			session, mock := newMockSession(t)
			mock.ExpectQuery(statement).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))

			res, err := session.Execute(context.Background(), statement)
			require.NoError(t, err)
			assert.NotNil(t, res.Rows)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestExecuteWrapsTheDatabaseError(t *testing.T) {
	boom := errors.New("boom")

	session, mock := newMockSession(t)
	mock.ExpectQuery("SELECT 1").WillReturnError(boom)
	_, err := session.Execute(context.Background(), "SELECT 1")
	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "SQL query failed")

	session, mock = newMockSession(t)
	mock.ExpectExec("DELETE FROM t").WillReturnError(boom)
	_, err = session.Execute(context.Background(), "DELETE FROM t")
	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "SQL execution failed")
}

func TestExecutePagedCountsThenFetchesThePageWithTheDialect(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT * FROM users) AS _t").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(42))
	mock.ExpectQuery("SELECT * FROM (SELECT * FROM users) AS _t LIMIT 10 OFFSET 20").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(21))

	// 末尾的分号要去掉，否则包成子查询就是语法错误。
	res, err := session.ExecutePaged(context.Background(), "SELECT * FROM users; ", dbdriver.Page{Index: 2, Size: 10})
	require.NoError(t, err)
	require.NotNil(t, res.Rows)
	assert.Equal(t, [][]any{{int64(21)}}, res.Rows.Rows)
	require.NotNil(t, res.Rows.Total)
	assert.Equal(t, int64(42), *res.Rows.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// SHOW / DESCRIBE / EXPLAIN / PRAGMA 不能包成子查询，写语句更不能：照原样执行，不给总数。
func TestExecutePagedRunsUnpageableStatementsAsIs(t *testing.T) {
	session, mock := newMockSession(t)
	mock.ExpectQuery("SHOW TABLES").WillReturnRows(sqlmock.NewRows([]string{"t"}).AddRow("users"))
	res, err := session.ExecutePaged(context.Background(), "SHOW TABLES", dbdriver.Page{Index: 0, Size: 10})
	require.NoError(t, err)
	require.NotNil(t, res.Rows)
	assert.Nil(t, res.Rows.Total)
	require.NoError(t, mock.ExpectationsWereMet())

	session, mock = newMockSession(t)
	mock.ExpectExec("UPDATE t SET a = 1").WillReturnResult(sqlmock.NewResult(0, 2))
	res, err = session.ExecutePaged(context.Background(), "UPDATE t SET a = 1", dbdriver.Page{Index: 0, Size: 10})
	require.NoError(t, err)
	assert.Nil(t, res.Rows)
	assert.Equal(t, int64(2), res.AffectedRows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestExecutePagedWrapsACountFailure(t *testing.T) {
	boom := errors.New("boom")
	session, mock := newMockSession(t)
	mock.ExpectQuery("SELECT COUNT(*) FROM (SELECT * FROM users) AS _t").WillReturnError(boom)

	_, err := session.ExecutePaged(context.Background(), "SELECT * FROM users", dbdriver.Page{Index: 0, Size: 10})
	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "count query failed")
}

type recordCloser struct {
	name  string
	order *[]string
	err   error
}

func (c *recordCloser) Close() error {
	*c.order = append(*c.order, c.name)
	return c.err
}

// 远端 SQLite 的 WAL 库在 db.Close() 时会回调远端文件系统删除 -wal/-shm，所以 *sql.DB
// 必须先于注销那套文件系统的 closer 关闭，否则回调会命中已注销的文件系统而 panic。
func TestCloseClosesTheDatabaseBeforeReleasingWhatItRunsOn(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	var order []string
	dbClosed := func() bool { return db.PingContext(context.Background()) != nil }
	vfs := &observingCloser{recordCloser: recordCloser{name: "vfs", order: &order}, dbClosed: dbClosed}
	sftp := &observingCloser{recordCloser: recordCloser{name: "sftp", order: &order}, dbClosed: dbClosed}
	mock.ExpectClose()

	require.NoError(t, NewSession(db, LimitOffset{}, vfs, sftp).Close())

	assert.Equal(t, []string{"vfs", "sftp"}, order, "releases run in the order given")
	assert.True(t, vfs.sawDBClosed, "the database must already be closed when the first release runs")
	assert.True(t, sftp.sawDBClosed)
	require.NoError(t, mock.ExpectationsWereMet())
}

type observingCloser struct {
	recordCloser
	dbClosed    func() bool
	sawDBClosed bool
}

func (c *observingCloser) Close() error {
	c.sawDBClosed = c.dbClosed()
	return c.recordCloser.Close()
}

// 一步失败不能挡住后面的释放，也不能被吞掉。
func TestCloseReleasesEverythingAndReportsEveryFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	dbErr, vfsErr := errors.New("db close failed"), errors.New("vfs close failed")
	mock.ExpectClose().WillReturnError(dbErr)
	var order []string
	vfs := &recordCloser{name: "vfs", order: &order, err: vfsErr}
	sftp := &recordCloser{name: "sftp", order: &order}

	err = NewSession(db, LimitOffset{}, vfs, sftp).Close()

	require.ErrorIs(t, err, dbErr)
	require.ErrorIs(t, err, vfsErr)
	assert.Equal(t, []string{"vfs", "sftp"}, order)
}

func TestEstablishPingsBeforeApplyingReadOnly(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectPing()
	mock.ExpectExec("SET READ ONLY").WillReturnResult(sqlmock.NewResult(0, 0))

	err = Establish(context.Background(), db, func(ctx context.Context, db *sql.DB) error {
		_, execErr := db.ExecContext(ctx, "SET READ ONLY")
		return execErr
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEstablishClosesTheDatabaseWhenItCannotBeReached(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	refused := errors.New("connection refused")
	mock.ExpectPing().WillReturnError(refused)
	mock.ExpectClose()

	err = Establish(context.Background(), db, func(context.Context, *sql.DB) error {
		t.Fatal("read-only must not be applied to a database that cannot be reached")
		return nil
	})
	require.ErrorIs(t, err, refused)
	assert.Contains(t, err.Error(), "数据库连接失败")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEstablishClosesTheDatabaseWhenReadOnlyCannotBeApplied(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	denied := errors.New("permission denied")
	mock.ExpectPing()
	mock.ExpectClose()

	err = Establish(context.Background(), db, func(context.Context, *sql.DB) error { return denied })
	require.ErrorIs(t, err, denied)
	assert.Contains(t, err.Error(), "设置只读模式失败")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestParseParams(t *testing.T) {
	assert.Equal(t, map[string]string{"charset": "utf8mb4", "parseTime": "true"}, ParseParams("charset=utf8mb4&parseTime=true"))
	assert.Empty(t, ParseParams(""))
}
