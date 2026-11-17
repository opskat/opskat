//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// sourceSchema 是往返用的源库：主键、唯一、索引、外键、自增、生成列（STORED 与 VIRTUAL）、注释、二进制与
// NULL、依赖另一个视图的视图（名字的字母序与依赖相反）、过程、函数和触发器。
var sourceSchema = []string{
	"CREATE TABLE parent (" +
		"id INT NOT NULL AUTO_INCREMENT, code VARCHAR(40) NOT NULL, note TEXT COMMENT 'a column comment', " +
		"PRIMARY KEY (id), UNIQUE KEY uq_code (code), KEY idx_note (note(20))) COMMENT='the parent table'",
	"CREATE TABLE child (" +
		"id BIGINT NOT NULL AUTO_INCREMENT, parent_id INT NOT NULL, label VARCHAR(100) DEFAULT NULL, payload BLOB, " +
		"label_chars INT GENERATED ALWAYS AS (CHAR_LENGTH(label)) STORED, label_bytes INT GENERATED ALWAYS AS (LENGTH(label)) VIRTUAL, " +
		"PRIMARY KEY (id), KEY idx_label (label), " +
		"CONSTRAINT fk_child_parent FOREIGN KEY (parent_id) REFERENCES parent (id) ON DELETE CASCADE)",
	"INSERT INTO parent (code, note) VALUES ('plain', 'hello'), ('it''s', 'say \"hi\"; -- not a comment /* nor this */'), " +
		"('back\\\\slash', 'C:\\\\new\\\\table\\\\x'), ('emoji', '😀 中文 émoji'), ('nul', NULL), ('empty', '')",
	"INSERT INTO child (parent_id, label, payload) VALUES (1, 'one', X'00FF10'), (1, '😀 two', X''), (2, NULL, NULL), " +
		"(4, 'line\nbreak;', X'0A0D5C27'), (5, 'to delete', NULL)",
	// 删掉最大的 id：自增的当前值比现有最大的 id 大，要原样还原。
	"DELETE FROM child WHERE label = 'to delete'",
	"CREATE VIEW v_z_base AS SELECT id, label, label_chars FROM child",
	"CREATE VIEW v_a_top AS SELECT b.id, b.label, p.code FROM v_z_base b JOIN child c ON c.id = b.id JOIN parent p ON p.id = c.parent_id",
	"CREATE PROCEDURE p_count_children(IN pid INT, OUT total INT) BEGIN SELECT COUNT(*) INTO total FROM child WHERE parent_id = pid; END",
	"CREATE FUNCTION f_double(x INT) RETURNS INT DETERMINISTIC RETURN x * 2",
	"CREATE TRIGGER trg_parent_note BEFORE INSERT ON parent FOR EACH ROW BEGIN IF NEW.note IS NULL THEN SET NEW.note = 'filled by trigger; ok'; END IF; END",
}

func exec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		_, err := db.Exec(statement)
		require.NoError(t, err, statement)
	}
}

// query 把结果集每行的各列用「|」连成一个文本。
func query(t *testing.T, db *sql.DB, statement string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(statement, args...)
	require.NoError(t, err, statement)
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	require.NoError(t, err)
	var lines []string
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		require.NoError(t, rows.Scan(targets...))
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = "<NULL>"
			if value.Valid {
				parts[i] = value.String
			}
		}
		lines = append(lines, strings.Join(parts, "|"))
	}
	require.NoError(t, rows.Err())
	return lines
}

func createScratchDatabase(t *testing.T, admin *sql.DB, purpose string) string {
	t.Helper()
	name := fmt.Sprintf("opskat_dump_%s_%d", purpose, time.Now().UnixNano())
	_, err := admin.Exec("CREATE DATABASE " + quoteIdent(name) + " CHARACTER SET utf8mb4")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP DATABASE " + quoteIdent(name))
		assert.NoError(t, err)
	})
	return name
}

// bare 去掉文本里的库名限定，好比较两个名字不同的库里的定义。
func bare(text string, databases ...string) string {
	for _, database := range databases {
		text = strings.ReplaceAll(text, quoteIdent(database)+".", "")
	}
	return text
}

