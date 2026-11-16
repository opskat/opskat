package postgresql

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// runScript 经会话运行 SQL 文件（每次读 readSize 字节；continueOnError 即「遇到错误时继续」），返回
// 依次交给数据库执行的语句。桩连接接受 want 条语句，多出来的执行失败，计进 Failed。
func runScript(t *testing.T, script string, readSize, want int, continueOnError bool) ([]string, *dbdriver.ScriptResult, error) {
	t.Helper()
	var executed []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, actual string) error {
		executed = append(executed, actual)
		return nil
	})))
	require.NoError(t, err)
	for range want {
		mock.ExpectExec("").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectClose()
	session := newSession(db)
	defer func() { require.NoError(t, session.Close()) }()
	result, err := session.RunScript(context.Background(), dbdriver.ScriptRequest{
		Script: strings.NewReader(script), Size: int64(len(script)), ContinueOnError: continueOnError, ReadSize: readSize,
	})
	return executed, result, err
}

// readSample 读 testdata 里的样例文件与它应当切出的语句（.statements，语句之间是单独一行的 @@@@）。
func readSample(t *testing.T, name string) (script string, statements []string) {
	t.Helper()
	testdata := os.DirFS("testdata")
	sql, err := fs.ReadFile(testdata, name+".sql")
	require.NoError(t, err)
	want, err := fs.ReadFile(testdata, name+".statements")
	require.NoError(t, err)
	return string(sql), strings.Split(strings.TrimSuffix(string(want), "\n"), "\n@@@@\n")
}

// pg_dump（--inserts）的文件逐条切对：$$ 与 $tag$ 块（互相嵌套）里的分号、BEGIN ATOMIC ... END 函数体
// （体里有 CASE ... END）、E'...' 字符串里反斜杠转义的引号、嵌套的多行注释都不结束语句；文件按多大的块
// 读取都一样。
func TestRunScriptSplitsAPgDumpFileStatementForStatement(t *testing.T) {
	script, want := readSample(t, "pg_dump_inserts")
	for readSize := 1; readSize <= len(script); readSize++ {
		executed, result, err := runScript(t, script, readSize, len(want), true)
		require.NoError(t, err, "read size %d", readSize)
		require.Equal(t, want, executed, "read size %d", readSize)
		require.Zero(t, result.Failed, "read size %d", readSize)
	}
}

// pg_dump 默认的 COPY ... FROM stdin 只有 psql 能执行：它之前的语句执行完，运行停在 COPY 那一行，
// 勾了「继续」也停下，并建议改用 psql；之后的语句不执行。
func TestRunScriptStopsAtCopyFromStdin(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("testdata", "pg_dump_copy.sql"))
	require.NoError(t, err)
	before := []string{
		"SET client_encoding = 'UTF8'",
		"SET standard_conforming_strings = on",
		"CREATE TABLE public.notes (\n    id integer NOT NULL,\n    body text\n)",
	}
	for readSize := 1; readSize <= len(script); readSize++ {
		executed, result, err := runScript(t, string(script), readSize, len(before), true)
		var unsupported *dbdriver.ScriptUnsupportedError
		require.ErrorAs(t, err, &unsupported, "read size %d", readSize)
		require.Equal(t, dbdriver.ScriptUnsupportedError{Construct: "COPY ... FROM stdin", Tool: "psql", Line: 17}, *unsupported, "read size %d", readSize)
		require.Equal(t, before, executed, "read size %d", readSize)
		require.EqualValues(t, len(before), result.Succeeded, "read size %d", readSize)
	}
}

// psql 的反斜杠元命令（不论在语句开头还是中间）同样停下，报出是哪个命令、在哪一行。
func TestRunScriptStopsAtAPsqlMetaCommand(t *testing.T) {
	for name, tt := range map[string]struct {
		script    string
		before    []string
		construct string
		line      int
	}{
		"connect": {
			script: "SELECT 1;\n-- switch database\n\\connect other\nSELECT 2;\n",
			before: []string{"SELECT 1"}, construct: `\connect`, line: 3,
		},
		"set": {
			script: "SELECT 'a\\b';\n\\set ON_ERROR_STOP on\n",
			before: []string{"SELECT 'a\\b'"}, construct: `\set`, line: 2,
		},
		"inside a statement": {
			script: "SELECT 1;\nSELECT 2\n\\gexec\n",
			before: []string{"SELECT 1"}, construct: `\gexec`, line: 3,
		},
		"copy from stdin in lower case after a column list": {
			script: "select 1;\ncopy t(a, b) from STDIN with (format csv);\n1,2\n\\.\n",
			before: []string{"select 1"}, construct: "COPY ... FROM stdin", line: 2,
		},
	} {
		executed, _, err := runScript(t, tt.script, 0, len(tt.before), true)
		var unsupported *dbdriver.ScriptUnsupportedError
		require.ErrorAs(t, err, &unsupported, name)
		assert.Equal(t, dbdriver.ScriptUnsupportedError{Construct: tt.construct, Tool: "psql", Line: tt.line}, *unsupported, name)
		assert.Equal(t, tt.before, executed, name)
	}
}

// COPY 到文件或从文件读（服务器端）、标识符与字符串里的 stdin 都不是 psql 才有的写法。
func TestRunScriptRunsCopyThatIsNotFromStdin(t *testing.T) {
	want := []string{
		"COPY t FROM '/tmp/t.csv'",
		"COPY t TO STDOUT",
		"SELECT 'from stdin', \"stdin\" FROM stdin",
	}
	executed, result, err := runScript(t, strings.Join(want, ";\n")+";\n", 0, len(want), false)
	require.NoError(t, err)
	assert.Equal(t, want, executed)
	assert.Nil(t, result.Failure)
}
