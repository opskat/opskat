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

// allOptions 是全部勾上的导出选项。
var allOptions = dbdriver.DumpOptions{
	Indexes: true, ForeignKeys: true, Views: true, Routines: true, Triggers: true, Drops: true, DisableForeignKeyChecks: true,
}

func dumpHeader() dbdriver.DumpHeader {
	return dbdriver.DumpHeader{Version: "1.16.0", Asset: "local-sqlite", Time: time.Date(2026, 11, 16, 9, 30, 5, 0, time.UTC)}
}

// 转储产出（选项全勾）：开头关闭外键检查、结尾恢复；删除段先删视图再删表（SQLite 能关外键检查，不先删外键）；
// 表的定义与索引都是 sqlite_master 里的原文；INSERT 与「复制为 INSERT」一样（不含生成列）；视图只有定义。
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
		Database: "main", Tables: []string{"short_notes", "notes"}, WithData: true, Options: allOptions, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)

	inserts, err := dbdrivertest.RowStatements(context.Background(), session, "main", "notes", dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id", "body"}, OmitDatabase: true,
		Rows: []map[string]any{{"id": float64(1), "body": "it's"}, {"id": float64(2), "body": nil}},
	})
	require.NoError(t, err)
	assert.Equal(t, `-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: local-sqlite
-- Database: main
-- Content: structure and data
-- Options: indexes, foreign keys, drop statements, disable foreign key checks
-- Date: 2026-11-16 09:30:05 +00:00
--
-- File encoding: UTF-8

PRAGMA foreign_keys = false;

-- ----------------------------
-- Drop existing objects
-- ----------------------------
DROP VIEW IF EXISTS "short_notes";
DROP TABLE IF EXISTS "notes";

-- ----------------------------
-- Table structure for notes
-- ----------------------------
CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT, size INT GENERATED ALWAYS AS (length(body)) VIRTUAL);
CREATE INDEX notes_body ON notes (body);

-- ----------------------------
-- Records of notes
-- ----------------------------
`+inserts[0]+"\n"+inserts[1]+`

-- ----------------------------
-- View structure for short_notes
-- ----------------------------
CREATE VIEW short_notes AS SELECT id FROM notes WHERE size < 10;

PRAGMA foreign_keys = true;
`, out.String())
	assert.Equal(t, dbdriver.DumpResult{Objects: 2, Rows: 2, Bytes: int64(out.Len())}, *result)
}

// 往返：选中的表（结构和数据，含一个视图）转储到文件，再经运行 SQL 文件导入一个空库：两边的表、索引、
// 视图定义、自增的当前值与每张表的行（含引号、换行、分号、NULL、二进制、中文）都一样；没选中的表不在
// 文件里。勾了「写入删除语句」，同一个文件再导入一次也成功（删除段先删掉已有的对象）。
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
		Database: "main", Tables: tables, WithData: true, Options: allOptions, Header: dumpHeader(),
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

// wholeDatabase 建出一个带视图（a_top 依赖 b_base；a_label 只在字符串里提到 b_base）与触发器（每插入一行
// items 就往 audit 记一行）的库，并插入两行。
func wholeDatabase(t *testing.T, session dbdriver.Session) {
	t.Helper()
	execAll(t, session,
		"CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)",
		"CREATE TABLE audit (id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INT)",
		"CREATE TRIGGER items_audit AFTER INSERT ON items BEGIN INSERT INTO audit (item_id) VALUES (new.id); END",
		"CREATE VIEW b_base AS SELECT id, name FROM items WHERE id > 0",
		`CREATE VIEW a_top AS SELECT name FROM "b_base"`,
		"CREATE VIEW a_label AS SELECT 'b_base' AS label /* b_base */ FROM items",
		"INSERT INTO items (name) VALUES ('x'), ('y;')",
	)
}

