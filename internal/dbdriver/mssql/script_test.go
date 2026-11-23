package mssql

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// scriptStatements 经会话运行 SQL 文件（每次读 readSize 字节；勾了继续），返回依次交给数据库执行的
// 语句（SQL Server 是一个个批）。桩连接接受 want 条，多出来的执行失败，计进 Failed。
func scriptStatements(t *testing.T, script string, readSize, want int) ([]string, *dbdriver.ScriptResult) {
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
		Script: strings.NewReader(script), Size: int64(len(script)), ContinueOnError: true, ReadSize: readSize,
	})
	require.NoError(t, err, "read size %d", readSize)
	return executed, result
}

// SSMS「生成脚本」式的文件按 GO 切成批，每批作为一次执行交给数据库：批里的分号不切分；字符串、方括号
// 标识符与（嵌套的）注释里的 GO、GOTO 这样的词都不是分隔符；GO 后面可以跟注释，GO 2 把批执行两次；
// 文件按多大的块读取都一样。
func TestRunScriptSplitsAGeneratedScriptIntoGoBatches(t *testing.T) {
	sql, err := os.ReadFile(filepath.Join("testdata", "ssms_script.sql"))
	require.NoError(t, err)
	statements, err := os.ReadFile(filepath.Join("testdata", "ssms_script.statements"))
	require.NoError(t, err)
	script, want := string(sql), strings.Split(strings.TrimSuffix(string(statements), "\n"), "\n@@@@\n")
	for readSize := 1; readSize <= len(script); readSize++ {
		executed, result := scriptStatements(t, script, readSize, len(want))
		require.Equal(t, want, executed, "read size %d", readSize)
		require.Zero(t, result.Failed, "read size %d", readSize)
	}
}
