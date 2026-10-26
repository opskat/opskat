package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// fkDialect 是能关闭外键检查的方言：语句里带着库名与表名，证明批把目标表交给了方言。
type fkDialect struct{ LimitOffset }

func (fkDialect) ForeignKeyChecksSQL(_ context.Context, _ *sql.Conn, database, table string) (string, string, error) {
	return "FK OFF " + database + "." + table, "FK ON " + database + "." + table, nil
}

// fkOffDialect 是连接本来就没在检查外键的方言：既不关闭，也不恢复。
type fkOffDialect struct{ LimitOffset }

func (fkOffDialect) ForeignKeyChecksSQL(context.Context, *sql.Conn, string, string) (string, string, error) {
	return "", "", nil
}

// 连接本来就没在检查外键：批照常执行，前后都不碰外键检查。
func TestExecuteBatchLeavesForeignKeyChecksAloneWhenTheyWereOff(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	session := NewSession(db, fkOffDialect{})
	mock.ExpectExec("INSERT INTO users VALUES (1);").WillReturnResult(sqlmock.NewResult(0, 1))

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO users VALUES (1);"}, ContinueOnError: true,
		DisableForeignKeyChecks: true, Database: "appdb", Table: "users",
	})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, AffectedRows: 1}}, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func newFKMockSession(t *testing.T) (*Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return NewSession(db, fkDialect{}), mock
}

var errStatement = errors.New("statement failed")

// 整批在一个事务里：一条失败即停止并回滚；外键检查在事务之外关闭，回滚之后恢复。
func TestExecuteBatchRollsBackAtomicBatchAndRestoresForeignKeyChecks(t *testing.T) {
	session, mock := newFKMockSession(t)
	mock.ExpectExec("FK OFF appdb.users").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM users;").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO users VALUES (1);").WillReturnError(errStatement)
	mock.ExpectRollback()
	mock.ExpectExec("FK ON appdb.users").WillReturnResult(sqlmock.NewResult(0, 0))

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements:              []string{"DELETE FROM users;", "INSERT INTO users VALUES (1);", "INSERT INTO users VALUES (2);"},
		Atomic:                  true,
		ContinueOnError:         true,
		DisableForeignKeyChecks: true,
		Database:                "appdb",
		Table:                   "users",
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.BatchResult{
		Executed: []dbdriver.BatchStatement{
			{Index: 0, AffectedRows: 3},
			{Index: 1, Error: errStatement.Error()},
		},
		RolledBack: true,
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 不在事务里、出错继续：失败的那条记下来，后面的照常执行，最后恢复外键检查。
func TestExecuteBatchContinuesNonAtomicBatchAndRestoresForeignKeyChecks(t *testing.T) {
	session, mock := newFKMockSession(t)
	mock.ExpectExec("FK OFF appdb.users").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO users VALUES ('bad');").WillReturnError(errStatement)
	mock.ExpectExec("INSERT INTO users VALUES (2);").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("FK ON appdb.users").WillReturnResult(sqlmock.NewResult(0, 0))

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements:              []string{"INSERT INTO users VALUES ('bad');", "INSERT INTO users VALUES (2);"},
		ContinueOnError:         true,
		DisableForeignKeyChecks: true,
		Database:                "appdb",
		Table:                   "users",
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{
		{Index: 0, Error: errStatement.Error()},
		{Index: 1, AffectedRows: 1},
	}}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 不在事务里、出错不继续：停在失败的那条。
func TestExecuteBatchStopsNonAtomicBatchAtTheFirstFailure(t *testing.T) {
	session, mock := newFKMockSession(t)
	mock.ExpectExec("INSERT INTO users VALUES ('bad');").WillReturnError(errStatement)

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO users VALUES ('bad');", "INSERT INTO users VALUES (2);"},
	})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, Error: errStatement.Error()}}, result.Executed)
	assert.False(t, result.RolledBack)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 全部成功的整批提交；空白语句跳过，下标仍指向请求里的位置。不要求关闭外键检查时不碰它。