// 整库转储（不勾「写入删除语句」与「关闭外键检查」）：全部表、视图按依赖顺序（被依赖的 b_base 在 a_top
// 之前；字符串与注释里的名字不算依赖），触发器在所有表与视图之后；定义都是 sqlite_master 里的原文；文件里
// 没有删除语句，也不动外键检查。
func TestDumpWholeDatabaseOrdersViewsAndPutsTriggersLast(t *testing.T) {
	session := openFileSession(t, "whole.db")
	wholeDatabase(t, session)
	var out strings.Builder

	result, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "main", Whole: true, Header: dumpHeader(), Writer: &out,
		Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Views: true, Routines: true, Triggers: true},
	})
	require.NoError(t, err)

	assert.Equal(t, `-- OpsKat SQL Dump
--
-- OpsKat version: 1.16.0
-- Asset: local-sqlite
-- Database: main
-- Content: structure only
-- Options: indexes, foreign keys, views, functions and procedures, triggers
-- Date: 2026-11-16 09:30:05 +00:00
--
-- File encoding: UTF-8

-- ----------------------------
-- Table structure for audit
-- ----------------------------
CREATE TABLE audit (id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INT);

-- ----------------------------
-- Table structure for items
-- ----------------------------
CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL);

-- ----------------------------
-- View structure for a_label
-- ----------------------------
CREATE VIEW a_label AS SELECT 'b_base' AS label /* b_base */ FROM items;

-- ----------------------------
-- View structure for b_base
-- ----------------------------
CREATE VIEW b_base AS SELECT id, name FROM items WHERE id > 0;

-- ----------------------------
-- View structure for a_top
-- ----------------------------
CREATE VIEW a_top AS SELECT name FROM "b_base";

-- ----------------------------
-- Trigger structure for items_audit
-- ----------------------------
CREATE TRIGGER items_audit AFTER INSERT ON items BEGIN INSERT INTO audit (item_id) VALUES (new.id); END;
`, out.String())
	assert.Equal(t, 6, result.Objects)
}

// 整库往返：结构和数据转储到文件，再经运行 SQL 文件导入一个空库：表、视图、触发器的定义与每张表的行数
// 都一样；触发器在所有数据之后才建出，导入时没有被触发（audit 的行数与原库一样，不是翻倍）。导入之后
// 触发器照常工作。
func TestDumpRoundTripsAWholeDatabaseThroughRunScript(t *testing.T) {
	ctx := context.Background()
	source := openFileSession(t, "source.db")
	wholeDatabase(t, source)
	path := filepath.Join(t.TempDir(), "whole.sql")

	result, err := dbdriver.DumpToFile(ctx, source, dbdriver.DumpRequest{
		Database: "main", Whole: true, WithData: true, Options: allOptions, Header: dumpHeader(),
	}, path)
	require.NoError(t, err)
	assert.Equal(t, 6, result.Objects)
	assert.EqualValues(t, 4, result.Rows)
	content, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)

	target := openFileSession(t, "target.db")
	file, err := os.Open(filepath.Clean(path))
	require.NoError(t, err)
	run, err := target.RunScript(ctx, dbdriver.ScriptRequest{Database: "main", Script: file, Size: int64(len(content))})
	require.NoError(t, file.Close())
	require.NoError(t, err)
	require.Nil(t, run.Failure, "the dump must run on an empty database")

	schema := "SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name"
	assert.Equal(t, queryRows(t, source, schema), queryRows(t, target, schema))
	for _, query := range []string{
		"SELECT count(*) FROM items",
		"SELECT count(*) FROM audit",
		"SELECT * FROM a_top ORDER BY name",
	} {
		assert.Equal(t, queryRows(t, source, query), queryRows(t, target, query), query)
	}
	assert.Equal(t, [][]any{{int64(2)}}, queryRows(t, target, "SELECT count(*) FROM audit"))

	execAll(t, target, "INSERT INTO items (name) VALUES ('z')")
	assert.Equal(t, [][]any{{int64(3)}}, queryRows(t, target, "SELECT count(*) FROM audit"))
}

