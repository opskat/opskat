package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ddlColumnRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default"})
}

func ddlColumns() *sqlmock.Rows {
	return ddlColumnRows().
		AddRow("id", "integer", "int4", "NO", "nextval('events_id_seq'::regclass)").
		AddRow("kind", "USER-DEFINED", "event_kind", "NO", nil).
		AddRow("payload", "jsonb", "jsonb", "YES", nil)
}

const wantColumns = "(\n" +
	"  \"id\" integer NOT NULL DEFAULT nextval('events_id_seq'::regclass),\n" +
	"  \"kind\" event_kind NOT NULL,\n" +
	"  \"payload\" jsonb,\n" +
	"  PRIMARY KEY (\"id\")\n);"

// 表名以 "schema.table" 的形式到达：按 schema 与表名分别查 information_schema，建表语句里
// 表名带上它真正的 schema——不是写死的 public。
func TestTableDDLUsesTheTablesRealSchema(t *testing.T) {
	for _, tc := range []struct{ table, schema, name string }{
		{"reporting.events", "reporting", "events"},
		{"public.events", "public", "events"},
		{"events", "public", "events"},
	} {
		t.Run(tc.table, func(t *testing.T) {
			session, mock := newMock(t)
			mock.ExpectQuery(columnsSQL).WithArgs(tc.schema, tc.name).WillReturnRows(ddlColumns())
			mock.ExpectQuery(primaryKeysSQL).WithArgs(tc.schema, tc.name).
				WillReturnRows(sqlmock.NewRows([]string{"column_name"}).AddRow("id"))

			ddl, err := session.TableDDL(context.Background(), "appdb", tc.table)
			require.NoError(t, err)
			assert.Equal(t, `CREATE TABLE "`+tc.schema+`"."`+tc.name+`" `+wantColumns, ddl)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestTableDDLOfAMissingTableIsEmpty(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(columnsSQL).WithArgs("reporting", "gone").WillReturnRows(ddlColumnRows())

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
