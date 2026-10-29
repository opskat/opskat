package query_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

var schemaStatements = []string{
	"ALTER TABLE users ADD age INT",
	"CREATE INDEX ix_age ON users (age)",
	"ALTER TABLE users DROP COLUMN nickname",
}

// 声明了表结构语句可回滚的驱动：整批在一个事务里执行，失败的那条带着数据库错误，之后的没有执行，
// 之前执行过的随整批回滚。
func TestRunSchemaBatchRunsAtomicallyWhenTheDriverDeclaresTransactionalDDL(t *testing.T) {
	session := &batchSession{result: &dbdriver.BatchResult{
		Executed:   []dbdriver.BatchStatement{{Index: 0}, {Index: 1, Error: "duplicate index name"}},
		RolledBack: true,
	}}

	result, err := RunSchemaBatch(context.Background(), session, dbdriver.Descriptor{ID: "extdb", TransactionalDDL: true}, "appdb", schemaStatements)
	require.NoError(t, err)
	assert.Equal(t, dbdriver.BatchRequest{Statements: schemaStatements, Atomic: true, Database: "appdb"}, session.request)
	assert.Equal(t, &SchemaBatchResult{
		Atomic:     true,
		RolledBack: true,
		Statements: []SchemaStatementResult{
			{Statement: schemaStatements[0], Status: SchemaStatementExecuted},
			{Statement: schemaStatements[1], Status: SchemaStatementFailed, Error: "duplicate index name"},
			{Statement: schemaStatements[2], Status: SchemaStatementNotExecuted},
		},
	}, result)
}

// 没有声明的驱动：逐条执行，停在第一条失败的语句；已执行的留下，没有回滚。
func TestRunSchemaBatchStopsAtTheFirstFailureOtherwise(t *testing.T) {
	session := &batchSession{result: &dbdriver.BatchResult{
		Executed: []dbdriver.BatchStatement{{Index: 0}, {Index: 1, Error: "duplicate index name"}},
	}}

	result, err := RunSchemaBatch(context.Background(), session, dbdriver.Descriptor{ID: "extdb"}, "appdb", schemaStatements)
	require.NoError(t, err)
	assert.Equal(t, dbdriver.BatchRequest{Statements: schemaStatements, Atomic: false, ContinueOnError: false, Database: "appdb"}, session.request)
	assert.Equal(t, &SchemaBatchResult{
		Statements: []SchemaStatementResult{
			{Statement: schemaStatements[0], Status: SchemaStatementExecuted},
			{Statement: schemaStatements[1], Status: SchemaStatementFailed, Error: "duplicate index name"},
			{Statement: schemaStatements[2], Status: SchemaStatementNotExecuted},
		},
	}, result)
}

func TestRunSchemaBatchReportsEveryStatementExecuted(t *testing.T) {
	session := &batchSession{result: &dbdriver.BatchResult{
		Executed: []dbdriver.BatchStatement{{Index: 0}, {Index: 1}, {Index: 2}},
	}}

	result, err := RunSchemaBatch(context.Background(), session, dbdriver.Descriptor{ID: "extdb", TransactionalDDL: true}, "appdb", schemaStatements)
	require.NoError(t, err)
	assert.Equal(t, &SchemaBatchResult{
		Atomic: true,
		Statements: []SchemaStatementResult{
			{Statement: schemaStatements[0], Status: SchemaStatementExecuted},
			{Statement: schemaStatements[1], Status: SchemaStatementExecuted},
			{Statement: schemaStatements[2], Status: SchemaStatementExecuted},
		},
	}, result)
}

// 批本身出错：没有语句失败的（拿不到连接、提交失败、ctx 被取消），它就是保存的错误；已经有语句失败
// 的（之后回滚也失败了），它与各条的结果一起报告，此时整批没有回滚。
func TestRunSchemaBatchReportsAFailureOfTheBatchItself(t *testing.T) {
	commitErr := errors.New("commit transaction: connection reset")
	session := &batchSession{result: &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{{Index: 0}}}, err: commitErr}
	_, err := RunSchemaBatch(context.Background(), session, dbdriver.Descriptor{ID: "extdb", TransactionalDDL: true}, "appdb", schemaStatements[:1])
	require.ErrorIs(t, err, commitErr)

	session = &batchSession{err: commitErr}
	_, err = RunSchemaBatch(context.Background(), session, dbdriver.Descriptor{ID: "extdb", TransactionalDDL: true}, "appdb", schemaStatements[:1])
	require.ErrorIs(t, err, commitErr)

	rollbackErr := errors.New("rollback transaction: connection reset")
	session = &batchSession{result: &dbdriver.BatchResult{
		Executed: []dbdriver.BatchStatement{{Index: 0}, {Index: 1, Error: "duplicate index name"}},
	}, err: rollbackErr}
	result, err := RunSchemaBatch(context.Background(), session, dbdriver.Descriptor{ID: "extdb", TransactionalDDL: true}, "appdb", schemaStatements)
	require.NoError(t, err)
	assert.Equal(t, &SchemaBatchResult{
		Atomic: true,
		Statements: []SchemaStatementResult{
			{Statement: schemaStatements[0], Status: SchemaStatementExecuted},
			{Statement: schemaStatements[1], Status: SchemaStatementFailed, Error: "duplicate index name"},
			{Statement: schemaStatements[2], Status: SchemaStatementNotExecuted},
		},
		Error: rollbackErr.Error(),
	}, result)
}

// 空白语句会被整批执行跳过、报成「未执行」而误导用户：在交给驱动之前就拒绝，什么也不执行。
func TestRunSchemaBatchRefusesBlankStatements(t *testing.T) {
	session := &batchSession{result: &dbdriver.BatchResult{}}
	_, err := RunSchemaBatch(context.Background(), session, dbdriver.Descriptor{ID: "extdb"}, "appdb", []string{"ALTER TABLE users ADD age INT", "  "})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1")
	assert.Equal(t, dbdriver.BatchRequest{}, session.request, "没有交给驱动")
}
