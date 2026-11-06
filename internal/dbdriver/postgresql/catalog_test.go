package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func newMock(t *testing.T) (dbdriver.Session, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return newSession(db), mock
}

func names(column string, values ...string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{column})
	for _, v := range values {
		rows.AddRow(v)
	}
	return rows
}

func TestListDatabasesSkipsTemplates(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname").
		WillReturnRows(names("datname", "admdb", "postgres"))

	databases, err := session.ListDatabases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"admdb", "postgres"}, databases)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 表与视图以 schema.table 限定，系统 schema 不列。
func TestListTablesQualifiesWithSchema(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SELECT table_schema || '.' || table_name AS name FROM information_schema.tables " +
		"WHERE table_schema NOT IN ('pg_catalog','information_schema') " +
		"AND table_type IN ('BASE TABLE', 'VIEW') ORDER BY table_schema, table_name").
		WillReturnRows(names("name", "adm.ads_audit", "public.users"))

	tables, err := session.ListTables(context.Background(), "admdb")
	require.NoError(t, err)
	assert.Equal(t, []string{"adm.ads_audit", "public.users"}, tables)
	require.NoError(t, mock.ExpectationsWereMet())
}

var seqDefault = "nextval('events_id_seq'::regclass)"

func TestTableMetadataSplitsTheQualifiedName(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery("SELECT column_name, data_type, udt_name, is_nullable, column_default, "+
		"col_description(format('%I.%I', table_schema, table_name)::regclass, ordinal_position::int) AS column_comment, "+
		"(is_identity = 'YES' OR column_default LIKE 'nextval(%') AS is_auto_increment, "+
		"(identity_generation = 'ALWAYS') AS is_identity_always "+
		"FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position").
		WithArgs("reporting", "events").WillReturnRows(
		sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default", "column_comment"}).
			AddRow("id", "integer", "int4", "NO", "nextval('events_id_seq'::regclass)", "event id").AddRow("note", "text", "text", "YES", nil, nil))
	mock.ExpectQuery("SELECT kcu.column_name FROM information_schema.table_constraints tc "+
		"JOIN information_schema.key_column_usage kcu "+
		"ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema "+
		"WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'PRIMARY KEY' ORDER BY kcu.ordinal_position").
		WithArgs("reporting", "events").WillReturnRows(names("column_name", "id"))
	expectIndexes(mock, "reporting", "events", primaryKeyRow("events_pkey", "id"))
	mock.ExpectQuery(foreignKeysSQL).
		WithArgs("reporting", "events").WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c", "d", "u", "d"}))

	meta, err := session.TableMetadata(context.Background(), "admdb", "reporting.events")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ColumnMeta{
		{Name: "id", Type: "integer", PrimaryKey: true, Comment: "event id", Default: &seqDefault},
		{Name: "note", Type: "text", Nullable: true},
	}, meta.Columns)
	assert.Equal(t, []dbdriver.IndexMeta{{Name: "events_pkey", Columns: []string{"id"}, Unique: true}}, meta.Indexes)
	assert.NotNil(t, meta.ForeignKeys)
	assert.Empty(t, meta.ForeignKeys)
	require.NoError(t, mock.ExpectationsWereMet())
}

// expectTableMetadataColumns 是 TableMetadata 先读的列定义与主键（这里不关心，返回空）。
func expectTableMetadataColumns(mock sqlmock.Sqlmock, schema, table string) {
	mock.ExpectQuery(columnsQuery).WithArgs(schema, table).WillReturnRows(
		sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default", "column_comment"}).
			AddRow("code", "text", "text", "YES", nil, nil))
	mock.ExpectQuery("SELECT kcu.column_name FROM information_schema.table_constraints tc "+
		"JOIN information_schema.key_column_usage kcu "+
		"ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema "+
		"WHERE tc.table_schema = $1 AND tc.table_name = $2 AND tc.constraint_type = 'PRIMARY KEY' ORDER BY kcu.ordinal_position").
		WithArgs(schema, table).WillReturnRows(names("column_name"))
}

// 覆盖索引（INCLUDE）的非键列不是键列：IndexMeta.Columns 只有键列（按键的次序），INCLUDE 列在 Include 里
// （改动前按 indnatts 取全部列，INCLUDE 列被当成了键列）；表达式键按次序显示表达式原文，列名不加引号。
func TestTableMetadataSeparatesIncludeColumns(t *testing.T) {
	session, mock := newMock(t)
	expectTableMetadataColumns(mock, "public", "events")
	// CREATE INDEX idx_cover ON events (code DESC, "UserId") INCLUDE (email)；CREATE INDEX idx_expr ON events (lower(email))。
	expectIndexes(mock, "public", "events",
		indexRow{name: "idx_cover", method: "btree", column: "code", text: "code", option: 3},
		keyRow("idx_cover", "UserId"),
		indexRow{name: "idx_cover", method: "btree", include: true, column: "email", text: "email"},
		indexRow{name: "idx_expr", method: "btree", text: "lower(email)"},
	)
	mock.ExpectQuery(foreignKeysSQL).WithArgs("public", "events").
		WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c", "d", "u", "d"}))

	meta, err := session.TableMetadata(context.Background(), "admdb", "events")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.IndexMeta{
		{Name: "idx_cover", Columns: []string{"code", "UserId"}, Include: []string{"email"}},
		{Name: "idx_expr", Columns: []string{"lower(email)"}},
	}, meta.Indexes)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 外键按 pg_constraint 读：多列外键按列的次序一一对应（不再与被引用列交叉相乘），带上 ON UPDATE / ON DELETE