func queryRows(t *testing.T, session dbdriver.Session, query string) [][]any {
	t.Helper()
	result, err := session.Execute(context.Background(), query)
	require.NoError(t, err, query)
	return result.Rows.Rows
}

// 不勾「包含外键」时去掉原文里的外键（表约束连同前面的逗号，列约束连同前面的空白），其余一字不动：字符串、
// 注释与名字里的 REFERENCES 不算，SET NULL / SET DEFAULT / NOT DEFERRABLE 属于外键，NOT NULL 与 DEFAULT 不属于。
func TestWithoutForeignKeys(t *testing.T) {
	for _, tt := range []struct{ name, in, want string }{
		{"column reference", "CREATE TABLE o (id INTEGER PRIMARY KEY, user_id INT NOT NULL REFERENCES users (id) ON DELETE CASCADE, note TEXT)",
			"CREATE TABLE o (id INTEGER PRIMARY KEY, user_id INT NOT NULL, note TEXT)"},
		{"named column reference before other constraints",
			"CREATE TABLE o (a INT CONSTRAINT fk_a REFERENCES p ON UPDATE SET NULL NOT DEFERRABLE INITIALLY IMMEDIATE NOT NULL DEFAULT 1)",
			"CREATE TABLE o (a INT NOT NULL DEFAULT 1)"},
		{"set default and match", "CREATE TABLE o (a INT REFERENCES p(id) ON DELETE SET DEFAULT MATCH SIMPLE DEFERRABLE INITIALLY DEFERRED DEFAULT 0)",
			"CREATE TABLE o (a INT DEFAULT 0)"},
		{"no action", "CREATE TABLE o (a INT REFERENCES \"p\" (\"id\") ON DELETE NO ACTION, b INT)", "CREATE TABLE o (a INT, b INT)"},
		{"table constraint", "CREATE TABLE o (\n  a INT,\n  b INT,\n  FOREIGN KEY (a, b) REFERENCES p (x, y) ON DELETE CASCADE\n)",
			"CREATE TABLE o (\n  a INT,\n  b INT\n)"},
		{"named table constraint in the middle",
			"CREATE TABLE o (a INT, CONSTRAINT [fk o] FOREIGN KEY (a) REFERENCES p (x), CHECK (a > 0)) WITHOUT ROWID",
			"CREATE TABLE o (a INT, CHECK (a > 0)) WITHOUT ROWID"},
		{"strings comments and names stay",
			"CREATE TABLE \"references\" (\"REFERENCES\" TEXT DEFAULT 'REFERENCES p', -- REFERENCES p\n b INT /* REFERENCES q */)",
			"CREATE TABLE \"references\" (\"REFERENCES\" TEXT DEFAULT 'REFERENCES p', -- REFERENCES p\n b INT /* REFERENCES q */)"},
		{"no foreign keys", "CREATE TABLE t (id INTEGER PRIMARY KEY, x TEXT CHECK (x IN ('a', 'b')))",
			"CREATE TABLE t (id INTEGER PRIMARY KEY, x TEXT CHECK (x IN ('a', 'b')))"},
	} {
		assert.Equal(t, tt.want, withoutForeignKeys(tt.in), tt.name)
	}
}

