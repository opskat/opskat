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
	// inherited 表示列继承自父表（attislocal 为假）。
	inherited bool
}

// ddlRel 是列查询每一行里关于表本身的部分；零值是一张普通的表。
type ddlRel struct {
	kind    string
	comment any
	// partition 表示它是一个分区；parents 是父表（schema 与名字，按继承的次序）的 JSON。
	partition bool
	parents   string
}

func ddlColumnRows(relkind string, tableComment any, columns ...ddlCol) *sqlmock.Rows {
	return ddlRelationRows(ddlRel{kind: relkind, comment: tableComment}, columns...)
}

func ddlRelationRows(rel ddlRel, columns ...ddlCol) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"attname", "format_type", "nullable", "default", "relkind",
		"identity", "start", "increment", "min", "max", "cycle", "generated", "generated_kind",
		"column_comment", "table_comment", "attislocal", "relispartition", "parents"})
	parents := rel.parents
	if parents == "" {
		parents = "[]"
	}
	for _, c := range columns {
		o := c.identityOptions
		rows.AddRow(c.name, c.dataType, c.nullable, c.def, rel.kind, c.identity, o[0], o[1], o[2], o[3],
			c.identityCycle, c.generated, c.generatedKind, c.comment, rel.comment, !c.inherited, rel.partition, parents)
	}
	return rows
}

// partitionRows 是分区表的分区键（pg_get_partkeydef）与分区的分区范围（pg_get_expr(relpartbound)）；nil 是没有。
func partitionRows(key, bound any) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"partkey", "bound"}).AddRow(key, bound)
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

// 分区表：父表写成带分区键的 CREATE TABLE ... PARTITION BY，列与约束在父表上；分区写成 PARTITION OF 父表 FOR VALUES
// （默认分区是 DEFAULT），不写继承来的列与约束，只写它自己的约束与只建在它上面的索引；再分区的分区接着写它自己的
// 分区键。
func TestTableDDLOfAPartitionedTableAndItsPartitions(t *testing.T) {
	inherited := func(name, dataType string) ddlCol { return ddlCol{name: name, dataType: dataType, inherited: true} }
	for _, tc := range []struct {
		name  string
		rel   ddlRel
		cols  []ddlCol
		key   any
		bound any
		cons  [][2]string
		index []string
		want  string
	}{
		{
			name: "readings", rel: ddlRel{kind: "p"}, key: "RANGE (created)",
			cols: []ddlCol{{name: "id", dataType: "integer"}, {name: "created", dataType: "date"}, {name: "region", dataType: "text"}},
			cons: [][2]string{{"readings_pkey", "PRIMARY KEY (id, created, region)"}},
			// 父表上的索引：查询已经去掉 pg_get_indexdef 的 ON ONLY，建出时随之建到每个分区上。
			index: []string{"CREATE INDEX readings_region ON public.readings USING btree (region)"},
			want: "CREATE TABLE \"public\".\"readings\" (\n  \"id\" integer NOT NULL,\n  \"created\" date NOT NULL,\n  \"region\" text NOT NULL,\n" +
				"  CONSTRAINT \"readings_pkey\" PRIMARY KEY (id, created, region)\n) PARTITION BY RANGE (created);\n\n" +
				"CREATE INDEX readings_region ON public.readings USING btree (region);",
		},
		{
			name: "r_2026", rel: ddlRel{kind: "p", partition: true, parents: `[["public", "readings"]]`},
			cols: []ddlCol{inherited("id", "integer"), inherited("created", "date"), inherited("region", "text")},
			key:  "LIST (region)", bound: "FOR VALUES FROM ('2026-01-01') TO ('2027-01-01')",
			index: []string{"CREATE INDEX r_2026_only ON public.r_2026 USING btree (id)"},
			want: "CREATE TABLE \"public\".\"r_2026\" PARTITION OF \"public\".\"readings\" FOR VALUES FROM ('2026-01-01') TO ('2027-01-01') " +
				"PARTITION BY LIST (region);\n\nCREATE INDEX r_2026_only ON public.r_2026 USING btree (id);",
		},
		{
			name: "r_default", rel: ddlRel{kind: "r", partition: true, parents: `[["public", "readings"]]`},
			cols:  []ddlCol{inherited("id", "integer"), inherited("created", "date"), inherited("region", "text")},
			bound: "DEFAULT", cons: [][2]string{{"r_default_id_check", "CHECK ((id > 0))"}},
			want: "CREATE TABLE \"public\".\"r_default\" PARTITION OF \"public\".\"readings\" (\n" +
				"  CONSTRAINT \"r_default_id_check\" CHECK ((id > 0))\n) DEFAULT;",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, mock := newMock(t)
			mock.ExpectQuery(ddlColumnsQuery).WithArgs("public", tc.name).WillReturnRows(ddlRelationRows(tc.rel, tc.cols...))
			mock.ExpectQuery(partitionQuery).WithArgs("public", tc.name).WillReturnRows(partitionRows(tc.key, tc.bound))
			mock.ExpectQuery(ownedSequencesQuery).WithArgs("public", tc.name).WillReturnRows(ownedSequenceRows())
			mock.ExpectQuery(tableConstraintsQuery).WithArgs("public", tc.name).WillReturnRows(constraintRows(tc.cons...))
			mock.ExpectQuery(tableIndexesQuery).WithArgs("public", tc.name).WillReturnRows(names("def", tc.index...))

			ddl, err := session.TableDDL(context.Background(), "appdb", "public."+tc.name)
			require.NoError(t, err)
			assert.Equal(t, tc.want, ddl)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// 继承的子表写成 CREATE TABLE ... (...) INHERITS (父表, ...)：只列它自己的列（继承来的列与约束由父表带来），没有自己的
// 列时是空的括号；父表按继承的次序、带它们的 schema。
func TestTableDDLOfAnInheritedTableListsOnlyItsOwnColumns(t *testing.T) {
	for _, tc := range []struct {
		name, parents string
		cols          []ddlCol
		want          string
	}{
		{
			name: "kid", parents: `[["public", "base"]]`,
			cols: []ddlCol{{name: "id", dataType: "integer", inherited: true}, {name: "extra", dataType: "integer", nullable: true}},
			want: "CREATE TABLE \"public\".\"kid\" (\n  \"extra\" integer\n) INHERITS (\"public\".\"base\");",
		},
		{
			name: "both", parents: `[["public", "base"], ["app", "Other"]]`,
			cols: []ddlCol{{name: "id", dataType: "integer", inherited: true}},
			want: "CREATE TABLE \"public\".\"both\" () INHERITS (\"public\".\"base\", \"app\".\"Other\");",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, mock := newMock(t)
			mock.ExpectQuery(ddlColumnsQuery).WithArgs("public", tc.name).
				WillReturnRows(ddlRelationRows(ddlRel{kind: "r", parents: tc.parents}, tc.cols...))
			mock.ExpectQuery(ownedSequencesQuery).WithArgs("public", tc.name).WillReturnRows(ownedSequenceRows())
			mock.ExpectQuery(tableConstraintsQuery).WithArgs("public", tc.name).WillReturnRows(constraintRows())
			mock.ExpectQuery(tableIndexesQuery).WithArgs("public", tc.name).WillReturnRows(names("def"))

			ddl, err := session.TableDDL(context.Background(), "appdb", "public."+tc.name)
			require.NoError(t, err)
			assert.Equal(t, tc.want, ddl)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
