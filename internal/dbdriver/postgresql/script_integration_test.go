//go:build integration

package postgresql

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

// 在真实的 PostgreSQL 上把 pg_dump（--inserts）式的样例运行到一个空库：每条语句都成功，$$ / $tag$ 函数、
// BEGIN ATOMIC 函数、DO 块、E'...' 字符串与触发器都按文件的写法工作。
func TestRunScriptRunsAPgDumpFileOnARealDatabase(t *testing.T) {
	admin := openDDLDatabase(t, os.Getenv("E2E_POSTGRES_DATABASE"))
	name := fmt.Sprintf("opskat_script_%d", time.Now().UnixNano())
	createScratchDatabase(t, admin, name)
	db := openDDLDatabase(t, name)
	script, want := readSample(t, "pg_dump_inserts")

	result, err := newSession(db).RunScript(context.Background(), dbdriver.ScriptRequest{
		Database: name, Script: strings.NewReader(script), Size: int64(len(script)), ReadSize: 64,
	})
	require.NoError(t, err)
	require.Nil(t, result.Failure)
	assert.EqualValues(t, len(want), result.Succeeded)

	var bodies []string
	rows, err := db.Query("SELECT body FROM public.notes ORDER BY id")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var body string
		require.NoError(t, rows.Scan(&body))
		bodies = append(bodies, body)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"it's; fine -- not a comment", `back\slash 'quoted'; done`}, bodies)

	var doubled int
	require.NoError(t, db.QueryRow("SELECT public.double_it(3)").Scan(&doubled))
	assert.Equal(t, 6, doubled)
	var id int
	require.NoError(t, db.QueryRow("SELECT public.add_note('new')").Scan(&id))
	var body string
	require.NoError(t, db.QueryRow("SELECT body FROM public.notes WHERE id = $1", id).Scan(&body))
	assert.Equal(t, "new; touched", body)
}

// pg_dump 默认的 COPY ... FROM stdin：之前的语句在真实的库上执行了（表建出来了），运行停在 COPY 那一行。
func TestRunScriptStopsAtCopyFromStdinOnARealDatabase(t *testing.T) {
	admin := openDDLDatabase(t, os.Getenv("E2E_POSTGRES_DATABASE"))
	name := fmt.Sprintf("opskat_script_copy_%d", time.Now().UnixNano())
	createScratchDatabase(t, admin, name)
	db := openDDLDatabase(t, name)
	script, err := os.ReadFile(filepath.Join("testdata", "pg_dump_copy.sql"))
	require.NoError(t, err)

	result, err := newSession(db).RunScript(context.Background(), dbdriver.ScriptRequest{
		Database: name, Script: strings.NewReader(string(script)), Size: int64(len(script)), ContinueOnError: true,
	})
	var unsupported *dbdriver.ScriptUnsupportedError
	require.ErrorAs(t, err, &unsupported)
	assert.Equal(t, 17, unsupported.Line)
	assert.EqualValues(t, 3, result.Succeeded)
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM public.notes").Scan(&count))
	assert.Zero(t, count)
}