func TestExecuteBatchCommitsAtomicBatchAndSkipsBlankStatements(t *testing.T) {
	session, mock := newFKMockSession(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users VALUES (1);").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: []string{"  ", "  INSERT INTO users VALUES (1);\n"},
		Atomic:     true,
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{{Index: 1, AffectedRows: 1}}}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 开始事务失败是批本身的错误；已经关闭的外键检查照样恢复。
func TestExecuteBatchRestoresForeignKeyChecksWhenTheTransactionCannotStart(t *testing.T) {
	session, mock := newFKMockSession(t)
	mock.ExpectExec("FK OFF appdb.users").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectBegin().WillReturnError(errors.New("no transactions today"))
	mock.ExpectExec("FK ON appdb.users").WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO users VALUES (1);"}, Atomic: true,
		DisableForeignKeyChecks: true, Database: "appdb", Table: "users",
	})
	require.ErrorContains(t, err, "no transactions today")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 提交失败是批本身的错误，同时给出已经执行过的语句。
func TestExecuteBatchReportsCommitFailureWithTheExecutedStatements(t *testing.T) {
	session, mock := newFKMockSession(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users VALUES (1);").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit refused"))

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO users VALUES (1);"}, Atomic: true,
	})
	require.ErrorContains(t, err, "commit refused")
	require.NotNil(t, result)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, AffectedRows: 1}}, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 恢复外键检查失败不能被吞掉：没有语句失败时它就是批的错误。
func TestExecuteBatchReportsForeignKeyRestoreFailure(t *testing.T) {
	session, mock := newFKMockSession(t)
	mock.ExpectExec("FK OFF appdb.users").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO users VALUES (1);").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("FK ON appdb.users").WillReturnError(errors.New("constraint check failed"))

	result, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO users VALUES (1);"}, ContinueOnError: true,
		DisableForeignKeyChecks: true, Database: "appdb", Table: "users",
	})
	require.ErrorContains(t, err, "restore foreign key checks")
	require.ErrorContains(t, err, "constraint check failed")
	require.NotNil(t, result)
	assert.Equal(t, []dbdriver.BatchStatement{{Index: 0, AffectedRows: 1}}, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ctx 在执行中途被取消（超时）：批停下并报错，不再逐条撞上已取消的 ctx；外键检查仍然恢复，
// 连接不会带着关掉的检查回到连接池。
func TestExecuteBatchStopsOnCancellationAndStillRestoresForeignKeyChecks(t *testing.T) {
	session, mock := newFKMockSession(t)
	mock.ExpectExec("FK OFF appdb.users").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO users VALUES (1);").WillDelayFor(time.Second).WillReturnResult(sqlmock.NewResult(0, 1))
	// 带延迟的期望在 ctx 已取消时必然以取消失败：恢复要成功，就不能用批的 ctx。
	mock.ExpectExec("FK ON appdb.users").WillDelayFor(10 * time.Millisecond).WillReturnResult(sqlmock.NewResult(0, 0))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := session.ExecuteBatch(ctx, dbdriver.BatchRequest{
		Statements:      []string{"INSERT INTO users VALUES (1);", "INSERT INTO users VALUES (2);"},
		ContinueOnError: true, DisableForeignKeyChecks: true, Database: "appdb", Table: "users",
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotContains(t, err.Error(), "restore foreign key checks")
	require.NotNil(t, result)
	require.Len(t, result.Executed, 1)
	assert.NotEmpty(t, result.Executed[0].Error)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 方言给不出关闭外键检查的语句时以 ErrUnsupported 拒绝，不执行任何语句。
func TestExecuteBatchRefusesForeignKeyChecksTheDialectCannotDisable(t *testing.T) {
	session, mock := newMockSession(t)

	_, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO users VALUES (1);"}, DisableForeignKeyChecks: true, Table: "users",
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 关闭外键检查要知道目标表（SQL Server 按表停用约束）：没给表名是调用方的错，不执行任何语句。
func TestExecuteBatchRequiresTheTargetTableToDisableForeignKeyChecks(t *testing.T) {
	session, mock := newFKMockSession(t)

	_, err := session.ExecuteBatch(context.Background(), dbdriver.BatchRequest{
		Statements: []string{"INSERT INTO users VALUES (1);"}, DisableForeignKeyChecks: true,
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}
