package mysql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// indexRow 是 SHOW INDEX 的一行；nil 的 column / collation / subPart / expression 是 NULL。
type indexRow struct {
	name       string
	nonUnique  int
	seq        int
	column     any
	collation  any
	subPart    any
	indexType  string
	comment    string
	visible    string
	expression any
}

// expectIndexes 是改表读表时的 SHOW INDEX（MySQL 8.0.13 起的列，含 Visible 与 Expression）。
func expectIndexes(mock sqlmock.Sqlmock, database, table string, parts ...indexRow) {
	rows := sqlmock.NewRows([]string{
		"Table", "Non_unique", "Key_name", "Seq_in_index", "Column_name", "Collation", "Cardinality", "Sub_part",
		"Packed", "Null", "Index_type", "Comment", "Index_comment", "Visible", "Expression",
	})
	for _, p := range parts {
		visible := p.visible
		if visible == "" {
			visible = "YES"
		}
		rows.AddRow(table, p.nonUnique, p.name, p.seq, p.column, p.collation, 0, p.subPart, nil, "", p.indexType, "", p.comment, visible, p.expression)
	}
	mock.ExpectQuery("SHOW INDEX FROM " + quoteTableRef(database, table)).WillReturnRows(rows)
}

// expectDesignedTable 同 expectAlterTable，但表带着索引。
func expectDesignedTable(mock sqlmock.Sqlmock, database, table string, columns []alterColumn, parts ...indexRow) {
	rows := sqlmock.NewRows([]string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"})
	for _, c := range columns {
		rows.AddRow(c.field, c.typ, c.collation, c.null, "", c.def, c.extra, "select,insert,update,references", c.comment)
	}
	mock.ExpectQuery("SHOW FULL COLUMNS FROM " + quoteTableRef(database, table)).WillReturnRows(rows)
	mock.ExpectQuery(alterTableCommentSQL).WithArgs(database, table).
		WillReturnRows(sqlmock.NewRows([]string{"table_comment"}).AddRow(""))
	expectIndexes(mock, database, table, parts...)
}

func btree(name string, nonUnique, seq int, column string) indexRow {
	return indexRow{name: name, nonUnique: nonUnique, seq: seq, column: column, collation: "A", indexType: "BTREE"}
}

var usersColumns = []alterColumn{
	{field: "id", typ: "bigint", null: "NO", extra: "auto_increment"},
	{field: "tenant_id", typ: "int", null: "NO"},
	{field: "name", typ: "varchar(100)", collation: "utf8mb4_bin", null: "YES"},
	{field: "email", typ: "varchar(200)", collation: "utf8mb4_bin", null: "YES"},
}

func method(value string) map[string]any { return map[string]any{"method": value} }

// 已有列的默认值按 SHOW FULL COLUMNS 判定写法：没有默认值是「无」；MySQL 8 的表达式默认值（Extra 里
// DEFAULT_GENERATED，含 CURRENT_TIMESTAMP）与 5.7 的时间函数默认值是表达式；位串与十六进制写法原样作
// 表达式；其余是值（空串也是值）。
func TestAlterTableModelClassifiesDefaultsForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "appdb", "events", []alterColumn{
		{field: "id", typ: "int", null: "NO", extra: "auto_increment"},
		{field: "note", typ: "varchar(20)", null: "YES"},
		{field: "label", typ: "varchar(20)", null: "NO", def: ""},
		{field: "count", typ: "int", null: "NO", def: "0"},
		{field: "created", typ: "datetime(3)", null: "YES", def: "CURRENT_TIMESTAMP(3)", extra: "DEFAULT_GENERATED"},
		{field: "code", typ: "varchar(36)", null: "YES", def: "uuid()", extra: "DEFAULT_GENERATED"},
		{field: "seen", typ: "timestamp", null: "YES", def: "CURRENT_TIMESTAMP", extra: "on update CURRENT_TIMESTAMP"},
		{field: "flag", typ: "bit(1)", null: "YES", def: "b'1'"},
		{field: "tag", typ: "varbinary(4)", null: "YES", def: "0x6162"},
	})

	model, err := session.AlterTableModel(context.Background(), "appdb", "events")
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
		"note":    {dbdriver.DefaultKindNone, "", false},
		"label":   {dbdriver.DefaultKindValue, "", false},
		"count":   {dbdriver.DefaultKindValue, "0", false},
		"created": {dbdriver.DefaultKindExpression, "CURRENT_TIMESTAMP(3)", false},
		"code":    {dbdriver.DefaultKindExpression, "uuid()", false},
		"seen":    {dbdriver.DefaultKindExpression, "CURRENT_TIMESTAMP", false},
		"flag":    {dbdriver.DefaultKindExpression, "b'1'", false},
		"tag":     {dbdriver.DefaultKindExpression, "0x6162", false},
	}, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改表模型带上 SHOW INDEX 读出的索引（主键在内，按 SHOW INDEX 的次序）：类型、方法、降序、前缀与注释；
