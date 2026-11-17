package postgresql

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

const (
	viewsSQL = "SELECT table_schema || '.' || table_name FROM information_schema.views WHERE table_schema NOT IN " +
		systemSchemas + " ORDER BY 1"
	triggersSQL = "SELECT DISTINCT trigger_schema, trigger_name, event_object_table FROM information_schema.triggers " +
		"WHERE trigger_schema NOT IN " + systemSchemas + " ORDER BY trigger_schema, trigger_name"
	listTablesSQL = "SELECT table_schema || '.' || table_name AS name FROM information_schema.tables " +
		"WHERE table_schema NOT IN " + systemSchemas + " AND table_type IN ('BASE TABLE', 'VIEW') ORDER BY table_schema, table_name"
	triggerDefSQL = "SELECT pg_get_triggerdef(tg.oid, true) FROM pg_trigger tg JOIN pg_class c ON c.oid = tg.tgrelid " +
		"JOIN pg_namespace n ON n.oid = c.relnamespace " +
		"WHERE n.nspname = $1 AND c.relname = $2 AND tg.tgname = $3 AND NOT tg.tgisinternal LIMIT 1"
)

func dumpHeader() dbdriver.DumpHeader {
	return dbdriver.DumpHeader{Version: "1.16.0", Asset: "prod-pg", Time: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
}

// readGolden 读 testdata 里的金样文件。
func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS("testdata"), name)
	require.NoError(t, err)
	return string(data)
}

func enumRows(enums ...[3]string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"nspname", "typname", "labels"})
	for _, e := range enums {
		rows.AddRow(e[0], e[1], e[2])
	}
	return rows
}

func dumpColumnRows(columns ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"attname", "identity_always"})
	for _, c := range columns {
		name, always := strings.CutSuffix(c, " always")
		rows.AddRow(name, always)
	}
	return rows
}

