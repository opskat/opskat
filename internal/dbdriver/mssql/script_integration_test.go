//go:build integration

package mssql

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 在真实的 SQL Server 上把 SSMS 式的样例运行到一个空库：每个 GO 批都成功（CREATE PROCEDURE / VIEW 各占
// 一批才能执行），字符串里跨行的 GO 原样写入，过程按文件的写法工作。
func TestRunScriptRunsAGeneratedScriptOnARealDatabase(t *testing.T) {
	master := openDDLDatabase(t, "master")
	name := fmt.Sprintf("opskat_script_%d", time.Now().UnixNano())
	createScratchDatabase(t, master, name)
	db := openDDLDatabase(t, name)
	script, err := os.ReadFile(filepath.Join("testdata", "ssms_script.sql"))
	require.NoError(t, err)

	result, err := newSession(db).RunScript(context.Background(), dbdriver.ScriptRequest{
		Database: name, Script: strings.NewReader(string(script)), Size: int64(len(script)), ReadSize: 64,
	})
	require.NoError(t, err)
	require.Nil(t, result.Failure)
	// 11 个批：GO 2 前面的 PRINT 执行两次。
	assert.EqualValues(t, 11, result.Succeeded)

	var note string
	require.NoError(t, db.QueryRow("EXEC [dbo].[order_note] @id = 2").Scan(&note, new(int)))
	assert.Equal(t, "multi\nGO\nline", note)
	var x int
	require.NoError(t, db.QueryRow("SELECT [go] FROM [dbo].[GO]").Scan(&x))
	assert.Equal(t, 1, x)
}
