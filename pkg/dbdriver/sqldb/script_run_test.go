package sqldb

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// scriptDialect 是能运行 SQL 文件的方言。
type scriptDialect struct{ LimitOffset }

func (scriptDialect) ScriptSyntax() ScriptSyntax { return triggerSyntax }

func scriptOf(text string) dbdriver.ScriptRequest {
	return dbdriver.ScriptRequest{Script: strings.NewReader(text), Size: int64(len(text))}
}

// 方言没有给出脚本语法：以 ErrUnsupported 拒绝，不拿连接、不执行任何语句。
func TestRunScriptIsRefusedWhenTheDialectHasNoScriptSyntax(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = NewSession(db, LimitOffset{}).RunScript(context.Background(), scriptOf("DELETE FROM t;"))
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 语句按文件的次序逐条执行（不包事务），停在第一条失败的语句；之后的不再执行。
func TestRunScriptExecutesStatementsInOrderAndStopsAtTheFirstFailure(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectExec("CREATE TABLE t (x INT)").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO t VALUES ('a;b')").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO t VALUES (oops)").WillReturnError(errors.New("no such column: oops"))

	result, err := NewSession(db, scriptDialect{}).RunScript(context.Background(),
		scriptOf("CREATE TABLE t (x INT);\nINSERT INTO t VALUES ('a;b');\n\nINSERT INTO t VALUES (oops);\nDROP TABLE t;\n"))
	require.NoError(t, err)
	assert.EqualValues(t, 2, result.Succeeded)
	assert.EqualValues(t, 1, result.Failed)
	require.NotNil(t, result.Failure)
	assert.Equal(t, dbdriver.ScriptFailure{Number: 3, Line: 4, Error: "no such column: oops", Excerpt: "INSERT INTO t VALUES (oops)"}, *result.Failure)
	require.NoError(t, mock.ExpectationsWereMet())
}