// 整库转储（结构和数据，除了关闭外键检查全勾）写出的文件：开头关掉函数体检查、建出 public 之外的 schema；删除段
// 先删各表上的外键（PostgreSQL 不能关闭外键检查，被引用的表要先拆外键才删得掉），再删触发器（带上它的表）、过程、
// 函数（每个重载带完整的参数）、视图（依赖别的视图的先删）、表，最后删枚举类型，都不带 CASCADE；枚举类型在所有表之前；
// 表之后是它的行（不写生成列，有 GENERATED ALWAYS 身份列的表用 OVERRIDING SYSTEM VALUE），外键（含自引用）
// 在全部数据之后用 ALTER TABLE 加上；视图按 pg_depend 的依赖排序；函数每个重载都写出，删除带完整的参数；
// 过程同样；触发器在所有数据之后，删除带上它的表；序列在最后同步到当前值；结尾恢复函数体检查。
func TestDumpWritesRestorablePostgreSQLFile(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(viewsSQL).WillReturnRows(names("v", "public.v_a", "public.v_b"))
	mock.ExpectQuery(proceduresSQL).WillReturnRows(names("r", "app.cleanup"))
	mock.ExpectQuery(functionsSQL).WillReturnRows(names("r", "public.touch"))
	mock.ExpectQuery(triggersSQL).WillReturnRows(sqlmock.NewRows([]string{"s", "n", "t"}).AddRow("public", "items_touch", "items"))
	mock.ExpectQuery(listTablesSQL).WillReturnRows(names("name", "app.notes", "public.items", "public.v_a", "public.v_b"))
	mock.ExpectQuery(dumpSchemasQuery).WillReturnRows(names("nspname", "app"))
	mock.ExpectQuery(scopeEnumsQuery).WithArgs("", `["app.notes","public.items","public.v_a","public.v_b"]`).WillReturnRows(enumRows([3]string{"public", "kind", `["new", "it's"]`}))

	// app.notes：serial 主键，外键指向 public.items。
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("app", "notes").WillReturnRows(ddlColumnRows("r", nil,
		ddlCol{name: "id", dataType: "integer", def: "nextval('app.notes_id_seq'::regclass)"},
		ddlCol{name: "item_id", dataType: "integer", nullable: true}))
	mock.ExpectQuery(ownedSequencesQuery).WithArgs("app", "notes").WillReturnRows(ownedSequenceRows().
		AddRow("app", "notes_id_seq", "id", "integer", "1", "1", "1", "2147483647", false))
	mock.ExpectQuery(tableConstraintsQuery).WithArgs("app", "notes").WillReturnRows(constraintRows([2]string{"notes_pkey", "PRIMARY KEY (id)"}))
	mock.ExpectQuery(tableIndexesQuery).WithArgs("app", "notes").WillReturnRows(names("def"))
	mock.ExpectQuery(dumpForeignKeysQuery).WithArgs("app", "notes").
		WillReturnRows(constraintRows([2]string{"notes_item_fk", "FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE"}))
	mock.ExpectQuery(dumpColumnsQuery).WithArgs("app", "notes").WillReturnRows(dumpColumnRows("id", "item_id"))
	mock.ExpectQuery(dumpSequencesQuery).WithArgs("app", "notes").WillReturnRows(sqlmock.NewRows([]string{"attname", "last_value"}).AddRow("id", 3))

	// public.items：GENERATED ALWAYS 身份列、枚举列、生成列、自引用的外键。
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("public", "items").WillReturnRows(ddlColumnRows("r", nil,
		ddlCol{name: "id", dataType: "integer", identity: "ALWAYS", identityOptions: [4]any{"1", "1", "1", "2147483647"}},
		ddlCol{name: "kind", dataType: "public.kind", nullable: true},
		ddlCol{name: "doubled", dataType: "integer", nullable: true, generated: "(id * 2)", generatedKind: "s"},
		ddlCol{name: "parent_id", dataType: "integer", nullable: true}))
	mock.ExpectQuery(ownedSequencesQuery).WithArgs("public", "items").WillReturnRows(ownedSequenceRows())
	mock.ExpectQuery(tableConstraintsQuery).WithArgs("public", "items").WillReturnRows(constraintRows([2]string{"items_pkey", "PRIMARY KEY (id)"}))
	mock.ExpectQuery(tableIndexesQuery).WithArgs("public", "items").WillReturnRows(names("def"))
	mock.ExpectQuery(dumpForeignKeysQuery).WithArgs("public", "items").
		WillReturnRows(constraintRows([2]string{"items_parent_fk", "FOREIGN KEY (parent_id) REFERENCES items(id)"}))
	mock.ExpectQuery(dumpColumnsQuery).WithArgs("public", "items").WillReturnRows(dumpColumnRows("id always", "kind", "parent_id"))
	mock.ExpectQuery(dumpSequencesQuery).WithArgs("public", "items").WillReturnRows(sqlmock.NewRows([]string{"attname", "last_value"}).AddRow("id", 9007199254740993))

	// 视图：列出的次序是 v_a、v_b，v_a 用到 v_b。
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("public", "v_a").WillReturnRows(ddlColumnRows("v", nil, ddlCol{name: "id", dataType: "integer", nullable: true}))
	mock.ExpectQuery(viewDefSQL).WithArgs("public", "v_a").WillReturnRows(names("def", " SELECT id\n   FROM v_b;"))
	mock.ExpectQuery(viewDependenciesQuery).WithArgs("public", "v_a").WillReturnRows(names("name", "public.v_b"))
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("public", "v_b").WillReturnRows(ddlColumnRows("v", nil, ddlCol{name: "id", dataType: "integer", nullable: true}))
	mock.ExpectQuery(viewDefSQL).WithArgs("public", "v_b").WillReturnRows(names("def", " SELECT id\n   FROM items;"))
	mock.ExpectQuery(viewDependenciesQuery).WithArgs("public", "v_b").WillReturnRows(names("name", "public.items"))

	// 函数的两个重载、过程、触发器。
	// 每个对象先读源码（ObjectSource），方言再读一次它的重载。
	touch := func() *sqlmock.Rows {
		return routineRows(
			[2]string{"n integer", "CREATE OR REPLACE FUNCTION public.touch(n integer)\n RETURNS integer\n LANGUAGE sql\nAS $function$SELECT n$function$\n"},
			[2]string{"t text", "CREATE OR REPLACE FUNCTION public.touch(t text)\n RETURNS text\n LANGUAGE sql\nAS $function$SELECT t$function$\n"})
	}
	mock.ExpectQuery(routinesQuery).WithArgs("public", "touch", "f").WillReturnRows(touch())
	mock.ExpectQuery(routinesQuery).WithArgs("public", "touch", "f").WillReturnRows(touch())
	cleanup := func() *sqlmock.Rows {
		return routineRows([2]string{"", "CREATE OR REPLACE PROCEDURE app.cleanup()\n LANGUAGE sql\nAS $procedure$DELETE FROM app.notes$procedure$\n"})
	}
	mock.ExpectQuery(routinesQuery).WithArgs("app", "cleanup", "p").WillReturnRows(cleanup())
	mock.ExpectQuery(routinesQuery).WithArgs("app", "cleanup", "p").WillReturnRows(cleanup())
	mock.ExpectQuery(triggerDefSQL).WithArgs("public", "items", "items_touch").
		WillReturnRows(names("def", "CREATE TRIGGER items_touch BEFORE INSERT ON items FOR EACH ROW EXECUTE FUNCTION touch_items()"))

	// 全部定义读完才读行（删除段在文件开头，要用到每个对象的删除语句）。
	expectColumns(mock, "app", "notes", [2]string{"id", "integer"}, [2]string{"item_id", "integer"})
	mock.ExpectQuery(`SELECT * FROM "app"."notes"`).WillReturnRows(sqlmock.NewRows([]string{"id", "item_id"}).
		AddRow(int64(1), int64(7)).AddRow(int64(3), nil))
	expectColumns(mock, "public", "items", [2]string{"id", "integer"}, [2]string{"kind", "USER-DEFINED"},
		[2]string{"doubled", "integer"}, [2]string{"parent_id", "integer"})
	mock.ExpectQuery(`SELECT * FROM "public"."items"`).WillReturnRows(sqlmock.NewRows([]string{"id", "kind", "doubled", "parent_id"}).
		AddRow(int64(7), "it's", int64(14), nil).AddRow(int64(9007199254740993), "new", int64(0), int64(7)))

	var out strings.Builder
	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "shop", Whole: true, WithData: true, Header: dumpHeader(), Writer: &out,
		Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Views: true, Routines: true, Triggers: true, Drops: true},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, readGolden(t, "dump_whole.sql"), out.String())
}