// 的动作（confupdtype / confdeltype 在查询里换成 CASCADE、SET NULL…）。
func TestTableMetadataReadsForeignKeyActions(t *testing.T) {
	session, mock := newMock(t)
	expectTableMetadataColumns(mock, "public", "orders")
	expectIndexes(mock, "public", "orders")
	mock.ExpectQuery(foreignKeysSQL).WithArgs("public", "orders").WillReturnRows(
		sqlmock.NewRows([]string{"constraint_name", "column_name", "referenced_table", "referenced_column", "on_update", "on_delete"}).
			AddRow("fk_user", "user_id", "users", "id", "NO ACTION", "CASCADE").
			AddRow("fk_item", "item_tenant", "items", "tenant", "SET NULL", "RESTRICT").
			AddRow("fk_item", "item_id", "items", "id", "SET NULL", "RESTRICT"))

	meta, err := session.TableMetadata(context.Background(), "admdb", "orders")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{
		{Name: "fk_user", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id", OnUpdate: "NO ACTION", OnDelete: "CASCADE"},
		{Name: "fk_item", Column: "item_tenant", ReferencedTable: "items", ReferencedColumn: "tenant", OnUpdate: "SET NULL", OnDelete: "RESTRICT"},
		{Name: "fk_item", Column: "item_id", ReferencedTable: "items", ReferencedColumn: "id", OnUpdate: "SET NULL", OnDelete: "RESTRICT"},
	}, meta.ForeignKeys)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 裸表名按 public 解析。
func TestBareTableNameDefaultsToPublic(t *testing.T) {
	schema, table := splitSchemaTable("events")
	assert.Equal(t, "public", schema)
	assert.Equal(t, "events", table)
}

// 触发器带着所属的表，查源码时靠它定位（PostgreSQL 的触发器名在表内才唯一）。
func TestTriggersCarryTheirTable(t *testing.T) {
	session, mock := newMock(t)
	const systemSchemas = "('pg_catalog','information_schema')"
	mock.ExpectQuery("SELECT table_schema || '.' || table_name FROM information_schema.views WHERE table_schema NOT IN " + systemSchemas + " ORDER BY 1").
		WillReturnRows(names("v", "public.v_users"))
	mock.ExpectQuery("SELECT routine_schema || '.' || routine_name FROM information_schema.routines WHERE routine_schema NOT IN " + systemSchemas + " AND routine_type = 'PROCEDURE' ORDER BY 1").
		WillReturnRows(names("r"))
	mock.ExpectQuery("SELECT routine_schema || '.' || routine_name FROM information_schema.routines WHERE routine_schema NOT IN " + systemSchemas + " AND routine_type = 'FUNCTION' ORDER BY 1").
		WillReturnRows(names("r", "public.f_total"))
	mock.ExpectQuery("SELECT DISTINCT trigger_schema, trigger_name, event_object_table " +
		"FROM information_schema.triggers " +
		"WHERE trigger_schema NOT IN " + systemSchemas + " " +
		"ORDER BY trigger_schema, trigger_name").
		WillReturnRows(sqlmock.NewRows([]string{"s", "n", "t"}).AddRow("public", "trg_audit", "users"))

	objects, err := session.ListObjects(context.Background(), "admdb")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "public.v_users", Type: "view"}}, objects.Views)
	assert.Empty(t, objects.Procedures)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "public.f_total", Type: "function"}}, objects.Functions)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "public.trg_audit", Type: "trigger", Table: "users"}}, objects.Triggers)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestObjectSource(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()

	mock.ExpectQuery(viewDefSQL).
		WithArgs("public", "v_users").WillReturnRows(names("def", "SELECT 1"))
	source, err := session.ObjectSource(ctx, "admdb", "view", "public.v_users", "")
	require.NoError(t, err)
	assert.Equal(t, "SELECT 1", source)

	mock.ExpectQuery("SELECT pg_get_triggerdef(tg.oid, true) "+
		"FROM pg_trigger tg "+
		"JOIN pg_class c ON c.oid = tg.tgrelid "+
		"JOIN pg_namespace n ON n.oid = c.relnamespace "+
		"WHERE n.nspname = $1 AND c.relname = $2 AND tg.tgname = $3 AND NOT tg.tgisinternal "+
		"LIMIT 1").WithArgs("public", "users", "trg_audit").WillReturnRows(names("def", "CREATE TRIGGER trg_audit ..."))
	source, err = session.ObjectSource(ctx, "admdb", "trigger", "public.trg_audit", "users")
	require.NoError(t, err)
	assert.Equal(t, "CREATE TRIGGER trg_audit ...", source)

	mock.ExpectQuery("SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace "+
		"WHERE n.nspname = $1 AND p.proname = $2 LIMIT 1").WithArgs("public", "f_total").WillReturnRows(names("def", "CREATE FUNCTION f_total ..."))
	source, err = session.ObjectSource(ctx, "admdb", "function", "public.f_total", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE FUNCTION f_total ...", source)

	_, err = session.ObjectSource(ctx, "admdb", "sequence", "public.s", "")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}
