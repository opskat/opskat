package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// viewDefSQL 是发给服务端的视图定义语句。format() 的参数是 variadic "any"，PostgreSQL 推不出
// 占位符类型（SQLSTATE 42P18），所以必须显式 ::text。sqlmock 不会做服务端类型推断，
// 这里只能通过精确匹配语句文本来守住这一约束。
const viewDefSQL = "SELECT pg_get_viewdef(format('%I.%I', $1::text, $2::text)::regclass, true)"

// ddlCol 是列查询返回的一行；零值字段是 NULL / false。
type ddlCol struct {
	name, dataType  string
	nullable        bool
	def             any
	identity        any // "ALWAYS" / "BY DEFAULT"
	identityOptions [4]any
	identityCycle   bool
	generated       any // 生成表达式
	generatedKind   any // "s" / "v"
	comment         any
}

func ddlColumnRows(relkind string, tableComment any, columns ...ddlCol) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"attname", "format_type", "nullable", "default", "relkind",
		"identity", "start", "increment", "min", "max", "cycle", "generated", "generated_kind",
		"column_comment", "table_comment"})
	for _, c := range columns {
		o := c.identityOptions
		rows.AddRow(c.name, c.dataType, c.nullable, c.def, relkind, c.identity, o[0], o[1], o[2], o[3],
			c.identityCycle, c.generated, c.generatedKind, c.comment, tableComment)
	}
	return rows
}

func ownedSequenceRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"schema", "name", "column", "data_type", "start", "increment", "min", "max", "cycle"})
}

func constraintRows(defs ...[2]string) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"conname", "def"})
	for _, d := range defs {
		rows.AddRow(d[0], d[1])
	}
	return rows
}

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
			mock.ExpectQuery(ddlColumnsQuery).WithArgs(tc.schema, tc.name).WillReturnRows(ddlColumnRows("r", nil,
				ddlCol{name: "id", dataType: "integer"},
				ddlCol{name: "kind", dataType: "reporting.kind_t", nullable: true}))
			mock.ExpectQuery(ownedSequencesQuery).WithArgs(tc.schema, tc.name).WillReturnRows(ownedSequenceRows())
			mock.ExpectQuery(tableConstraintsQuery).WithArgs(tc.schema, tc.name).
				WillReturnRows(constraintRows([2]string{"events_pkey", "PRIMARY KEY (id)"}))
			mock.ExpectQuery(tableIndexesQuery).WithArgs(tc.schema, tc.name).WillReturnRows(names("def"))

			ddl, err := session.TableDDL(context.Background(), "appdb", tc.table)
			require.NoError(t, err)
			assert.Equal(t, `CREATE TABLE "`+tc.schema+`"."`+tc.name+`" (`+"\n"+
				"  \"id\" integer NOT NULL,\n"+
				"  \"kind\" reporting.kind_t,\n"+
				"  CONSTRAINT \"events_pkey\" PRIMARY KEY (id)\n);", ddl)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// 能在空库上还原：表拥有的序列先建出并在建表后归属到列；身份列带序列参数，生成列带表达式与存储方式；