// 只转储选中的表（仅结构）：只写这些表的列用到的枚举类型，建出它们所在的 schema（public 除外）；不读行、
// 不同步序列，外键仍在全部表之后。
func TestDumpSelectedTablesWritesOnlyTheEnumsTheyUse(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(viewsSQL).WillReturnRows(names("v"))
	mock.ExpectQuery(proceduresSQL).WillReturnRows(names("r"))
	mock.ExpectQuery(functionsSQL).WillReturnRows(names("r"))
	mock.ExpectQuery(triggersSQL).WillReturnRows(sqlmock.NewRows([]string{"s", "n", "t"}))
	mock.ExpectQuery(usedEnumsQuery).WithArgs(`["app.notes"]`).WillReturnRows(enumRows([3]string{"lookup", "Mood", `["ok"]`}))
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("app", "notes").WillReturnRows(ddlColumnRows("r", nil,
		ddlCol{name: "mood", dataType: `lookup."Mood"`, nullable: true}))
	mock.ExpectQuery(ownedSequencesQuery).WithArgs("app", "notes").WillReturnRows(ownedSequenceRows())
	mock.ExpectQuery(tableConstraintsQuery).WithArgs("app", "notes").WillReturnRows(constraintRows())
	mock.ExpectQuery(tableIndexesQuery).WithArgs("app", "notes").WillReturnRows(names("def"))
	mock.ExpectQuery(dumpForeignKeysQuery).WithArgs("app", "notes").WillReturnRows(constraintRows())

	var out strings.Builder
	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "shop", Tables: []string{"app.notes"}, Header: dumpHeader(), Writer: &out,
		Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, readGolden(t, "dump_tables.sql"), out.String())
}

// 整个 schema：只建这个 schema、只写它里面的枚举类型。
func TestDumpWholeSchemaKeepsToItsSchema(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(viewsSQL).WillReturnRows(names("v"))
	mock.ExpectQuery(proceduresSQL).WillReturnRows(names("r", "public.other"))
	mock.ExpectQuery(functionsSQL).WillReturnRows(names("r"))
	mock.ExpectQuery(triggersSQL).WillReturnRows(sqlmock.NewRows([]string{"s", "n", "t"}))
	mock.ExpectQuery(listTablesSQL).WillReturnRows(names("name", "app.notes", "public.items"))
	mock.ExpectQuery(scopeEnumsQuery).WithArgs("app", `["app.notes"]`).WillReturnRows(enumRows([3]string{"app", "state", `[]`}))
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("app", "notes").WillReturnRows(ddlColumnRows("r", nil,
		ddlCol{name: "state", dataType: "app.state", nullable: true}))
	mock.ExpectQuery(ownedSequencesQuery).WithArgs("app", "notes").WillReturnRows(ownedSequenceRows())
	mock.ExpectQuery(tableConstraintsQuery).WithArgs("app", "notes").WillReturnRows(constraintRows())
	mock.ExpectQuery(tableIndexesQuery).WithArgs("app", "notes").WillReturnRows(names("def"))
	mock.ExpectQuery(dumpForeignKeysQuery).WithArgs("app", "notes").WillReturnRows(constraintRows())

	var out strings.Builder
	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "shop", Schema: "app", Whole: true, Header: dumpHeader(), Writer: &out,
		Options: dbdriver.DumpOptions{Indexes: true, ForeignKeys: true, Views: true, Routines: true, Triggers: true},
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, readGolden(t, "dump_schema.sql"), out.String())
}

