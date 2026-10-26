package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// pgColumn 是改表时读列定义的一行；nil 的 def 是 NULL（没有默认值）。
type pgColumn struct {
	name, dataType, udtName, nullable string
	def                               any
	comment                           string
}

func expectAlterTable(mock sqlmock.Sqlmock, schema, table, tableComment string, columns ...pgColumn) {
	rows := sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default", "column_comment"})
	for _, c := range columns {
		rows.AddRow(c.name, c.dataType, c.udtName, c.nullable, c.def, c.comment)
	}
	mock.ExpectQuery(alterColumnsSQL).WithArgs(schema, table).WillReturnRows(rows)
	mock.ExpectQuery(alterTableCommentSQL).WithArgs(schema, table).
		WillReturnRows(sqlmock.NewRows([]string{"table_comment"}).AddRow(tableComment))
}

// 任一 schema 的表都按它所在的 schema 读出全部列（改动前写死 public、又拿 schema.table 去匹配表名，
// 一列也读不到）；改名输入框里是不带 schema 的表名；自定义类型显示类型名。
func TestAlterTableModelLoadsColumnsOfATableInAnySchema(t *testing.T) {
	session, mock := newMock(t)
	expectAlterTable(mock, "reporting", "events", "event log",
		pgColumn{name: "id", dataType: "integer", udtName: "int4", nullable: "NO", def: "nextval('reporting.events_id_seq'::regclass)"},
		pgColumn{name: "status", dataType: "USER-DEFINED", udtName: "event_status", nullable: "YES", def: "'new'::event_status", comment: "state"},
		pgColumn{name: "payload", dataType: "jsonb", udtName: "jsonb", nullable: "YES"},
	)

	model, err := session.AlterTableModel(context.Background(), "app", "reporting.events")
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableModel{
		Name:    "events",
		Comment: "event log",
		Columns: []dbdriver.ColumnDefinition{
			{Name: "id", Type: "integer", DefaultValue: "nextval('reporting.events_id_seq'::regclass)"},
			{Name: "status", Type: "event_status", Nullable: true, DefaultValue: "'new'::event_status", Comment: "state"},
			{Name: "payload", Type: "jsonb", Nullable: true},
		},
	}, model)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改名、新增、修改与表 / 列注释：与改动前前端生成的文本逐字一致（原 AlterTableDialog.test.ts 的断言）。
func TestAlterTableStatementsForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	expectAlterTable(mock, "public", "users", "old table comment",
		pgColumn{name: "name", dataType: "text", udtName: "text", nullable: "YES", comment: "old comment"},
	)

	result, err := session.AlterTableStatements(context.Background(), "appdb", "users", dbdriver.AlterTableRequest{
		Name:    "users_v2",
		Comment: "new table comment",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "name", Name: "full_name", Type: "varchar(120)", Nullable: false, DefaultValue: "anonymous", Comment: "full name comment"},
			{Name: "age", Type: "integer", Nullable: true, Comment: "age in years"},
			{Name: "tag", Type: "bytea", Nullable: true, DefaultValue: `\x41`},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{
		Statements: []string{
			`ALTER TABLE "users" RENAME TO "users_v2"`,
			`ALTER TABLE "users_v2" ADD COLUMN "age" integer`,
			`ALTER TABLE "users_v2" ADD COLUMN "tag" bytea DEFAULT '\x41'`,
			`ALTER TABLE "users_v2" RENAME COLUMN "name" TO "full_name"`,
			`ALTER TABLE "users_v2" ALTER COLUMN "full_name" TYPE varchar(120)`,
			`ALTER TABLE "users_v2" ALTER COLUMN "full_name" SET NOT NULL`,
			`ALTER TABLE "users_v2" ALTER COLUMN "full_name" SET DEFAULT 'anonymous'`,
			`COMMENT ON TABLE "users_v2" IS 'new table comment'`,
			`COMMENT ON COLUMN "users_v2"."full_name" IS 'full name comment'`,
			`COMMENT ON COLUMN "users_v2"."age" IS 'age in years'`,
		},
		Table: "users_v2",
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改名只给不带 schema 的新名字：生成 RENAME TO "users_v2"（改动前是 "public.users_v2"），表仍在原
// schema，之后的语句与返回的表名都带着它；清空的默认值与注释写成 DROP DEFAULT / IS NULL。
func TestAlterTableRenameKeepsTheTableInItsSchema(t *testing.T) {
	session, mock := newMock(t)
	expectAlterTable(mock, "public", "users", "t",
		pgColumn{name: "n", dataType: "text", udtName: "text", nullable: "NO", def: "'x'::text", comment: "c"},
	)

	result, err := session.AlterTableStatements(context.Background(), "appdb", "public.users", dbdriver.AlterTableRequest{
		Name:    "users_v2",
		Comment: " ",
		Columns: []dbdriver.AlterColumn{{OriginalName: "n", Name: "n", Type: "text", Nullable: true}},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{
		Statements: []string{
			`ALTER TABLE "public"."users" RENAME TO "users_v2"`,
			`ALTER TABLE "public"."users_v2" ALTER COLUMN "n" DROP NOT NULL`,
			`ALTER TABLE "public"."users_v2" ALTER COLUMN "n" DROP DEFAULT`,
			`COMMENT ON TABLE "public"."users_v2" IS NULL`,
			`COMMENT ON COLUMN "public"."users_v2"."n" IS NULL`,
		},
		Table: "public.users_v2",
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}
