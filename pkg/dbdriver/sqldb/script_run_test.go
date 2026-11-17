package sqldb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// 一批语句执行到一半时被要求停下：正在执行的语句执行完，后面的不再执行，也不记在结果里。
func TestRunScriptStopsAfterTheCurrentStatementInsideABatch(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectExec("INSERT INTO t VALUES (1)").WillDelayFor(150 * time.Millisecond).WillReturnResult(sqlmock.NewResult(0, 1))

	stop := make(chan struct{})
	time.AfterFunc(30*time.Millisecond, func() { close(stop) })
	req := scriptOf("INSERT INTO t VALUES (1);\nINSERT INTO t VALUES (2);\nINSERT INTO t VALUES (3);\n")
	req.Stop = stop

	result, err := NewSession(db, scriptDialect{}).RunScript(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.Stopped)
	assert.EqualValues(t, 1, result.Succeeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

// slashSplitter 是自己切分的方言（如 Oracle 以单独一行 / 结束 PL/SQL 块）：一行只有 / 才结束语句，
// 分号不结束。它不给出 ScriptSyntax。
type slashSplitter struct{ LimitOffset }

func (slashSplitter) SplitScript(_ context.Context, chunk dbdriver.ScriptChunk) (*dbdriver.ScriptSplit, error) {
	split := &dbdriver.ScriptSplit{}
	start := 0
	for pos := 0; pos < len(chunk.Text); {
		end := strings.IndexByte(chunk.Text[pos:], '\n')
		if end < 0 {
			break
		}
		if strings.TrimSpace(chunk.Text[pos:pos+end]) == "/" {
			split.Statements = append(split.Statements, dbdriver.ScriptStatement{Text: strings.TrimSpace(chunk.Text[start:pos]), Offset: start})
			start = pos + end + 1
		}
		pos += end + 1
	}
	split.Consumed = start
	if chunk.Final {
		if text := strings.TrimSpace(chunk.Text[start:]); text != "" {
			split.Statements = append(split.Statements, dbdriver.ScriptStatement{Text: text, Offset: start})
		}
		split.Consumed = len(chunk.Text)
	}
	return split, nil
}

// bothDialect 既给出 ScriptSyntax 又自己切分。
type bothDialect struct{ slashSplitter }

func (bothDialect) ScriptSyntax() ScriptSyntax { return triggerSyntax }

// 方言自己实现 dbdriver.ScriptSplitter 时按它切分，不需要 ScriptSyntax；同时给出两者的也按它自己的切分。
func TestRunScriptSplitsWithTheDialectsOwnSplitter(t *testing.T) {
	for name, dialect := range map[string]Dialect{"splitter only": slashSplitter{}, "splitter and syntax": bothDialect{}} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectExec("BEGIN x := 1; y := 2; END;").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("INSERT INTO t VALUES (1)").WillReturnResult(sqlmock.NewResult(0, 1))

			result, err := NewSession(db, dialect).RunScript(context.Background(),
				scriptOf("BEGIN x := 1; y := 2; END;\n/\nINSERT INTO t VALUES (1)\n/\n"))
			require.NoError(t, err)
			assert.EqualValues(t, 2, result.Succeeded)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// ScriptRunner 是 RunScript 用的两个钩子，扩展 SDK 经它在 ABI 上提供运行 SQL 文件：方言既没有
// ScriptSyntax 也不自己切分时以 ErrUnsupported 拒绝；有的话按方言的写法切分。
func TestScriptRunnerIsTheDialectsHooks(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = NewSession(db, LimitOffset{}).ScriptRunner()
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)

	runner, err := NewSession(db, scriptDialect{}).ScriptRunner()
	require.NoError(t, err)
	split, err := runner.SplitScript(context.Background(), dbdriver.ScriptChunk{Text: "SELECT 1; SELECT"})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ScriptStatement{{Text: "SELECT 1", Offset: 0}}, split.Statements)

	runner, err = NewSession(db, slashSplitter{}).ScriptRunner()
	require.NoError(t, err)
	split, err = runner.SplitScript(context.Background(), dbdriver.ScriptChunk{Text: "a; b\n/\n", Final: true})
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ScriptStatement{{Text: "a; b", Offset: 0}}, split.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}
