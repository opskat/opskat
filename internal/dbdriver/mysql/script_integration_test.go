//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// 需要一个可随意建库的 MySQL：E2E_MYSQL_HOST / PORT / USER / PASSWORD。
func openScriptDatabase(t *testing.T, database string) *sql.DB {
	t.Helper()
	host := os.Getenv("E2E_MYSQL_HOST")
	if host == "" {
		t.Skip("E2E_MYSQL_HOST 未设置，跳过集成测试")
	}
	port, err := strconv.Atoi(os.Getenv("E2E_MYSQL_PORT"))
	require.NoError(t, err)
	cfg := &dbdriver.Config{Host: host, Port: port, Username: os.Getenv("E2E_MYSQL_USER"), Database: database}
	db, err := sql.Open("mysql", dsn(cfg, os.Getenv("E2E_MYSQL_PASSWORD"), "tcp"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// 在真实的 MySQL 上把 mysqldump 与 Navicat 式的样例文件各运行到一个空库：每条语句都成功，表、数据、
// 视图、过程与触发器都建出来并按文件的写法工作。
func TestRunScriptRunsDumpFilesOnARealDatabase(t *testing.T) {
	admin := openScriptDatabase(t, "")
	ctx := context.Background()
	for _, tt := range []struct {
		sample, table, call string
		rows                int
	}{
		{sample: "mysqldump", table: "orders", call: "CALL order_total(1, @total)", rows: 3},
		{sample: "navicat", table: "customers", call: "CALL add_customer('')", rows: 2},
	} {
		t.Run(tt.sample, func(t *testing.T) {
			name := fmt.Sprintf("opskat_script_%s_%d", tt.sample, time.Now().UnixNano())
			_, err := admin.Exec("CREATE DATABASE " + quoteIdent(name))
			require.NoError(t, err)
			t.Cleanup(func() {
				_, err := admin.Exec("DROP DATABASE " + quoteIdent(name))
				assert.NoError(t, err)
			})
			db := openScriptDatabase(t, name)
			script, want := readSample(t, tt.sample)

			result, err := newSession(db).RunScript(ctx, dbdriver.ScriptRequest{
				Database: name, Script: strings.NewReader(script), Size: int64(len(script)), ReadSize: 64,
			})
			require.NoError(t, err)
			require.Nil(t, result.Failure)
			assert.EqualValues(t, len(want), result.Succeeded)

			var count int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+quoteIdent(tt.table)).Scan(&count))
			assert.Equal(t, tt.rows, count)
			_, err = db.Exec(tt.call)
			require.NoError(t, err)
		})
	}
}

// mysqldump 样例的数据里反斜杠转义的内容原样写入，触发器体（DELIMITER ;; 包着）把空的 note 改写。
func TestRunScriptKeepsEscapesAndTriggerBodiesOnARealDatabase(t *testing.T) {
	admin := openScriptDatabase(t, "")
	name := fmt.Sprintf("opskat_script_escape_%d", time.Now().UnixNano())
	_, err := admin.Exec("CREATE DATABASE " + quoteIdent(name))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP DATABASE " + quoteIdent(name))
		assert.NoError(t, err)
	})
	db := openScriptDatabase(t, name)
	script, _ := readSample(t, "mysqldump")
	result, err := newSession(db).RunScript(context.Background(), dbdriver.ScriptRequest{
		Database: name, Script: strings.NewReader(script), Size: int64(len(script)),
	})
	require.NoError(t, err)
	require.Nil(t, result.Failure)

	var notes []string
	rows, err := db.Query("SELECT note FROM orders ORDER BY id")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var note string
		require.NoError(t, rows.Scan(&note))
		notes = append(notes, note)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"it's; fine", `back\slash; "quoted"`, "line\nbreak -- not a comment /* nor this */ # nor this"}, notes)

	_, err = db.Exec("INSERT INTO orders (note, total) VALUES ('', 1)")
	require.NoError(t, err)
	var note string
	require.NoError(t, db.QueryRow("SELECT note FROM orders WHERE id = LAST_INSERT_ID()").Scan(&note))
	assert.Equal(t, "empty;", note)
}