// 表达式键、空间索引与不可见索引设计器无法原样重建，只读并说明原因。索引名在表内唯一，没有范围内的其他名字。
func TestAlterTableModelReadsIndexesForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "appdb", "users", usersColumns,
		btree("PRIMARY", 0, 1, "id"),
		indexRow{name: "idx_name", nonUnique: 1, seq: 1, column: "name", collation: "D", subPart: 10, indexType: "BTREE", comment: "by name"},
		btree("idx_name", 1, 2, "tenant_id"),
		indexRow{name: "uq_email", nonUnique: 0, seq: 1, column: "email", collation: "A", indexType: "HASH"},
		indexRow{name: "ft_name", nonUnique: 1, seq: 1, column: "name", indexType: "FULLTEXT"},
		indexRow{name: "idx_lower", nonUnique: 1, seq: 1, collation: "A", indexType: "BTREE", expression: "lower(`email`)"},
		indexRow{name: "idx_hidden", nonUnique: 1, seq: 1, column: "tenant_id", collation: "A", indexType: "BTREE", visible: "NO"},
		indexRow{name: "sp_area", nonUnique: 1, seq: 1, column: "email", collation: "A", subPart: 32, indexType: "SPATIAL"},
	)

	model, err := session.AlterTableModel(context.Background(), "appdb", "users")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.IndexDefinition{
		{Name: "PRIMARY", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Options: method("BTREE")},
		{Name: "idx_name", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{
			{Name: "name", Descending: true, PrefixLength: 10}, {Name: "tenant_id"},
		}, Options: method("BTREE"), Comment: "by name"},
		{Name: "uq_email", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "email"}}, Options: method("HASH")},
		{Name: "ft_name", Type: dbdriver.IndexTypeFullText, Columns: []dbdriver.IndexColumn{{Name: "name"}}},
		{Name: "idx_lower", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "lower(`email`)", Expression: true}},
			Options: method("BTREE"), ReadOnlyReason: readOnlyExpression},
		{Name: "idx_hidden", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "tenant_id"}},
			Options: method("BTREE"), ReadOnlyReason: readOnlyInvisible},
		{Name: "sp_area", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "email", PrefixLength: 32}},
			ReadOnlyReason: readOnlySpatial},
	}, model.Indexes)
	assert.Empty(t, model.TakenIndexNames)
	require.NoError(t, mock.ExpectationsWereMet())
}

// alterUsers 是对 usersColumns 那张表的改表：读表（带 parts 这些索引）后按请求生成语句。
func alterUsers(t *testing.T, req dbdriver.AlterTableRequest, parts ...indexRow) ([]string, error) {
	t.Helper()
	session, mock := newMock(t)
	expectDesignedTable(mock, "appdb", "users", usersColumns, parts...)
	result, err := session.AlterTableStatements(context.Background(), "appdb", "users", req)
	require.NoError(t, mock.ExpectationsWereMet())
	if err != nil {
		return nil, err
	}
	return result.Statements, nil
}

