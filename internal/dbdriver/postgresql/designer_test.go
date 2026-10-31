package postgresql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// indexRow 是 indexesSQL 的一行（索引的一列）。column 为空是表达式键（text 是表达式）。
type indexRow struct {
	name                         string
	primary, unique              bool
	method, constraint           string
	deferrable, include          bool
	column, text                 string
	option                       int
	otherOpclass, otherCollation bool
	predicate, comment           string
	storage                      bool
	definition                   string
}

// keyRow 是普通 btree 索引的一个升序键列。
func keyRow(name, column string) indexRow {
	return indexRow{name: name, method: "btree", column: column, text: column}
}

// primaryKeyRow 是主键约束的一个键列。
func primaryKeyRow(name, column string) indexRow {
	row := keyRow(name, column)
	row.primary, row.unique, row.constraint = true, true, "p"
	return row
}

func expectIndexes(mock sqlmock.Sqlmock, schema, table string, parts ...indexRow) {
	rows := sqlmock.NewRows([]string{
		"index_name", "is_primary", "is_unique", "method", "constraint_type", "is_deferrable", "is_include",
		"column_name", "column_text", "column_option", "default_opclass", "other_collation", "predicate",
		"index_comment", "has_storage", "index_def",
	})
	for _, p := range parts {
		rows.AddRow(p.name, p.primary, p.unique, p.method, p.constraint, p.deferrable, p.include,
			p.column, p.text, p.option, !p.otherOpclass, p.otherCollation, p.predicate,
			p.comment, p.storage, p.definition)
	}
	mock.ExpectQuery(indexesSQL).WithArgs(schema, table).WillReturnRows(rows)
}

func expectTakenIndexNames(mock sqlmock.Sqlmock, schema, table string, taken ...string) {
	mock.ExpectQuery(takenIndexNamesSQL).WithArgs(schema, table).WillReturnRows(names("relname", taken...))
}

// expectIndexConstraints 是改表写索引语句前查的约束：名字 → 种类（p / u / x）。
func expectIndexConstraints(mock sqlmock.Sqlmock, schema, table string, constraints map[string]string) {
	rows := sqlmock.NewRows([]string{"conname", "contype"})
	for name, kind := range constraints {
		rows.AddRow(name, kind)
	}
	mock.ExpectQuery(indexConstraintsSQL).WithArgs(schema, table).WillReturnRows(rows)
}

// expectDesignedTable 同 expectAlterTable，但表带着索引与 schema 里已占用的名字。
func expectDesignedTable(mock sqlmock.Sqlmock, schema, table string, columns []pgColumn, indexes []indexRow, taken ...string) {
	rows := sqlmock.NewRows([]string{"column_name", "data_type", "udt_name", "is_nullable", "column_default", "column_comment", "is_identity"})
	for _, c := range columns {
		identity := c.identity
		if identity == "" {
			identity = "NO"
		}
		rows.AddRow(c.name, c.dataType, c.udtName, c.nullable, c.def, c.comment, identity)
	}
	mock.ExpectQuery(alterColumnsSQL).WithArgs(schema, table).WillReturnRows(rows)
	mock.ExpectQuery(alterTableCommentSQL).WithArgs(schema, table).
		WillReturnRows(sqlmock.NewRows([]string{"table_comment"}).AddRow(""))
	expectIndexes(mock, schema, table, indexes...)
	expectTakenIndexNames(mock, schema, table, taken...)
}

var usersColumns = []pgColumn{
	{name: "id", dataType: "bigint", udtName: "int8", nullable: "NO", identity: "YES"},
	{name: "tenant_id", dataType: "integer", udtName: "int4", nullable: "NO"},
	{name: "name", dataType: "text", udtName: "text", nullable: "YES"},
	{name: "email", dataType: "text", udtName: "text", nullable: "YES"},
}

