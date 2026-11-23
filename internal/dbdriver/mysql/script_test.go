package mysql

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// scriptStatements 经会话运行 SQL 文件（每次读 readSize 字节；勾了继续），返回依次交给数据库执行的
// 语句。桩连接接受 want 条语句，多出来的执行失败，计进 Failed。
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

// mysqldump 与 Navicat 转储的文件逐条切对：版本注释 /*!...*/ 是要执行的语句，DELIMITER ;; 包着的过程与
// 触发器体是一条语句，反斜杠转义的引号不结束字符串，# 与 -- 注释、多行注释里的分号不结束语句；文件
// 按多大的块读取都一样。
func TestRunScriptSplitsDumpFilesStatementForStatement(t *testing.T) {
	for _, name := range []string{"mysqldump", "navicat"} {
		t.Run(name, func(t *testing.T) {
			script, want := readSample(t, name)
			for readSize := 1; readSize <= len(script); readSize++ {
				executed, result := scriptStatements(t, script, readSize, len(want))
				require.Equal(t, want, executed, "read size %d", readSize)
				require.Zero(t, result.Failed, "read size %d", readSize)
			}
		})
	}
}