// keptColumns 是 usersColumns 原样交回（自增现状一并交回）。
func keptColumns() []dbdriver.AlterColumn {
	return []dbdriver.AlterColumn{
		{OriginalName: "id", Name: "id", Type: "bigint", DefaultKind: dbdriver.DefaultKindNone, AutoIncrement: true},
		{OriginalName: "tenant_id", Name: "tenant_id", Type: "int", DefaultKind: dbdriver.DefaultKindNone},
		{OriginalName: "name", Name: "name", Type: "varchar(100)", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
		{OriginalName: "email", Name: "email", Type: "varchar(200)", Nullable: true, DefaultKind: dbdriver.DefaultKindNone},
	}
}

var primaryRow = btree("PRIMARY", 0, 1, "id")

func primaryKept() dbdriver.AlterIndex {
	return dbdriver.AlterIndex{OriginalName: "PRIMARY", IndexDefinition: dbdriver.IndexDefinition{
		Name: "PRIMARY", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}, Options: method("BTREE"),
	}}
}

// 新建索引：前缀、降序、方法与注释都写进 ALTER TABLE 的 ADD INDEX；没有改的主键不产生语句。
func TestAlterTableAddsAnIndexForMySQL(t *testing.T) {
	statements, err := alterUsers(t, dbdriver.AlterTableRequest{
		Columns: keptColumns(),
		Indexes: []dbdriver.AlterIndex{primaryKept(), {IndexDefinition: dbdriver.IndexDefinition{
			Name: "idx_name", Type: dbdriver.IndexTypeNormal,
			Columns: []dbdriver.IndexColumn{{Name: "name", PrefixLength: 10, Descending: true}, {Name: "tenant_id"}},
			Options: method("BTREE"), Comment: "by name",
		}}, {IndexDefinition: dbdriver.IndexDefinition{
			Name: "ft_email", Type: dbdriver.IndexTypeFullText, Columns: []dbdriver.IndexColumn{{Name: "email"}},
		}}},
	}, primaryRow)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ALTER TABLE `appdb`.`users` ADD INDEX `idx_name` (`name`(10) DESC, `tenant_id`) USING BTREE COMMENT 'by name', " +
			"ADD FULLTEXT INDEX `ft_email` (`email`)",
	}, statements)
}

// 改动已有索引：在同一条 ALTER TABLE 里先删后建（同名的删与建可以在一条语句里）。
func TestAlterTableChangesAnIndexForMySQL(t *testing.T) {
	statements, err := alterUsers(t, dbdriver.AlterTableRequest{
		Columns: keptColumns(),
		Indexes: []dbdriver.AlterIndex{primaryKept(), {OriginalName: "idx_name", IndexDefinition: dbdriver.IndexDefinition{
			Name: "uq_name", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "tenant_id"}, {Name: "name"}},
		}}},
	}, primaryRow, btree("idx_name", 1, 1, "name"))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ALTER TABLE `appdb`.`users` DROP INDEX `idx_name`, ADD UNIQUE INDEX `uq_name` (`tenant_id`, `name`)",
	}, statements)
}

// 改主键：DROP PRIMARY KEY 与 ADD PRIMARY KEY 写在同一条语句里——自增列必须始终在键上，分开执行时
// 单独的 DROP PRIMARY KEY 会被 MySQL 拒绝（1075）。
func TestAlterTableChangesThePrimaryKeyForMySQL(t *testing.T) {
	statements, err := alterUsers(t, dbdriver.AlterTableRequest{
		Columns: keptColumns(),
		Indexes: []dbdriver.AlterIndex{{OriginalName: "PRIMARY", IndexDefinition: dbdriver.IndexDefinition{
			Name: "PRIMARY", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}, {Name: "tenant_id"}},
		}}},
	}, primaryRow)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ALTER TABLE `appdb`.`users` DROP PRIMARY KEY, ADD PRIMARY KEY (`id`, `tenant_id`)",
	}, statements)
}