// usersIndexes 是 public.users 的索引，按 indexesSQL 的次序（主键在前，其余按名字）。
var usersIndexes = []indexRow{
	primaryKeyRow("users_pkey", "id"),
	{name: "idx_hash", method: "hash", column: "name", text: "name"},
	{name: "idx_name", method: "btree", column: "name", text: "name", option: 3, comment: "by name"},
	keyRow("idx_name", "tenant_id"),
	{name: "users_email_key", unique: true, method: "btree", constraint: "u", column: "email", text: "email"},
}

func method(value string) map[string]any { return map[string]any{"method": value} }

func usersIndexDefinitions() []dbdriver.IndexDefinition {
	return []dbdriver.IndexDefinition{
		{Name: "users_pkey", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Options: method("btree")},
		{Name: "idx_hash", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "name"}}, Options: method("hash")},
		{Name: "idx_name", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "name", Descending: true}, {Name: "tenant_id"}},
			Options: method("btree"), Comment: "by name"},
		{Name: "users_email_key", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "email"}}, Options: method("btree")},
	}
}

// keepIndexes 把读出的索引原样交回（改表请求里的「保留」）。
func keepIndexes(indexes []dbdriver.IndexDefinition) []dbdriver.AlterIndex {
	out := make([]dbdriver.AlterIndex, len(indexes))
	for i, index := range indexes {
		out[i] = dbdriver.AlterIndex{OriginalName: index.Name, IndexDefinition: index}
	}
	return out
}

// keepColumns 把读出的列原样交回。
func keepColumns(columns []dbdriver.ColumnDefinition) []dbdriver.AlterColumn {
	out := make([]dbdriver.AlterColumn, len(columns))
	for i, c := range columns {
		out[i] = dbdriver.AlterColumn{
			OriginalName: c.Name, Name: c.Name, Type: c.Type, Nullable: c.Nullable, DefaultValue: c.DefaultValue,
			Comment: c.Comment, DefaultKind: c.DefaultKind,
		}
	}
	return out
}

func usersRequest() dbdriver.AlterTableRequest {
	return dbdriver.AlterTableRequest{
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "id", Name: "id", Type: "bigint", DefaultKind: dbdriver.DefaultKindNone},
			{OriginalName: "tenant_id", Name: "tenant_id", Type: "integer", DefaultKind: dbdriver.DefaultKindNone},
			{OriginalName: "name", Name: "name", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
			{OriginalName: "email", Name: "email", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
		},
		Indexes: keepIndexes(usersIndexDefinitions()),
	}
}