// 「包含索引」不勾时不写表的索引（约束自动建的索引随建表语句，不受影响），唯一索引除外（外键可能引用它）；「包含外键」不勾时建表语句里没有
// 外键；「关闭外键检查」不勾时没有 PRAGMA。SQLite 能在会话里关闭外键检查（驱动描述由方言得出）。
func TestDumpSQLiteHonoursTheStructureOptions(t *testing.T) {
	assert.True(t, descriptor.DumpDisablesForeignKeyChecks)
	session := openFileSession(t, "options.db")
	execAll(t, session,
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT UNIQUE)",
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INT REFERENCES users (id) ON DELETE CASCADE)",
		"CREATE INDEX orders_user ON orders (user_id)",
		"CREATE UNIQUE INDEX users_name_lower ON users (lower(name))",
	)
	dump := func(options dbdriver.DumpOptions) string {
		var out strings.Builder
		_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
			Database: "main", Tables: []string{"users", "orders"}, Options: options, Header: dumpHeader(), Writer: &out,
		})
		require.NoError(t, err)
		return out.String()
	}
	full := dump(dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, DisableForeignKeyChecks: true})
	assert.Contains(t, full, "CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INT REFERENCES users (id) ON DELETE CASCADE);\nCREATE INDEX orders_user ON orders (user_id);\n")
	assert.Contains(t, full, "\nPRAGMA foreign_keys = false;\n")

	none := dump(dbdriver.DumpOptions{})
	assert.Contains(t, none, "CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INT);\n")
	assert.Contains(t, none, "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT UNIQUE);\n")
	assert.NotContains(t, none, "CREATE INDEX")
	assert.Contains(t, none, "CREATE UNIQUE INDEX users_name_lower ON users (lower(name));\n")
	assert.NotContains(t, none, "PRAGMA")
	assert.NotContains(t, none, "DROP")
}

// 覆盖还原：勾了「写入删除语句」，整库（表之间有外键、视图、触发器、数据）转储后运行回原来的库——库里还有
// 这些对象与数据——成功，结构与数据与转储前一样（触发器没有在导入时多记行）。不勾时文件里没有删除语句。
func TestDumpOverwritesTheOriginalDatabase(t *testing.T) {
	ctx := context.Background()
	source := openFileSession(t, "overwrite.db")
	wholeDatabase(t, source)
	execAll(t, source,
		"CREATE TABLE tags (id INTEGER PRIMARY KEY, item_id INT NOT NULL REFERENCES items (id), label TEXT)",
		"CREATE INDEX tags_item ON tags (item_id)",
		"INSERT INTO tags (item_id, label) VALUES (1, 'a'), (2, 'b')",
		"PRAGMA foreign_keys = true",
	)
	schema := "SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name"
	data := []string{
		"SELECT * FROM items ORDER BY id", "SELECT * FROM audit ORDER BY id", "SELECT * FROM tags ORDER BY id",
		"SELECT name, seq FROM sqlite_sequence ORDER BY name",
	}
	before := map[string][][]any{schema: queryRows(t, source, schema)}
	for _, query := range data {
		before[query] = queryRows(t, source, query)
	}
	dir := t.TempDir()

	withoutDrops := allOptions
	withoutDrops.Drops = false
	_, err := dbdriver.DumpToFile(ctx, source, dbdriver.DumpRequest{
		Database: "main", Whole: true, WithData: true, Options: withoutDrops, Header: dumpHeader(),
	}, filepath.Join(dir, "plain.sql"))
	require.NoError(t, err)
	plain, err := os.ReadFile(filepath.Join(dir, "plain.sql")) //nolint:gosec // the test's own temp file
	require.NoError(t, err)
	assert.NotContains(t, string(plain), "DROP")

	path := filepath.Join(dir, "overwrite.sql")
	_, err = dbdriver.DumpToFile(ctx, source, dbdriver.DumpRequest{
		Database: "main", Whole: true, WithData: true, Options: allOptions, Header: dumpHeader(),
	}, path)
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)
	file, err := os.Open(filepath.Clean(path))
	require.NoError(t, err)
	run, err := source.RunScript(ctx, dbdriver.ScriptRequest{Database: "main", Script: file, Size: int64(len(content))})
	require.NoError(t, file.Close())
	require.NoError(t, err)
	require.Nil(t, run.Failure, "the dump must run back into the database it came from")
	assert.Zero(t, run.Failed)

	for query, rows := range before {
		assert.Equal(t, rows, queryRows(t, source, query), query)
	}
	assert.Empty(t, queryRows(t, source, "SELECT * FROM pragma_foreign_key_check"))
}