// 删列时引用它的索引随列一起删，DROP INDEX 在 DROP COLUMN 之前；新增列上的索引在 ADD COLUMN 之后建；
// 改名之后的语句都用新表名。
func TestAlterTableOrdersIndexesAroundColumnChangesForMySQL(t *testing.T) {
	columns := keptColumns()[:3] // 删掉 email
	columns = append(columns, dbdriver.AlterColumn{Name: "code", Type: "varchar(20)", Nullable: false, DefaultKind: dbdriver.DefaultKindNone})
	statements, err := alterUsers(t, dbdriver.AlterTableRequest{
		Name:    "members",
		Columns: columns,
		Drops:   []string{"email"},
		Indexes: []dbdriver.AlterIndex{primaryKept(), {IndexDefinition: dbdriver.IndexDefinition{
			Name: "uq_code", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "code"}},
		}}},
	}, primaryRow, btree("idx_email", 1, 1, "email"))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"RENAME TABLE `appdb`.`users` TO `appdb`.`members`",
		"ALTER TABLE `appdb`.`members` DROP INDEX `idx_email`, ADD COLUMN `code` varchar(20) NOT NULL, DROP COLUMN `email`, " +
			"ADD UNIQUE INDEX `uq_code` (`code`)",
	}, statements)
}

// 表达式索引只读：原样交回不产生语句，改动以不支持拒绝，可以删除。
func TestAlterTableNeverRebuildsAnExpressionIndexForMySQL(t *testing.T) {
	expressionRow := indexRow{name: "idx_lower", nonUnique: 1, seq: 1, collation: "A", indexType: "BTREE", expression: "lower(`email`)"}
	readBack := dbdriver.AlterIndex{OriginalName: "idx_lower", IndexDefinition: dbdriver.IndexDefinition{
		Name: "idx_lower", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "lower(`email`)", Expression: true}},
		Options: method("BTREE"), ReadOnlyReason: readOnlyExpression,
	}}

	statements, err := alterUsers(t, dbdriver.AlterTableRequest{Columns: keptColumns(), Indexes: []dbdriver.AlterIndex{readBack}}, expressionRow)
	require.NoError(t, err)
	assert.Empty(t, statements)

	changed := readBack
	changed.Name = "idx_lower_email"
	_, err = alterUsers(t, dbdriver.AlterTableRequest{Columns: keptColumns(), Indexes: []dbdriver.AlterIndex{changed}}, expressionRow)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)

	statements, err = alterUsers(t, dbdriver.AlterTableRequest{Columns: keptColumns(), IndexDrops: []string{"idx_lower"}}, expressionRow)
	require.NoError(t, err)
	assert.Equal(t, []string{"ALTER TABLE `appdb`.`users` DROP INDEX `idx_lower`"}, statements)
}

// 按写法给出的默认值：值按列类型写字面量（数值类型的数字与 TRUE / FALSE 不加引号），表达式加括号（时间
// 函数除外，它们不加括号也是默认值），NULL 写 NULL，无不写；没改的已有列原样写回现在的默认值。新增列可以自增。
func TestAlterTableWritesDefaultKindsAndAutoIncrementForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectDesignedTable(mock, "appdb", "events", []alterColumn{
		{field: "id", typ: "int", null: "NO", extra: "auto_increment"},
		{field: "code", typ: "varchar(36)", null: "YES", def: "uuid()", extra: "DEFAULT_GENERATED"},
		{field: "flag", typ: "bit(1)", null: "YES", def: "b'1'"},
		{field: "name", typ: "varchar(20)", null: "YES", def: "x"},
	})

	result, err := session.AlterTableStatements(context.Background(), "appdb", "events", dbdriver.AlterTableRequest{
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "id", Name: "id", Type: "int", DefaultKind: dbdriver.DefaultKindNone, AutoIncrement: true},
			{OriginalName: "code", Name: "code", Type: "varchar(36)", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "uuid()", Comment: "kept"},
			{OriginalName: "flag", Name: "flag", Type: "bit(1)", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "b'1'", Comment: "kept"},
			{OriginalName: "name", Name: "name", Type: "varchar(20)", Nullable: true, DefaultKind: dbdriver.DefaultKindNull},
			{Name: "seq", Type: "BIGINT", DefaultKind: dbdriver.DefaultKindNone, AutoIncrement: true},
			{Name: "count", Type: "INT", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "0"},
			{Name: "active", Type: "BOOLEAN", Nullable: true, DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "true"},
			{Name: "label", Type: "VARCHAR(20)", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: "it's"},
			{Name: "token", Type: "CHAR(36)", DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: " uuid() "},
			{Name: "created", Type: "DATETIME(3)", DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "CURRENT_TIMESTAMP(3)"},
		},
		Indexes: []dbdriver.AlterIndex{{IndexDefinition: dbdriver.IndexDefinition{
			Name: "uq_seq", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "seq"}},
		}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"ALTER TABLE `appdb`.`events` " +
		"ADD COLUMN `seq` BIGINT NOT NULL AUTO_INCREMENT, " +
		"ADD COLUMN `count` INT NOT NULL DEFAULT 0, " +
		"ADD COLUMN `active` BOOLEAN DEFAULT TRUE, " +
		"ADD COLUMN `label` VARCHAR(20) NOT NULL DEFAULT 'it''s', " +
		"ADD COLUMN `token` CHAR(36) NOT NULL DEFAULT (uuid()), " +
		"ADD COLUMN `created` DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3), " +
		"MODIFY COLUMN `code` varchar(36) DEFAULT (uuid()) COMMENT 'kept', " +
		"MODIFY COLUMN `flag` bit(1) DEFAULT b'1' COMMENT 'kept', " +
		"MODIFY COLUMN `name` varchar(20) DEFAULT NULL, " +
		"ADD UNIQUE INDEX `uq_seq` (`seq`)",
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 自增只能用在整数类型的新列上。
func TestAlterTableRefusesAutoIncrementOnANonIntegerColumnForMySQL(t *testing.T) {
	columns := append(keptColumns(), dbdriver.AlterColumn{Name: "code", Type: "varchar(20)", DefaultKind: dbdriver.DefaultKindNone, AutoIncrement: true})
	_, err := alterUsers(t, dbdriver.AlterTableRequest{Columns: columns}, primaryRow)
	require.ErrorContains(t, err, `"code"`)
}