// 约束（主键、唯一、CHECK）按服务端给出的定义写进建表语句；非约束索引与注释各自单独一条。
func TestTableDDLIsRestorable(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("reporting", "events").WillReturnRows(ddlColumnRows("r", "it's events",
		ddlCol{name: "id", dataType: "integer", def: "nextval('reporting.events_id_seq'::regclass)"},
		ddlCol{name: "ident", dataType: "bigint", identity: "ALWAYS",
			identityOptions: [4]any{"10", "5", "1", "9223372036854775807"}},
		ddlCol{name: "seq", dataType: "smallint", identity: "BY DEFAULT",
			identityOptions: [4]any{"1", "1", "1", "32767"}, identityCycle: true},
		ddlCol{name: "Mixed Code", dataType: "character varying(20)", comment: "the code"},
		ddlCol{name: "price", dataType: "numeric(12,2)", nullable: true, def: "0"},
		ddlCol{name: "total", dataType: "numeric", nullable: true, generated: "(price * (2)::numeric)", generatedKind: "s"},
		ddlCol{name: "doubled", dataType: "integer", nullable: true, generated: "(id * 2)", generatedKind: "v"},
		ddlCol{name: "note", dataType: "text", nullable: true, comment: ""}))
	mock.ExpectQuery(ownedSequencesQuery).WithArgs("reporting", "events").WillReturnRows(ownedSequenceRows().
		AddRow("reporting", "events_id_seq", "id", "integer", "1", "1", "1", "2147483647", false).
		AddRow("audit", "Big Seq", "note", "bigint", "100", "-1", "-9223372036854775808", "100", true))
	mock.ExpectQuery(tableConstraintsQuery).WithArgs("reporting", "events").WillReturnRows(constraintRows(
		[2]string{"events_pkey", "PRIMARY KEY (id)"},
		[2]string{"events_Mixed Code_key", `UNIQUE ("Mixed Code")`},
		[2]string{"events_price_check", "CHECK ((price >= (0)::numeric))"}))
	mock.ExpectQuery(tableIndexesQuery).WithArgs("reporting", "events").WillReturnRows(names("def",
		"CREATE INDEX events_happened ON reporting.events USING btree (price DESC)",
		"CREATE INDEX events_note_lower ON reporting.events USING btree (lower(note)) WHERE (note IS NOT NULL)"))

	ddl, err := session.TableDDL(context.Background(), "appdb", "reporting.events")
	require.NoError(t, err)
	assert.Equal(t, `CREATE SEQUENCE "reporting"."events_id_seq" AS integer START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 2147483647;

CREATE SEQUENCE "audit"."Big Seq" START WITH 100 INCREMENT BY -1 MINVALUE -9223372036854775808 MAXVALUE 100 CYCLE;

CREATE TABLE "reporting"."events" (
  "id" integer NOT NULL DEFAULT nextval('reporting.events_id_seq'::regclass),
  "ident" bigint NOT NULL GENERATED ALWAYS AS IDENTITY (START WITH 10 INCREMENT BY 5 MINVALUE 1 MAXVALUE 9223372036854775807),
  "seq" smallint NOT NULL GENERATED BY DEFAULT AS IDENTITY (START WITH 1 INCREMENT BY 1 MINVALUE 1 MAXVALUE 32767 CYCLE),
  "Mixed Code" character varying(20) NOT NULL,
  "price" numeric(12,2) DEFAULT 0,
  "total" numeric GENERATED ALWAYS AS ((price * (2)::numeric)) STORED,
  "doubled" integer GENERATED ALWAYS AS ((id * 2)) VIRTUAL,
  "note" text,
  CONSTRAINT "events_pkey" PRIMARY KEY (id),
  CONSTRAINT "events_Mixed Code_key" UNIQUE ("Mixed Code"),
  CONSTRAINT "events_price_check" CHECK ((price >= (0)::numeric))
);

ALTER SEQUENCE "reporting"."events_id_seq" OWNED BY "reporting"."events"."id";

ALTER SEQUENCE "audit"."Big Seq" OWNED BY "reporting"."events"."note";

CREATE INDEX events_happened ON reporting.events USING btree (price DESC);

CREATE INDEX events_note_lower ON reporting.events USING btree (lower(note)) WHERE (note IS NOT NULL);

COMMENT ON TABLE "reporting"."events" IS 'it''s events';

COMMENT ON COLUMN "reporting"."events"."Mixed Code" IS 'the code';`, ddl)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 视图（任一 schema）输出 CREATE VIEW，不是 CREATE TABLE；不查序列、约束与索引。
func TestTableDDLOfAViewIsItsDefinition(t *testing.T) {
	for _, tc := range []struct{ table, schema, name string }{
		{"public.v_users", "public", "v_users"},
		{"reporting.v_events", "reporting", "v_events"},
	} {
		t.Run(tc.table, func(t *testing.T) {
			session, mock := newMock(t)
			mock.ExpectQuery(ddlColumnsQuery).WithArgs(tc.schema, tc.name).WillReturnRows(ddlColumnRows("v", nil,
				ddlCol{name: "id", dataType: "integer", nullable: true},
				ddlCol{name: "kind", dataType: "reporting.kind_t", nullable: true}))
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
	mock.ExpectQuery(ddlColumnsQuery).WithArgs("reporting", "gone").WillReturnRows(ddlColumnRows("r", nil))

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
