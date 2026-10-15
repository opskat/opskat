package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 建表语句的列查询：类型由 format_type 给出（PostgreSQL 自己的完整写法，pg_catalog 之外的类型
// 带 schema），relkind 区分表与视图。
const ddlColumnsSQL = "SELECT a.attname, " +
	"CASE WHEN tn.nspname = 'pg_catalog' OR NOT pg_type_is_visible(a.atttypid) " +
	"THEN format_type(a.atttypid, a.atttypmod) " +
	"ELSE quote_ident(tn.nspname) || '.' || format_type(a.atttypid, a.atttypmod) END, " +
	"NOT a.attnotnull, col.column_default, c.relkind::text " +
	"FROM pg_class c " +
	"JOIN pg_namespace n ON n.oid = c.relnamespace " +
	"JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped " +
	"JOIN pg_type t ON t.oid = a.atttypid " +
	"JOIN pg_namespace tn ON tn.oid = t.typnamespace " +
	"LEFT JOIN information_schema.columns col " +
	"ON col.table_schema = n.nspname AND col.table_name = c.relname AND col.column_name = a.attname " +
	"WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'v') " +
	"ORDER BY a.attnum"

const viewDefSQL = "SELECT pg_get_viewdef(format('%I.%I', $1, $2)::regclass, true)"

func ddlColumnRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"attname", "format_type", "nullable", "default", "relkind"})
}

func ddlColumns() *sqlmock.Rows {
	return ddlColumnRows().
		AddRow("id", "integer", false, "nextval('events_id_seq'::regclass)", "r").
		AddRow("kind", "reporting.kind_t", false, nil, "r").
		AddRow("payload", "jsonb", true, nil, "r")
}

const wantColumns = "(\n" +
	"  \"id\" integer NOT NULL DEFAULT nextval('events_id_seq'::regclass),\n" +
	"  \"kind\" reporting.kind_t NOT NULL,\n" +
	"  \"payload\" jsonb,\n" +
	"  PRIMARY KEY (\"id\")\n);"

// 表名以 "schema.table" 的形式到达：按 schema 与表名查目录，建表语句里表名带上它真正的
// schema——不是写死的 public。
func TestTableDDLUsesTheTablesRealSchema(t *testing.T) {
	for _, tc := range []struct{ table, schema, name string }{
		{"reporting.events", "reporting", "events"},
		{"public.events", "public", "events"},
		{"events", "public", "events"},
	} {
		t.Run(tc.table, func(t *testing.T) {
			session, mock := newMock(t)
			mock.ExpectQuery(ddlColumnsSQL).WithArgs(tc.schema, tc.name).WillReturnRows(ddlColumns())
			mock.ExpectQuery(primaryKeysSQL).WithArgs(tc.schema, tc.name).
				WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))

			ddl, err := session.TableDDL(context.Background(), "appdb", tc.table)
			require.NoError(t, err)
			assert.Equal(t, `CREATE TABLE "`+tc.schema+`"."`+tc.name+`" `+wantColumns, ddl)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// 视图（任一 schema）输出 CREATE VIEW，不是 CREATE TABLE；不查主键。
func TestTableDDLOfAViewIsItsDefinition(t *testing.T) {
	for _, tc := range []struct{ table, schema, name string }{
		{"public.v_users", "public", "v_users"},
		{"reporting.v_events", "reporting", "v_events"},
	} {
		t.Run(tc.table, func(t *testing.T) {
			session, mock := newMock(t)
			mock.ExpectQuery(ddlColumnsSQL).WithArgs(tc.schema, tc.name).WillReturnRows(ddlColumnRows().
				AddRow("id", "integer", true, nil, "v").
				AddRow("kind", "reporting.kind_t", true, nil, "v"))
			mock.ExpectQuery(viewDefSQL).WithArgs(tc.schema, tc.name).
				WillReturnRows(names("def", " SELECT events.id,\n    events.kind\n   FROM reporting.events;"))

			ddl, err := session.TableDDL(context.Background(), "appdb", tc.table)
			require.NoError(t, err)
			assert.Equal(t, `CREATE VIEW "`+tc.schema+`"."`+tc.name+`" AS SELECT events.id,`+"\n"+
				"    events.kind\n   FROM reporting.events;", ddl)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestTableDDLOfAMissingTableIsEmpty(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsSQL).WithArgs("reporting", "gone").WillReturnRows(ddlColumnRows())

	ddl, err := session.TableDDL(context.Background(), "appdb", "reporting.gone")
	require.NoError(t, err)
	assert.Empty(t, ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStarterQuery(t *testing.T) {
	session, _ := newMock(t)
	got, err := session.StarterQuery(context.Background(), "appdb", "reporting.events", 100)
	require.NoError(t, err)
	assert.Equal(t, `SELECT * FROM "reporting"."events" LIMIT 100`, got)
}