// 建表：列注释、自增、按写法的默认值与索引（主键在内）都写在 CREATE TABLE 里，表注释写在表选项里。
func TestCreateTableWithCommentsIndexesAndAutoIncrementForMySQL(t *testing.T) {
	session, _ := newMock(t)

	statements, err := session.CreateTableStatements(context.Background(), "appdb", dbdriver.CreateTableRequest{
		Name:    "users",
		Comment: "app users",
		Columns: []dbdriver.NewColumn{
			{Name: "id", Type: "BIGINT", DefaultKind: dbdriver.DefaultKindNone, AutoIncrement: true, Comment: "pk"},
			{Name: "email", Type: "VARCHAR(255)", DefaultKind: dbdriver.DefaultKindValue, DefaultValue: ""},
			{Name: "token", Type: "CHAR(36)", Nullable: true, DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "uuid()"},
			{Name: "created_at", Type: "DATETIME", DefaultKind: dbdriver.DefaultKindExpression, DefaultValue: "CURRENT_TIMESTAMP", Comment: "it's created"},
		},
		Indexes: []dbdriver.IndexDefinition{
			{Name: "PRIMARY", Type: dbdriver.IndexTypePrimary, Columns: []dbdriver.IndexColumn{{Name: "id"}}},
			{Name: "uq_email", Type: dbdriver.IndexTypeUnique, Columns: []dbdriver.IndexColumn{{Name: "email", PrefixLength: 100}}, Comment: "login"},
			{Name: "idx_created", Type: dbdriver.IndexTypeNormal, Columns: []dbdriver.IndexColumn{{Name: "created_at", Descending: true}}, Options: method("BTREE")},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"CREATE TABLE `appdb`.`users` (\n" +
		"  `id` BIGINT AUTO_INCREMENT NOT NULL COMMENT 'pk',\n" +
		"  `email` VARCHAR(255) DEFAULT '' NOT NULL,\n" +
		"  `token` CHAR(36) DEFAULT (uuid()),\n" +
		"  `created_at` DATETIME DEFAULT CURRENT_TIMESTAMP NOT NULL COMMENT 'it''s created',\n" +
		"  PRIMARY KEY (`id`),\n" +
		"  UNIQUE INDEX `uq_email` (`email`(100)) COMMENT 'login',\n" +
		"  INDEX `idx_created` (`created_at` DESC) USING BTREE\n" +
		") COMMENT = 'app users'"}, statements)
}
