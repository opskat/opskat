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
	mock.ExpectQuery("SELECT i.relname, ix.indisunique, pg_get_indexdef(ix.indexrelid, s.pos::int, true) "+
		"FROM pg_class t "+
		"JOIN pg_namespace n ON n.oid = t.relnamespace "+
		"JOIN pg_index ix ON ix.indrelid = t.oid "+
		"JOIN pg_class i ON i.oid = ix.indexrelid "+
		"CROSS JOIN LATERAL generate_series(1, ix.indnatts) AS s(pos) "+
		"WHERE n.nspname = $1 AND t.relname = $2 "+
		"ORDER BY i.relname, s.pos").
		WithArgs("reporting", "events").WillReturnRows(
		sqlmock.NewRows([]string{"relname", "indisunique", "def"}).AddRow("events_pkey", true, "id"))
	mock.ExpectQuery("SELECT tc.constraint_name, kcu.column_name, ccu.table_name, ccu.column_name "+
		"FROM information_schema.table_constraints tc "+
		"JOIN information_schema.key_column_usage kcu ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema "+
		"JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema "+
		"WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = $1 AND tc.table_name = $2 "+
		"ORDER BY tc.constraint_name").
		WithArgs("reporting", "events").WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c", "d"}))

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