// 已有列的默认值按 column_default 判定写法：没有默认值（IDENTITY 列也没有）是「无」；NULL 与 NULL::类型是 NULL；
// 字符串常量去掉类型转换与引号（成对的单引号还原）、数字与布尔是值（负数在目录里是 '-1'::integer）；函数、序列与运算是
// 表达式。IDENTITY 列与默认值取序列的 serial 列自增。
func TestAlterTableModelClassifiesDefaultsForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "events", []pgColumn{
		{name: "id", dataType: "bigint", nullable: "NO", identity: "YES"},
		{name: "sid", dataType: "integer", nullable: "NO", def: "nextval('events_sid_seq'::regclass)"},
		{name: "note", dataType: "text", nullable: "YES"},
		{name: "code", dataType: "character varying", nullable: "YES", def: "'it''s'::character varying"},
		{name: "empty", dataType: "text", nullable: "YES", def: "''::text"},
		{name: "created", dataType: "timestamp with time zone", nullable: "YES", def: "now()"},
		{name: "neg", dataType: "integer", nullable: "YES", def: "'-1'::integer"},
		{name: "amount", dataType: "numeric", nullable: "YES", def: "0.00"},
		{name: "flag", dataType: "boolean", nullable: "YES", def: "true"},
		{name: "nul", dataType: "character varying", nullable: "YES", def: "NULL::character varying"},
		{name: "u", dataType: "uuid", nullable: "YES", def: "gen_random_uuid()"},
		{name: "arr", dataType: "ARRAY", nullable: "YES", def: "'{1,2}'::integer[]"},
		{name: "bits", dataType: "bit", nullable: "YES", def: `'101'::"bit"`},
		{name: "expr", dataType: "text", nullable: "YES", def: "('a'::text || 'b'::text)"},
	}, nil)

	model, err := session.AlterTableModel(context.Background(), "app", "events")
	require.NoError(t, err)
	type kind struct {
		kind          dbdriver.DefaultKind
		value         string
		autoIncrement bool
	}
	got := map[string]kind{}
	for _, column := range model.Columns {
		got[column.Name] = kind{column.DefaultKind, column.DefaultValue, column.AutoIncrement}
	}
	assert.Equal(t, map[string]kind{
		"id":      {dbdriver.DefaultKindNone, "", true},
		"sid":     {dbdriver.DefaultKindExpression, "nextval('events_sid_seq'::regclass)", true},
		"note":    {dbdriver.DefaultKindNone, "", false},
		"code":    {dbdriver.DefaultKindValue, "it's", false},
		"empty":   {dbdriver.DefaultKindValue, "", false},
		"created": {dbdriver.DefaultKindExpression, "now()", false},
		"neg":     {dbdriver.DefaultKindValue, "-1", false},
		"amount":  {dbdriver.DefaultKindValue, "0.00", false},
		"flag":    {dbdriver.DefaultKindValue, "true", false},
		"nul":     {dbdriver.DefaultKindNull, "", false},
		"u":       {dbdriver.DefaultKindExpression, "gen_random_uuid()", false},
		"arr":     {dbdriver.DefaultKindValue, "{1,2}", false},
		"bits":    {dbdriver.DefaultKindValue, "101", false},
		"expr":    {dbdriver.DefaultKindExpression, "('a'::text || 'b'::text)", false},
	}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改表模型带上索引（主键在前）：类型、键列（降序）、方法与注释；schema 里已占用的名字（其他表的索引、表、
// 序列……）供重名校验。设计器写不回原样的索引只读并说明原因：表达式、部分、INCLUDE、非默认操作符类 / 排序规则 /
// 空值次序、不提供的方法、存储参数、NULLS NOT DISTINCT、可延迟约束与排除约束。
func TestAlterTableModelReadsIndexesForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	indexes := append(append([]indexRow{}, usersIndexes...),
		indexRow{name: "ro_collation", method: "btree", column: "name", text: "name", otherCollation: true},
		indexRow{name: "ro_cover", method: "btree", column: "name", text: "name"},
		indexRow{name: "ro_cover", method: "btree", include: true, column: "email", text: "email"},
		indexRow{name: "ro_deferrable", unique: true, method: "btree", constraint: "u", deferrable: true, column: "tenant_id", text: "tenant_id"},
		indexRow{name: "ro_exclude", method: "gist", constraint: "x", column: "name", text: "name"},
		indexRow{name: "ro_expr", method: "btree", text: "lower(email)"},
		indexRow{name: "ro_method", method: "bloom", column: "name", text: "name"},
		indexRow{name: "ro_nnd", unique: true, method: "btree", column: "email", text: "email",
			definition: "CREATE UNIQUE INDEX ro_nnd ON public.users USING btree (email) NULLS NOT DISTINCT"},
		indexRow{name: "ro_nulls", method: "btree", column: "name", text: "name", option: 2},
		indexRow{name: "ro_ops", method: "btree", column: "name", text: "name", otherOpclass: true},
		indexRow{name: "ro_partial", method: "btree", column: "name", text: "name", predicate: "tenant_id > 0"},
		indexRow{name: "ro_storage", method: "btree", column: "name", text: "name", storage: true},
	)
	expectDesignedTable(mock, "public", "users", usersColumns, indexes, "orders", "orders_pkey", "users", "users_id_seq")

	model, err := session.AlterTableModel(context.Background(), "app", "users")
	require.NoError(t, err)
	readOnly := func(name, column, reason string) dbdriver.IndexDefinition {
		return dbdriver.IndexDefinition{Name: name, Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: column}},
			Options: method("btree"), ReadOnlyReason: reason}
	}
	cover := readOnly("ro_cover", "name", "It has INCLUDE columns (email), which the designer cannot recreate.")
	cover.Include = []string{"email"}
	deferrable := readOnly("ro_deferrable", "tenant_id", readOnlyDeferrable)
	deferrable.Type = dbdriver.IndexTypeUnique
	exclude := readOnly("ro_exclude", "name", readOnlyExclusion)
	exclude.Options = method("gist")
	expression := readOnly("ro_expr", "lower(email)", readOnlyExpression)
	expression.Columns[0].Expression = true
	bloom := readOnly("ro_method", "name", `It uses the index method "bloom", which the designer does not offer.`)
	bloom.Options = method("bloom")
	nnd := readOnly("ro_nnd", "email", readOnlyNullsNotDistinct)
	nnd.Type = dbdriver.IndexTypeUnique
	assert.Equal(t, append(usersIndexDefinitions(),
		readOnly("ro_collation", "name", readOnlyCollation),
		cover, deferrable, exclude, expression, bloom, nnd,
		readOnly("ro_nulls", "name", readOnlyNullsOrder),
		readOnly("ro_ops", "name", readOnlyOperatorClass),
		readOnly("ro_partial", "name", "It is a partial index (WHERE tenant_id > 0), which the designer cannot recreate."),
		readOnly("ro_storage", "name", readOnlyStorage),
	), model.Indexes)
	assert.Equal(t, []string{"orders", "orders_pkey", "users", "users_id_seq"}, model.TakenIndexNames)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 读出的列与索引原样交回不产生语句，也不必再查约束。