// 往返：真实的 MySQL 上把一个包含各类对象的源库整库转储（结构和数据），把文件运行到另一个名字的空库：
// 表（SHOW CREATE TABLE 含自增当前值）、视图、过程、函数、触发器的定义，每张表的行数与数据校验和都一样；
// 文件里没有源库的名字，所以数据没有写回源库。
func TestDumpRoundTripsAWholeDatabaseOnARealMySQL(t *testing.T) {
	admin := openScriptDatabase(t, "")
	ctx := context.Background()
	source := createScratchDatabase(t, admin, "src")
	target := createScratchDatabase(t, admin, "dst")
	sourceDB := openScriptDatabase(t, source)
	targetDB := openScriptDatabase(t, target)
	exec(t, sourceDB, sourceSchema...)

	var file strings.Builder
	result, err := newSession(sourceDB).Dump(ctx, dbdriver.DumpRequest{
		Database: source, Whole: true, WithData: true, Header: dumpHeader(), Writer: &file,
	})
	require.NoError(t, err)
	content := file.String()
	assert.Equal(t, 7, result.Objects, "2 tables, 2 views, 1 procedure, 1 function and 1 trigger")
	assert.NotContains(t, content, quoteIdent(source)+".", "the file must not refer to the source database")

	run, err := newSession(targetDB).RunScript(ctx, dbdriver.ScriptRequest{
		Database: target, Script: strings.NewReader(content), Size: int64(len(content)),
	})
	require.NoError(t, err)
	require.Nil(t, run.Failure, "%+v", run.Failure)
	// 源库没有被动过：转储文件里每条语句都只作用在目标库。
	assert.Equal(t, []string{"6"}, query(t, sourceDB, "SELECT COUNT(*) FROM parent"))

	for _, table := range []string{"parent", "child"} {
		assert.Equal(t, query(t, sourceDB, "SHOW CREATE TABLE "+quoteIdent(table)), query(t, targetDB, "SHOW CREATE TABLE "+quoteIdent(table)), table)
	}
	assert.Contains(t, query(t, targetDB, "SHOW CREATE TABLE `child`")[0], "AUTO_INCREMENT=6", "the next auto_increment value survives the delete of the highest id")

	checksum := func(db *sql.DB, statement string) []string { return query(t, db, statement) }
	for name, statement := range map[string]string{
		"parent": "SELECT COUNT(*), SUM(CRC32(CONCAT_WS('#', id, HEX(code), IFNULL(HEX(note), '<NULL>')))) FROM parent",
		"child": "SELECT COUNT(*), SUM(CRC32(CONCAT_WS('#', id, parent_id, IFNULL(HEX(label), '<NULL>'), IFNULL(HEX(payload), '<NULL>'), " +
			"IFNULL(label_chars, '<NULL>'), IFNULL(label_bytes, '<NULL>')))) FROM child",
	} {
		assert.Equal(t, checksum(sourceDB, statement), checksum(targetDB, statement), name)
	}
	// 逐行（十六进制，二进制与编码都不会被掩盖）比，出错时看得出是哪一行哪一列。
	for _, statement := range []string{
		"SELECT id, HEX(code), IFNULL(HEX(note), '<NULL>') FROM parent ORDER BY id",
		"SELECT id, parent_id, IFNULL(HEX(label), '<NULL>'), IFNULL(HEX(payload), '<NULL>') FROM child ORDER BY id",
	} {
		assert.Equal(t, query(t, sourceDB, statement), query(t, targetDB, statement), statement)
	}
	assert.Equal(t, []string{"6"}, query(t, targetDB, "SELECT COUNT(*) FROM parent"))
	assert.Equal(t, []string{"4"}, query(t, targetDB, "SELECT COUNT(*) FROM child"))

	// 连接的默认库不是视图所在库时，服务器给视图里的每个对象都带上库名；去掉后不再有源库的名字。
	qualified := query(t, admin, "SHOW CREATE VIEW "+quoteIdent(source)+".`v_a_top`")
	require.Len(t, qualified, 1)
	require.Contains(t, qualified[0], quoteIdent(source)+".")
	stripped, _ := stripDatabase(qualified[0], source)
	assert.NotContains(t, stripped, quoteIdent(source)+".")

	// 视图、过程、函数、触发器：定义一样（库名限定除外），DEFINER 保留。
	viewDefinitions := "SELECT TABLE_NAME, VIEW_DEFINITION, DEFINER, SECURITY_TYPE FROM information_schema.VIEWS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME"
	assert.Equal(t, bare(strings.Join(query(t, sourceDB, viewDefinitions), "\n"), source), bare(strings.Join(query(t, targetDB, viewDefinitions), "\n"), target))
	assert.Len(t, query(t, targetDB, viewDefinitions), 2)
	routines := "SELECT ROUTINE_NAME, ROUTINE_TYPE, DTD_IDENTIFIER, ROUTINE_DEFINITION, IS_DETERMINISTIC, DEFINER FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() ORDER BY ROUTINE_NAME"
	assert.Equal(t, query(t, sourceDB, routines), query(t, targetDB, routines))
	assert.Len(t, query(t, targetDB, routines), 2)
	triggers := "SELECT TRIGGER_NAME, EVENT_MANIPULATION, EVENT_OBJECT_TABLE, ACTION_TIMING, ACTION_STATEMENT, DEFINER FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY TRIGGER_NAME"
	assert.Equal(t, query(t, sourceDB, triggers), query(t, targetDB, triggers))
	assert.Len(t, query(t, targetDB, triggers), 1)

	// 还原出来的对象照样工作：自增接着原来的值，触发器、函数与视图的结果与源库一样。
	for _, db := range []*sql.DB{sourceDB, targetDB} {
		exec(t, db, "INSERT INTO parent (code, note) VALUES ('after', NULL)", "INSERT INTO child (parent_id, label) VALUES (1, 'after')")
	}
	for _, statement := range []string{
		"SELECT id, code, note FROM parent WHERE code = 'after'",
		"SELECT id, label, label_chars, label_bytes FROM child WHERE label = 'after'",
		"SELECT f_double(21)",
		"SELECT id, label, code FROM v_a_top ORDER BY id",
	} {
		assert.Equal(t, query(t, sourceDB, statement), query(t, targetDB, statement), statement)
	}
	assert.Equal(t, []string{"7|after|filled by trigger; ok"}, query(t, targetDB, "SELECT id, code, note FROM parent WHERE code = 'after'"))
}