// expectNotesDDL 预期 app.notes 的建表语句的查询（有一个不由约束建出的索引）；indexes 为假时只查唯一索引（没有）。
func expectNotesDDL(mock sqlmock.Sqlmock, indexes bool) {
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("app", "notes").WillReturnRows(ddlColumnRows("r", nil,
		ddlCol{name: "id", dataType: "integer"}, ddlCol{name: "item_id", dataType: "integer", nullable: true}))
	mock.ExpectQuery(ownedSequencesQuery).WithArgs("app", "notes").WillReturnRows(ownedSequenceRows())
	mock.ExpectQuery(tableConstraintsQuery).WithArgs("app", "notes").WillReturnRows(constraintRows([2]string{"notes_pkey", "PRIMARY KEY (id)"}))
	if indexes {
		mock.ExpectQuery(tableIndexesQuery).WithArgs("app", "notes").
			WillReturnRows(names("def", `CREATE INDEX notes_item ON app.notes USING btree (item_id)`))
		return
	}
	mock.ExpectQuery(tableUniqueIndexesQuery).WithArgs("app", "notes").WillReturnRows(names("def"))
}

// 选中一张有索引与外键的表：「包含索引」不勾时建表语句之后没有它的索引（主键随建表语句）；「包含外键」不勾时
// 全部数据之后没有外键；勾了「写入删除语句」而没勾「包含外键」时，删除段仍先删它的外键（目标库里的外键挡着删表）。
func TestDumpLeavesOutIndexesAndForeignKeysWhenExcluded(t *testing.T) {
	dump := func(t *testing.T, options dbdriver.DumpOptions) string {
		t.Helper()
		session, mock := newMock(t)
		mock.ExpectQuery(viewsSQL).WillReturnRows(names("v"))
		mock.ExpectQuery(proceduresSQL).WillReturnRows(names("r"))
		mock.ExpectQuery(functionsSQL).WillReturnRows(names("r"))
		mock.ExpectQuery(triggersSQL).WillReturnRows(sqlmock.NewRows([]string{"s", "n", "t"}))
		mock.ExpectQuery(usedEnumsQuery).WithArgs(`["app.notes"]`).WillReturnRows(enumRows())
		expectNotesDDL(mock, true)
		if !options.Indexes {
			expectNotesDDL(mock, false)
		}
		mock.ExpectQuery(dumpForeignKeysQuery).WithArgs("app", "notes").
			WillReturnRows(constraintRows([2]string{"notes_item_fk", "FOREIGN KEY (item_id) REFERENCES items(id)"}))
		var out strings.Builder
		_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
			Database: "shop", Tables: []string{"app.notes"}, Options: options, Header: dumpHeader(), Writer: &out,
		})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
		return out.String()
	}
	index := "\n\nCREATE INDEX notes_item ON app.notes USING btree (item_id);"
	foreignKey := `ALTER TABLE "app"."notes" ADD CONSTRAINT "notes_item_fk" FOREIGN KEY (item_id) REFERENCES items(id);`
	dropForeignKey := `ALTER TABLE IF EXISTS "app"."notes" DROP CONSTRAINT IF EXISTS "notes_item_fk";`

	full := dump(t, dbdriver.DumpOptions{Indexes: true, ForeignKeys: true})
	assert.Contains(t, full, "  CONSTRAINT \"notes_pkey\" PRIMARY KEY (id)\n);"+index+"\n")
	assert.Contains(t, full, foreignKey)
	assert.NotContains(t, full, "DROP")

	bare := dump(t, dbdriver.DumpOptions{Drops: true})
	assert.Contains(t, bare, "  CONSTRAINT \"notes_pkey\" PRIMARY KEY (id)\n);\n")
	assert.NotContains(t, bare, "CREATE INDEX")
	assert.NotContains(t, bare, "ADD CONSTRAINT")
	assert.Contains(t, bare, "-- Drop existing objects\n-- ----------------------------\n"+dropForeignKey+"\nDROP TABLE IF EXISTS \"app\".\"notes\";\n")
}