func TestAlterTableRoundTripGeneratesNothingForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	model, err := session.AlterTableModel(context.Background(), "app", "public.users")
	require.NoError(t, err)

	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", dbdriver.AlterTableRequest{
		Name: model.Name, Comment: model.Comment, Columns: keepColumns(model.Columns), Indexes: keepIndexes(model.Indexes),
	})
	require.NoError(t, err)
	assert.Empty(t, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 新索引（降序、方法、注释）建在新增的列之后：CREATE INDEX 之后一条 COMMENT ON INDEX，索引名带表的 schema；
// 没有现有索引的改动，不查约束。
func TestAlterTableAddsAnIndexOnANewColumnForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)

	req := usersRequest()
	req.Columns = append(req.Columns, dbdriver.AlterColumn{Name: "code", Type: "varchar(20)", Nullable: true})
	req.Indexes = append(req.Indexes, dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
		Name: "idx_users_code", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "code", Descending: true}},
		Options: method("btree"), Comment: "by code",
	}}, dbdriver.AlterIndex{IndexDefinition: dbdriver.IndexDefinition{
		Name: "uq_users_code", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "code"}, {Name: "tenant_id"}},
	}})
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`ALTER TABLE "public"."users" ADD COLUMN "code" varchar(20)`,
		`CREATE INDEX "idx_users_code" ON "public"."users" USING btree ("code" DESC)`,
		`CREATE UNIQUE INDEX "uq_users_code" ON "public"."users" ("code", "tenant_id")`,
		`COMMENT ON INDEX "public"."idx_users_code" IS 'by code'`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改动的索引删在列的改动之前、重建在之后（改名的列已是新名字），原有的注释重建后写回；表名不带 schema 时
