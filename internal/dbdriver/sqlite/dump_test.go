package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func openFileSession(t *testing.T, name string) dbdriver.Session {
	t.Helper()
	return openLocalSession(t, &dbdriver.Config{Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), name)})
}

func execAll(t *testing.T, session dbdriver.Session, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		_, err := session.Execute(context.Background(), statement)
		require.NoError(t, err, statement)
	}
}

func dumpHeader() dbdriver.DumpHeader {
	return dbdriver.DumpHeader{Version: "1.16.0", Asset: "local-sqlite", Time: time.Date(2026, 11, 16, 9, 30, 5, 0, time.UTC)}
}

// 转储产出：开头关闭外键检查、结尾恢复；表的定义与索引都是 sqlite_master 里的原文；INSERT 与「复制为
// INSERT」一样（不含生成列）；视图只有定义。
func TestDumpWritesSQLiteDefinitionsAndRows(t *testing.T) {
	session := openFileSession(t, "golden.db")
	execAll(t, session,
		"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT, size INT GENERATED ALWAYS AS (length(body)) VIRTUAL)",
		"CREATE INDEX notes_body ON notes (body)",
		"CREATE VIEW short_notes AS SELECT id FROM notes WHERE size < 10",
		"INSERT INTO notes (id, body) VALUES (1, 'it''s'), (2, NULL)",
	)
	var out strings.Builder

	result, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "main", Tables: []string{"short_notes", "notes"}, WithData: true, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)

	inserts, err := dbdrivertest.RowStatements(context.Background(), session, "main", "notes", dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id", "body"},
		Rows: []map[string]any{{"id": float64(1), "body": "it's"}, {"id": float64(2), "body": nil}},
	})
	require.NoError(t, err)
	assert.Equal(t, `-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: local-sqlite
-- Database: main
-- Content: structure and data
-- Date: 2026-11-16 09:30:05 +00:00
--
-- File encoding: UTF-8

PRAGMA foreign_keys = false;

-- ----------------------------
-- Table structure for notes
-- ----------------------------
DROP TABLE IF EXISTS "main"."notes";
CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT, size INT GENERATED ALWAYS AS (length(body)) VIRTUAL);
CREATE INDEX notes_body ON notes (body);

-- ----------------------------
-- Records of notes
-- ----------------------------
`+inserts[0]+"\n"+inserts[1]+`

-- ----------------------------
-- View structure for short_notes
-- ----------------------------
DROP VIEW IF EXISTS "main"."short_notes";
CREATE VIEW short_notes AS SELECT id FROM notes WHERE size < 10;

PRAGMA foreign_keys = true;
`, out.String())
	assert.Equal(t, dbdriver.DumpResult{Objects: 2, Rows: 2, Bytes: int64(out.Len())}, *result)
}

// 往返：选中的表（结构和数据，含一个视图）转储到文件，再经运行 SQL 文件导入一个空库：两边的表、索引、
// 视图定义、自增的当前值与每张表的行（含引号、换行、分号、NULL、二进制、中文）都一样；没选中的表不在
// 文件里。同一个文件再导入一次也成功（每个对象前先删除）。
func TestDumpRoundTripsSelectedTablesThroughRunScript(t *testing.T) {
	ctx := context.Background()
	source := openFileSession(t, "source.db")
	execAll(t, source,
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			score REAL DEFAULT 0 CHECK (score >= 0),
			avatar BLOB,
			name_upper TEXT GENERATED ALWAYS AS (upper(name)) STORED
		)`,
		"CREATE INDEX users_score ON users (score DESC)",
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE, note TEXT)",
		"CREATE INDEX orders_user ON orders (user_id)",
		"CREATE VIEW user_orders AS SELECT u.name, o.note FROM users u JOIN orders o ON o.user_id = u.id",
		"CREATE TABLE unrelated (x INT)",
		"INSERT INTO unrelated VALUES (1)",
		`INSERT INTO users (name, score, avatar) VALUES ('alice', 1.5, X'00FF10'), ('bob''s; --', 0, NULL), ('中文'||char(10)||'line', 2, X'')`,
		"DELETE FROM users WHERE name = 'alice'",
		"INSERT INTO users (name) VALUES ('carol')",
		"INSERT INTO orders (user_id, note) VALUES (2, 'first;'), (3, NULL), (4, '/* not a comment */')",
	)
	path := filepath.Join(t.TempDir(), "dump.sql")
	tables := []string{"orders", "user_orders", "users"}

	result, err := dbdriver.DumpToFile(ctx, source, dbdriver.DumpRequest{
		Database: "main", Tables: tables, WithData: true, Header: dumpHeader(),
	}, path)
	require.NoError(t, err)
	assert.Equal(t, 3, result.Objects)
	assert.EqualValues(t, 6, result.Rows)
	content, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)
	assert.NotContains(t, string(content), "unrelated")

	target := openFileSession(t, "target.db")
	for range 2 {
		file, err := os.Open(filepath.Clean(path))
		require.NoError(t, err)
		run, err := target.RunScript(ctx, dbdriver.ScriptRequest{Database: "main", Script: file, Size: int64(len(content))})
		require.NoError(t, file.Close())
		require.NoError(t, err)
		require.Nil(t, run.Failure, "the dump must run on an empty database")
	}

	schema := "SELECT type, name, tbl_name, sql FROM sqlite_master WHERE tbl_name <> 'unrelated' ORDER BY type, name"
	assert.Equal(t, queryRows(t, source, schema), queryRows(t, target, schema))
	for _, query := range []string{
		"SELECT id, name, score, hex(avatar), typeof(avatar), name_upper FROM users ORDER BY id",
		"SELECT id, user_id, note FROM orders ORDER BY id",
		"SELECT * FROM user_orders ORDER BY name",
		"SELECT name, seq FROM sqlite_sequence",
	} {
		assert.Equal(t, queryRows(t, source, query), queryRows(t, target, query), query)
	}
	assert.Empty(t, queryRows(t, target, "SELECT * FROM pragma_foreign_key_check"))
	assert.Empty(t, queryRows(t, target, "SELECT name FROM sqlite_master WHERE name = 'unrelated'"))
}

func queryRows(t *testing.T, session dbdriver.Session, query string) [][]any {
	t.Helper()
	result, err := session.Execute(context.Background(), query)
	require.NoError(t, err, query)
	return result.Rows.Rows
}
