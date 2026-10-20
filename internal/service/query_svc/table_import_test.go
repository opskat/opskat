package query_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// batchSession 是只会整批执行的会话：记下收到的请求，返回给定的结果。
type batchSession struct {
	dbdriver.Session
	request dbdriver.BatchRequest
	result  *dbdriver.BatchResult
	err     error
}

func (s *batchSession) ExecuteBatch(_ context.Context, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	s.request = req
	return s.result, s.err
}

// 复制模式与不继续的导入放进一个事务；外键开关与目标表原样交给驱动。
func TestRunTableImportBatchAsksTheDriverForOneTransactionWhenNeeded(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mode            string
		continueOnError bool
		atomic          bool
	}{
		{"copy", "copy", true, true},
		{"stop on error", "append", false, true},
		{"continue on error", "append", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := &batchSession{result: &dbdriver.BatchResult{}}
			_, err := RunTableImportBatch(context.Background(), session, "appdb", TableImportBatchRequest{
				Statements: []string{"INSERT INTO t VALUES (1);"}, Mode: tc.mode, ContinueOnError: tc.continueOnError,
				DisableForeignKeyChecks: true, Table: "dbo.users",
			})
			require.NoError(t, err)
			assert.Equal(t, dbdriver.BatchRequest{
				Statements: []string{"INSERT INTO t VALUES (1);"}, Atomic: tc.atomic, ContinueOnError: tc.continueOnError,
				DisableForeignKeyChecks: true, Database: "appdb", Table: "dbo.users",
			}, session.request)
		})
	}
}

// 新增 / 更新 / 删除按语句的首个关键字计数，认不出的按导入模式归类；失败的语句带着语句原文。
func TestRunTableImportBatchCountsRowsByStatementKind(t *testing.T) {
	statements := []string{
		"DELETE FROM t;", "UPDATE t SET a = 1;", "INSERT INTO t VALUES (1);", "REPLACE INTO t VALUES (2);",
		"MERGE INTO t USING s ON 1 = 1;", "INSERT INTO t VALUES ('bad');",
	}
	session := &batchSession{result: &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{
		{Index: 0, AffectedRows: 4}, {Index: 1, AffectedRows: 3}, {Index: 2, AffectedRows: 1},
		{Index: 3, AffectedRows: 2}, {Index: 4, AffectedRows: 5}, {Index: 5, Error: "bad value"},
	}}}

	result, err := RunTableImportBatch(context.Background(), session, "appdb", TableImportBatchRequest{
		Statements: statements, Mode: "update", ContinueOnError: true,
	})
	require.NoError(t, err)
	assert.Equal(t, &TableImportBatchResult{
		Processed: 6, Added: 3, Updated: 8, Deleted: 4, Error: 1,
		Errors: []TableImportBatchError{{Index: 5, Statement: "INSERT INTO t VALUES ('bad');", Message: "bad value"}},
	}, result)
}

// 回滚了的整批不报告留下的行。
func TestRunTableImportBatchReportsNoRowsForARolledBackBatch(t *testing.T) {
	session := &batchSession{result: &dbdriver.BatchResult{
		Executed:   []dbdriver.BatchStatement{{Index: 0, AffectedRows: 3}, {Index: 1, Error: "duplicate"}},
		RolledBack: true,
	}}

	result, err := RunTableImportBatch(context.Background(), session, "appdb", TableImportBatchRequest{
		Statements: []string{"DELETE FROM t;", "INSERT INTO t VALUES (1);"}, Mode: "copy", ContinueOnError: true,
	})
	require.NoError(t, err)
	assert.Equal(t, &TableImportBatchResult{
		Processed: 2, Error: 1, RolledBack: true,
		Errors: []TableImportBatchError{{Index: 1, Statement: "INSERT INTO t VALUES (1);", Message: "duplicate"}},
	}, result)
}

// 在一个事务里的整批有语句失败、回滚本身又出错：事务没有提交，执行成功的语句也没有留下，不能计成
// 新增；回滚的错误与失败的语句一起报告。
func TestRunTableImportBatchReportsNoRowsWhenAFailedTransactionCannotRollBack(t *testing.T) {
	rollbackErr := errors.New("rollback transaction: connection reset")
	session := &batchSession{
		result: &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{{Index: 0, AffectedRows: 3}, {Index: 1, Error: "duplicate"}}},
		err:    rollbackErr,
	}

	result, err := RunTableImportBatch(context.Background(), session, "appdb", TableImportBatchRequest{
		Statements: []string{"INSERT INTO t VALUES (1), (2), (3);", "INSERT INTO t VALUES (1);"}, Mode: "append",
	})
	require.NoError(t, err)
	assert.Equal(t, &TableImportBatchResult{
		Processed: 2, Error: 2,
		Errors: []TableImportBatchError{
			{Index: 1, Statement: "INSERT INTO t VALUES (1);", Message: "duplicate"},
			{Index: -1, Message: rollbackErr.Error()},
		},
	}, result)
}

// 批本身出错：已经有语句失败时，它与那些失败一起出现在错误列表里（不对应语句）；没有语句失败
// 时它就是导入的错误。
func TestRunTableImportBatchReportsBatchFailures(t *testing.T) {
	restoreErr := errors.New("restore foreign key checks: denied")

	session := &batchSession{
		result: &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{{Index: 0, Error: "bad value"}}},
		err:    restoreErr,
	}
	result, err := RunTableImportBatch(context.Background(), session, "appdb", TableImportBatchRequest{
		Statements: []string{"INSERT INTO t VALUES ('bad');"}, Mode: "append", ContinueOnError: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Error)
	assert.Equal(t, []TableImportBatchError{
		{Index: 0, Statement: "INSERT INTO t VALUES ('bad');", Message: "bad value"},
		{Index: -1, Message: restoreErr.Error()},
	}, result.Errors)

	session = &batchSession{result: &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{{Index: 0, AffectedRows: 1}}}, err: restoreErr}
	_, err = RunTableImportBatch(context.Background(), session, "appdb", TableImportBatchRequest{
		Statements: []string{"INSERT INTO t VALUES (1);"}, Mode: "append", ContinueOnError: true,
	})
	require.ErrorIs(t, err, restoreErr)

	session = &batchSession{err: restoreErr}
	_, err = RunTableImportBatch(context.Background(), session, "appdb", TableImportBatchRequest{Mode: "append"})
	require.ErrorIs(t, err, restoreErr)
}