// 索引名也不带。
func TestAlterTableRebuildsAChangedIndexForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	expectIndexConstraints(mock, "public", "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})

	req := usersRequest()
	req.Columns[2].Name = "full_name"
	req.Indexes[2].Columns = []dbdriver.IndexColumn{{Name: "full_name", Descending: true}}
	req.Indexes[1].Columns = []dbdriver.IndexColumn{{Name: "full_name"}}
	result, err := session.AlterTableStatements(context.Background(), "app", "users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`DROP INDEX "idx_name"`,
		`ALTER TABLE "users" RENAME COLUMN "name" TO "full_name"`,
		`CREATE INDEX "idx_name" ON "users" USING btree ("full_name" DESC)`,
		`COMMENT ON INDEX "idx_name" IS 'by name'`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改主键：随约束删（DROP CONSTRAINT）再以同名约束加回；改唯一约束的索引同样随约束删、改完仍能写成唯一约束的写回
// 唯一约束。
func TestAlterTableChangesConstraintIndexesForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	expectIndexConstraints(mock, "public", "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})

	req := usersRequest()
	req.Indexes[0].Columns = []dbdriver.IndexColumn{{Name: "tenant_id"}, {Name: "id"}}
	req.Indexes[3].Columns = []dbdriver.IndexColumn{{Name: "email"}, {Name: "tenant_id"}}
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`ALTER TABLE "public"."users" DROP CONSTRAINT "users_pkey"`,
		`ALTER TABLE "public"."users" DROP CONSTRAINT "users_email_key"`,
		`ALTER TABLE "public"."users" ADD CONSTRAINT "users_pkey" PRIMARY KEY ("tenant_id", "id")`,
		`ALTER TABLE "public"."users" ADD CONSTRAINT "users_email_key" UNIQUE ("email", "tenant_id")`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 唯一约束的索引改成降序就写不成约束了：随约束删、建成唯一索引。
func TestAlterTableRebuildsAUniqueConstraintAsAnIndexWhenItNoLongerFits(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	expectIndexConstraints(mock, "public", "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})

	req := usersRequest()
	req.Indexes[3].Columns = []dbdriver.IndexColumn{{Name: "email", Descending: true}}
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`ALTER TABLE "public"."users" DROP CONSTRAINT "users_email_key"`,
		`CREATE UNIQUE INDEX "users_email_key" ON "public"."users" USING btree ("email" DESC)`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 删除支撑唯一约束的索引是删约束，普通索引是 DROP INDEX。
func TestAlterTableDropsAConstraintBackedIndexWithItsConstraint(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	expectIndexConstraints(mock, "public", "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})

	req := usersRequest()
	req.Indexes = req.Indexes[:3]
	req.Indexes = append(req.Indexes[:1], req.Indexes[2:]...)
	req.IndexDrops = []string{"users_email_key", "idx_hash"}
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`ALTER TABLE "public"."users" DROP CONSTRAINT "users_email_key"`,
		`DROP INDEX "public"."idx_hash"`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 索引名带上表所在的 schema，schema 名里的引号照样成对转义。
func TestAlterTableQualifiesIndexNamesWithAnEscapedSchema(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, `we"ird`, "users", usersColumns, usersIndexes)
	expectIndexConstraints(mock, `we"ird`, "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})

	req := usersRequest()
	req.Indexes = append(req.Indexes[:1], req.Indexes[2:]...)
	req.IndexDrops = []string{"idx_hash"}
	result, err := session.AlterTableStatements(context.Background(), "app", `we"ird.users`, req)
	require.NoError(t, err)
	assert.Equal(t, []string{`DROP INDEX "we""ird"."idx_hash"`}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 删一列时，引用它的索引随列一起删，删索引在 DROP COLUMN 之前。
func TestAlterTableDropsIndexesWithTheirColumnFirst(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	expectIndexConstraints(mock, "public", "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})

	req := usersRequest()
	req.Columns = append(req.Columns[:2], req.Columns[3])
	req.Drops = []string{"name"}
	req.Indexes = []dbdriver.AlterIndex{req.Indexes[0], req.Indexes[3]}
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`DROP INDEX "public"."idx_hash"`,
		`DROP INDEX "public"."idx_name"`,
		`ALTER TABLE "public"."users" DROP COLUMN "name"`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 只读索引不能改（ErrUnsupported，原因原样给出），只能删除。
func TestAlterTableNeverRebuildsAReadOnlyIndex(t *testing.T) {
	partial := indexRow{name: "ro_partial", method: "btree", column: "name", text: "name", predicate: "tenant_id > 0"}
	indexes := append(append([]indexRow{}, usersIndexes...), partial)

	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, indexes)
	model, err := session.AlterTableModel(context.Background(), "app", "public.users")
	require.NoError(t, err)

	expectDesignedTable(mock, "public", "users", usersColumns, indexes)
	req := usersRequest()
	changed := model.Indexes[4]
	changed.Columns = []dbdriver.IndexColumn{{Name: "email"}}
	req.Indexes = append(req.Indexes, dbdriver.AlterIndex{OriginalName: "ro_partial", IndexDefinition: changed})
	_, err = session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	assert.Contains(t, err.Error(), "partial index")

	expectDesignedTable(mock, "public", "users", usersColumns, indexes)
	expectIndexConstraints(mock, "public", "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})
	req = usersRequest()
	req.IndexDrops = []string{"ro_partial"}
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{`DROP INDEX "public"."ro_partial"`}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 只改名字或注释的索引不重建：ALTER INDEX … RENAME TO（约束的索引改名，约束随之改名）与 COMMENT ON INDEX。
func TestAlterTableRenamesAndCommentsAnIndexWithoutRebuilding(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	expectIndexConstraints(mock, "public", "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})

	req := usersRequest()
	req.Indexes[1].Name = "idx_users_name_hash"
	req.Indexes[2].Comment = ""
	req.Indexes[3].Name = "uq_users_email"
	req.Indexes[3].Comment = "one per user"
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`ALTER INDEX "public"."idx_hash" RENAME TO "idx_users_name_hash"`,
		`ALTER INDEX "public"."users_email_key" RENAME TO "uq_users_email"`,
		`COMMENT ON INDEX "public"."idx_name" IS NULL`,
		`COMMENT ON INDEX "public"."uq_users_email" IS 'one per user'`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 两个索引互换名字：先改名会撞上另一个还没改走的名字，所以都删了重建。
func TestAlterTableRebuildsIndexesThatSwapNames(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "users", usersColumns, usersIndexes)
	expectIndexConstraints(mock, "public", "users", map[string]string{"users_pkey": "p", "users_email_key": "u"})

	req := usersRequest()
	req.Indexes[1].Name = "idx_name"
	req.Indexes[2].Name = "idx_hash"
	result, err := session.AlterTableStatements(context.Background(), "app", "public.users", req)
	require.NoError(t, err)
	assert.Equal(t, []string{
		`DROP INDEX "public"."idx_hash"`,
		`DROP INDEX "public"."idx_name"`,
		`CREATE INDEX "idx_name" ON "public"."users" USING hash ("name")`,
		`CREATE INDEX "idx_hash" ON "public"."users" USING btree ("name" DESC, "tenant_id")`,
		`COMMENT ON INDEX "public"."idx_hash" IS 'by name'`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 新列可以自增（IDENTITY）；已有列按写法改默认值：表达式原样、值加引号、NULL、无是 DROP DEFAULT。
func TestAlterTableColumnFeaturesForPostgreSQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "public", "events", []pgColumn{
		{name: "created", dataType: "timestamp with time zone", nullable: "YES"},
		{name: "note", dataType: "text", nullable: "YES", def: "'x'::text"},
		{name: "code", dataType: "text", nullable: "YES"},
		{name: "flag", dataType: "boolean", nullable: "YES", def: "random() > 0.5"},
	}, nil)

	result, err := session.AlterTableStatements(context.Background(), "app", "events", dbdriver.AlterTableRequest{
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "created", Name: "created", Type: "timestamp with time zone", Nullable: true,
				DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "now()"},
			{OriginalName: "note", Name: "note", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindNull},
			{OriginalName: "code", Name: "code", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "it's"},
			{OriginalName: "flag", Name: "flag", Type: "boolean", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
			{Name: "seq", Type: "bigint", AutoIncrement: true, DefaultKind: dbdriver.DefaultKindNone},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		`ALTER TABLE "events" ADD COLUMN "seq" bigint GENERATED BY DEFAULT AS IDENTITY NOT NULL`,
		`ALTER TABLE "events" ALTER COLUMN "created" SET DEFAULT now()`,
		`ALTER TABLE "events" ALTER COLUMN "note" SET DEFAULT NULL`,
		`ALTER TABLE "events" ALTER COLUMN "code" SET DEFAULT 'it''s'`,
		`ALTER TABLE "events" ALTER COLUMN "flag" DROP DEFAULT`,
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 建表：IDENTITY 主键 id、按写法的默认值、主键写在表定义里，其余索引随后建，再写表注释、列注释与索引注释。
func TestCreateTableWithIndexesAndCommentsForPostgreSQL(t *testing.T) {
	session, _ := newMock(t)
	statements, err := session.CreateTableStatements(context.Background(), "app", dbdriver.CreateTableRequest{
		Name:    "reporting.events",
		Comment: "event log",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "bigint", AutoIncrement: true, DefaultKind: dbdriver.DefaultKindNone},
			{Name: "created", Type: "timestamptz", DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "now()"},
			{Name: "code", Type: "varchar(20)", Nullable: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "a", Comment: "the code"},
			{Name: "note", Type: "text", Nullable: true, DefaultKind: dbdriver.DefaultKindNull},
		},
		Indexes: []dbdriver.IndexDefinition{
			{Name: "events_pkey", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Comment: "pk"},
			{Name: "idx_events_code", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "code", Descending: true}},
				Options: method("gin"), Comment: "by code"},
			{Name: "uq_events_created", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "created"}}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"CREATE TABLE \"reporting\".\"events\" (\n" +
			"  \"id\" bigint GENERATED BY DEFAULT AS IDENTITY NOT NULL,\n" +
			"  \"created\" timestamptz DEFAULT now() NOT NULL,\n" +
			"  \"code\" varchar(20) DEFAULT 'a',\n" +
			"  \"note\" text DEFAULT NULL,\n" +
			"  CONSTRAINT \"events_pkey\" PRIMARY KEY (\"id\")\n" +
			")",
		`CREATE INDEX "idx_events_code" ON "reporting"."events" USING gin ("code" DESC)`,
		`CREATE UNIQUE INDEX "uq_events_created" ON "reporting"."events" ("created")`,
		`COMMENT ON TABLE "reporting"."events" IS 'event log'`,
		`COMMENT ON COLUMN "reporting"."events"."code" IS 'the code'`,
		`COMMENT ON INDEX "reporting"."events_pkey" IS 'pk'`,
		`COMMENT ON INDEX "reporting"."idx_events_code" IS 'by code'`,
	}, statements)
}

// 自增只用于整数类型、不能同时有默认值；主键的列不能降序、只能是 btree；没有名字的索引写不了注释。这些都以
// 错误拒绝，而不是写出数据库会拒的语句或悄悄丢掉。
func TestCreateTableRefusesWhatPostgreSQLCannotWrite(t *testing.T) {
	session, _ := newMock(t)
	for name, req := range map[string]dbdriver.CreateTableRequest{
		"identity on text": {Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "text", AutoIncrement: true}}},
		"identity with a default": {Name: "t", Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "bigint", AutoIncrement: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "1"}}},
		"descending primary key": {Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "bigint"}}, Indexes: []dbdriver.IndexDefinition{
			{Name: "t_pkey", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id", Descending: true}}}}},
		"hash primary key": {Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "bigint"}}, Indexes: []dbdriver.IndexDefinition{
			{Name: "t_pkey", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Options: method("hash")}}},
		"comment on an unnamed index": {Name: "t", Columns: []dbdriver.NewColumn{{Name: "id", Type: "bigint"}}, Indexes: []dbdriver.IndexDefinition{
			{Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Comment: "x"}}},
	} {
		_, err := session.CreateTableStatements(context.Background(), "app", req)
		assert.Error(t, err, name)
	}
}

// 前缀长度没有声明：带前缀的索引以 ErrUnsupported 拒绝。
func TestCreateTableRefusesIndexPrefixesForPostgreSQL(t *testing.T) {
	session, _ := newMock(t)
	_, err := session.CreateTableStatements(context.Background(), "app", dbdriver.CreateTableRequest{
		Name: "t", Columns: []dbdriver.NewColumn{{Name: "code", Type: "text"}},
		Indexes: []dbdriver.IndexDefinition{{Name: "ix", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "code", PrefixLength: 4}}}},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
}
