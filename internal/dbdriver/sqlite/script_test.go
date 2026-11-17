package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 在真实的库文件上运行一个 SQL 文件：引号与注释里的分号、触发器体（体里有 CASE ... END）都切对；
// 前面建的临时表（只在这条连接上可见）后面的语句能用，说明整个文件在同一条连接上执行；文件按
// 很小的块读取也一样。
func TestRunScriptOnARealDatabaseKeepsSessionStateAndTriggerBodies(t *testing.T) {
	script := "\ufeff-- dump header; not a statement\n" +
		"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT, kind TEXT);\n" +
		"CREATE TABLE audit (note_id INTEGER, kind TEXT);\n" +
		"CREATE TRIGGER notes_ai AFTER INSERT ON notes\nBEGIN\n" +
		"  INSERT INTO audit VALUES (NEW.id, CASE WHEN NEW.kind = 'a' THEN 'alpha;' ELSE 'other' END);\nEND;\n" +
		"CREATE TEMP TABLE staging (body TEXT, kind TEXT);\n" +
		"INSERT INTO staging VALUES ('it''s; quoted', 'a'), ('/* not a comment */', 'b');\n" +
		"/* copy; from the temp table */\n" +
		"INSERT INTO notes (body, kind) SELECT body, kind FROM staging;\n"
	for _, readSize := range []int{0, 5} {
		session := openLocalSession(t, &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "script.db"),
		})
		ctx := context.Background()

		result, err := session.RunScript(ctx, dbdriver.ScriptRequest{
			Database: "main", Script: strings.NewReader(script), Size: int64(len(script)), ReadSize: readSize,
		})
		require.NoError(t, err, "read size %d", readSize)
		assert.Nil(t, result.Failure, "read size %d", readSize)
		assert.EqualValues(t, 6, result.Succeeded, "read size %d", readSize)

		notes, err := session.Execute(ctx, "SELECT body FROM notes ORDER BY id")
		require.NoError(t, err)
		assert.Equal(t, [][]any{{"it's; quoted"}, {"/* not a comment */"}}, notes.Rows.Rows)
		audit, err := session.Execute(ctx, "SELECT kind FROM audit ORDER BY note_id")
		require.NoError(t, err)
		assert.Equal(t, [][]any{{"alpha;"}, {"other"}}, audit.Rows.Rows)
	}
}

// 文件在连接上留下的状态（这里是没提交的事务与临时表；真实的文件里常见关闭外键检查、SET 会话变量）
// 不跟着连接回到会话的连接池：运行停在中途时，查询面板之后的语句不会落进那个没提交的事务、看到那张
// 临时表。
func TestRunScriptDoesNotLeaveItsConnectionStateInTheSessionPool(t *testing.T) {
	session := openLocalSession(t, &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "state.db"),
	})
	ctx := context.Background()
	_, err := session.Execute(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	script := "CREATE TEMP TABLE staging (x INTEGER);\nBEGIN;\nINSERT INTO t VALUES (1);\nINSERT INTO missing VALUES (1);\nCOMMIT;\n"

	result, err := session.RunScript(ctx, dbdriver.ScriptRequest{Script: strings.NewReader(script), Size: int64(len(script))})
	require.NoError(t, err)
	require.NotNil(t, result.Failure)

	_, err = session.Execute(ctx, "SELECT x FROM temp.staging")
	require.Error(t, err, "the script's temp table is visible to the panel's next statement")
	_, err = session.Execute(ctx, "COMMIT")
	require.Error(t, err, "the panel's next statement ran inside the script's open transaction")
	rows, err := session.Execute(ctx, "SELECT id FROM t")
	require.NoError(t, err)
	assert.Empty(t, rows.Rows.Rows)
}

// 第一条失败的语句处停下：报出第几条、文件第几行、SQLite 的错误；之后的语句没有执行。
func TestRunScriptOnARealDatabaseStopsAtTheFirstFailure(t *testing.T) {
	session := openLocalSession(t, &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "fail.db"),
	})
	ctx := context.Background()
	script := "CREATE TABLE t (id INTEGER PRIMARY KEY);\nINSERT INTO t VALUES (1);\n\nINSERT INTO t VALUES (1);\nINSERT INTO t VALUES (2);\n"

	result, err := session.RunScript(ctx, dbdriver.ScriptRequest{Script: strings.NewReader(script), Size: int64(len(script))})
	require.NoError(t, err)
	assert.EqualValues(t, 2, result.Succeeded)
	require.NotNil(t, result.Failure)
	assert.EqualValues(t, 3, result.Failure.Number)
	assert.Equal(t, 4, result.Failure.Line)
	assert.Contains(t, result.Failure.Error, "UNIQUE")
	assert.Equal(t, "INSERT INTO t VALUES (1)", result.Failure.Excerpt)

	rows, err := session.Execute(ctx, "SELECT id FROM t")
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(1)}}, rows.Rows.Rows)
}