// 整库转储里的虚表（FTS5）：它的影子表（docs_data、docs_config 等，CREATE VIRTUAL TABLE 自己建）不另写，
// 文件在空库上运行成功，全文检索照样查得到原来的行。
func TestDumpRoundTripsAVirtualTableWithoutItsShadowTables(t *testing.T) {
	ctx := context.Background()
	source := openFileSession(t, "source.db")
	execAll(t, source,
		"CREATE VIRTUAL TABLE docs USING fts5(title, body)",
		"INSERT INTO docs VALUES ('first', 'hello world'), ('second', 'goodbye moon')",
	)
	var out strings.Builder
	_, err := source.Dump(ctx, dbdriver.DumpRequest{
		Database: "main", Whole: true, WithData: true, Options: allOptions, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)
	script := out.String()
	assert.NotContains(t, script, "docs_config")

	target := openFileSession(t, "target.db")
	run, err := target.RunScript(ctx, dbdriver.ScriptRequest{Database: "main", Script: strings.NewReader(script), Size: int64(len(script))})
	require.NoError(t, err)
	require.Nil(t, run.Failure, "the dump must run on an empty database: %+v", run.Failure)
	assert.Equal(t, [][]any{{"first"}}, queryRows(t, target, "SELECT title FROM docs WHERE docs MATCH 'hello'"))
}

// 没有声明类型（或声明的不是 BLOB）的列里存着不是 UTF-8 文本的二进制值：转储写成 X'…'，文件仍是 UTF-8，在空库上
// 运行成功，字节一样、仍是 blob。
func TestDumpWritesBinaryValuesOfUntypedColumnsAsBlobLiterals(t *testing.T) {
	ctx := context.Background()
	source := openFileSession(t, "source.db")
	execAll(t, source,
		"CREATE TABLE kv (k TEXT PRIMARY KEY, v)",
		"INSERT INTO kv VALUES ('raw', X'00FF10FE'), ('text', 'plain')",
	)
	var out strings.Builder
	_, err := source.Dump(ctx, dbdriver.DumpRequest{
		Database: "main", Tables: []string{"kv"}, WithData: true, Options: allOptions, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)
	script := out.String()

	target := openFileSession(t, "target.db")
	run, err := target.RunScript(ctx, dbdriver.ScriptRequest{Database: "main", Script: strings.NewReader(script), Size: int64(len(script))})
	require.NoError(t, err)
	require.Nil(t, run.Failure, "the dump must run on an empty database: %+v", run.Failure)
	query := "SELECT k, hex(v), typeof(v) FROM kv ORDER BY k"
	assert.Equal(t, queryRows(t, source, query), queryRows(t, target, query))
}

// AUTOINCREMENT 的表：最大的那一行被删掉之后，sqlite_sequence 里的值比现存的行大；结构和数据的转储在空库上
// 还原出同样的值，之后插入的行不会重用删掉的 id。
func TestDumpRestoresTheAutoIncrementCounter(t *testing.T) {
	ctx := context.Background()
	source := openFileSession(t, "source.db")
	execAll(t, source,
		"CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT)",
		"INSERT INTO notes (body) VALUES ('a'), ('b'), ('c')",
		"DELETE FROM notes WHERE id = 3",
	)
	var out strings.Builder
	_, err := source.Dump(ctx, dbdriver.DumpRequest{
		Database: "main", Tables: []string{"notes"}, WithData: true, Options: allOptions, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)
	script := out.String()

	target := openFileSession(t, "target.db")
	for range 2 {
		run, err := target.RunScript(ctx, dbdriver.ScriptRequest{Database: "main", Script: strings.NewReader(script), Size: int64(len(script))})
		require.NoError(t, err)
		require.Nil(t, run.Failure, "the dump must run on an empty database and again over itself: %+v", run.Failure)
	}
	query := "SELECT name, seq FROM sqlite_sequence ORDER BY name"
	assert.Equal(t, queryRows(t, source, query), queryRows(t, target, query))
}
