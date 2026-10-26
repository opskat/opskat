package mysql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

const alterTableCommentSQL = "SELECT COALESCE(table_comment, '') AS table_comment FROM information_schema.tables " +
	"WHERE table_schema = ? AND table_name = ? LIMIT 1"

// alterColumn 是 SHOW FULL COLUMNS 的一行；nil 的 Collation / Default 是 NULL。
type alterColumn struct {
	field, typ string
	collation  any
	null       string
	def        any
	extra      string
	comment    string
}

func expectAlterTable(mock sqlmock.Sqlmock, database, table, tableComment string, columns ...alterColumn) {
	rows := sqlmock.NewRows([]string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"})
	for _, c := range columns {
		rows.AddRow(c.field, c.typ, c.collation, c.null, "", c.def, c.extra, "select,insert,update,references", c.comment)
	}
	mock.ExpectQuery("SHOW FULL COLUMNS FROM " + quoteTableRef(database, table)).WillReturnRows(rows)
	mock.ExpectQuery(alterTableCommentSQL).WithArgs(database, table).
		WillReturnRows(sqlmock.NewRows([]string{"table_comment"}).AddRow(tableComment))
}

func expectSQLMode(mock sqlmock.Sqlmock, mode string) {
	mock.ExpectQuery(sqlModeSQL).WillReturnRows(sqlmock.NewRows([]string{"@@SESSION.sql_mode"}).AddRow(mode))
}

// 打开改表对话框：列与表注释照 SHOW FULL COLUMNS 与 information_schema.tables 读出，与改动前对话框
// 显示的一致（没有默认值显示为空）。
func TestAlterTableModelForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectAlterTable(mock, "appdb", "users", "user table",
		alterColumn{field: "id", typ: "bigint unsigned", null: "NO", extra: "auto_increment", comment: "pk"},
		alterColumn{field: "name", typ: "varchar(100)", collation: "utf8mb4_bin", null: "YES", def: "anon"},
	)

	model, err := session.AlterTableModel(context.Background(), "appdb", "users")
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableModel{
		Name:    "users",
		Comment: "user table",
		Columns: []dbdriver.ColumnDefinition{
			{Name: "id", Type: "bigint unsigned", Nullable: false, Comment: "pk"},
			{Name: "name", Type: "varchar(100)", Nullable: true, DefaultValue: "anon"},
		},
	}, model)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 改名、新增、修改、删除与表注释：与改动前前端生成的文本逐字一致（原 AlterTableDialog.test.ts 的断言）。
func TestAlterTableStatementsForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectAlterTable(mock, "appdb", "users", "",
		alterColumn{field: "id", typ: "INT", null: "NO"},
		alterColumn{field: "name", typ: "VARCHAR(100)", null: "YES", comment: "old name"},
	)

	result, err := session.AlterTableStatements(context.Background(), "appdb", "users", dbdriver.AlterTableRequest{
		Name:    "users_v2",
		Comment: "user table",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "id", Name: "id", Type: "BIGINT", Nullable: false, Comment: "id column"},
			{Name: "email", Type: "VARCHAR(255)", Nullable: false, Comment: "email column"},
		},
		Drops: []string{"name"},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{
		Statements: []string{
			"RENAME TABLE `appdb`.`users` TO `appdb`.`users_v2`",
			"ALTER TABLE `appdb`.`users_v2` ADD COLUMN `email` VARCHAR(255) NOT NULL COMMENT 'email column', MODIFY COLUMN `id` BIGINT NOT NULL COMMENT 'id column', DROP COLUMN `name`, COMMENT = 'user table'",
		},
		Table: "users_v2",
	}, result)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 只给自增主键加一条注释，它仍然自增：改列时保留对话框里不显示、用户也没改的定义。
func TestAlterTableKeepsAutoIncrementWhenOnlyTheCommentChanges(t *testing.T) {
	session, mock := newMock(t)
	expectAlterTable(mock, "appdb", "users", "",
		alterColumn{field: "id", typ: "bigint unsigned", null: "NO", extra: "auto_increment"},
	)

	result, err := session.AlterTableStatements(context.Background(), "appdb", "users", dbdriver.AlterTableRequest{
		Name:    "users",
		Columns: []dbdriver.AlterColumn{{OriginalName: "id", Name: "id", Type: "bigint unsigned", Comment: "primary key"}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"ALTER TABLE `appdb`.`users` MODIFY COLUMN `id` bigint unsigned NOT NULL AUTO_INCREMENT COMMENT 'primary key'",
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 用户没动的 ON UPDATE、排序规则（字符集随它）与表达式默认值原样保留（MySQL 5.7 没有 DEFAULT_GENERATED，
// 时间函数默认值同样原样）；改成非文本类型时不再带排序规则；
// 用户改过的默认值按新值写（清空即去掉），与建表一样照改动前的规则写：反斜杠、二进制列都不另作处理。
func TestAlterTableKeepsUntouchedColumnAttributesForMySQL(t *testing.T) {
	session, mock := newMock(t)
	expectAlterTable(mock, "appdb", "events", "",
		alterColumn{field: "updated_at", typ: "timestamp", null: "NO", def: "CURRENT_TIMESTAMP", extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
		alterColumn{field: "code", typ: "varchar(36)", collation: "utf8mb4_bin", null: "YES", def: "uuid()", extra: "DEFAULT_GENERATED"},
		alterColumn{field: "label", typ: "varchar(10)", collation: "utf8mb4_bin", null: "YES"},
		alterColumn{field: "title", typ: "varchar(64)", collation: "utf8mb4_general_ci", null: "YES"},
		alterColumn{field: "created", typ: "datetime(3)", null: "YES", def: "CURRENT_TIMESTAMP(3)", extra: "DEFAULT_GENERATED"},
		alterColumn{field: "path", typ: "text", collation: "utf8mb4_0900_ai_ci", null: "YES"},
		alterColumn{field: "seen", typ: "datetime(6)", null: "YES", def: "CURRENT_TIMESTAMP(6)", extra: "on update CURRENT_TIMESTAMP(6)"},
	)

	result, err := session.AlterTableStatements(context.Background(), "appdb", "events", dbdriver.AlterTableRequest{
		Name: "events",
		Columns: []dbdriver.AlterColumn{
			{OriginalName: "updated_at", Name: "updated_at", Type: "timestamp", DefaultValue: "CURRENT_TIMESTAMP", Comment: "touched"},
			{OriginalName: "code", Name: "uid", Type: "varchar(36)", Nullable: true, DefaultValue: "uuid()"},
			{OriginalName: "label", Name: "label", Type: "varchar(20)", Nullable: true},
			{OriginalName: "title", Name: "title", Type: "int", Nullable: true},
			{OriginalName: "created", Name: "created", Type: "datetime(3)", Nullable: true},
			{OriginalName: "path", Name: "path", Type: "text", Nullable: true, DefaultValue: `C:\new`, Comment: `a\b`},
			{Name: "tag", Type: "varbinary(4)", Nullable: true, DefaultValue: "ab"},
			{OriginalName: "seen", Name: "seen", Type: "datetime(6)", Nullable: true, DefaultValue: "CURRENT_TIMESTAMP(6)", Comment: "5.7"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"ALTER TABLE `appdb`.`events` ADD COLUMN `tag` varbinary(4) DEFAULT 'ab', " +
		"MODIFY COLUMN `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT 'touched', " +
		"CHANGE COLUMN `code` `uid` varchar(36) COLLATE utf8mb4_bin DEFAULT (uuid()), " +
		"MODIFY COLUMN `label` varchar(20) COLLATE utf8mb4_bin, " +
		"MODIFY COLUMN `title` int, " +
		"MODIFY COLUMN `created` datetime(3), " +
		"MODIFY COLUMN `path` text COLLATE utf8mb4_0900_ai_ci DEFAULT 'C:\\new' COMMENT 'a\\b', " +
		"MODIFY COLUMN `seen` datetime(6) DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '5.7'",
	}, result.Statements)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 什么都没改时没有语句；说到的列不在表里时生成失败。
func TestAlterTableWithoutChangesOrWithUnknownColumnsForMySQL(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()
	expectAlterTable(mock, "appdb", "users", "c", alterColumn{field: "id", typ: "int", null: "NO"})

	result, err := session.AlterTableStatements(ctx, "appdb", "users", dbdriver.AlterTableRequest{
		Name: " users ", Comment: " c ",
		Columns: []dbdriver.AlterColumn{{OriginalName: "id", Name: "id", Type: " INT "}},
	})
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{Statements: []string{}, Table: "users"}, result)

	expectAlterTable(mock, "appdb", "users", "", alterColumn{field: "id", typ: "int", null: "NO"})
	_, err = session.AlterTableStatements(ctx, "appdb", "users", dbdriver.AlterTableRequest{
		Columns: []dbdriver.AlterColumn{{OriginalName: "gone", Name: "gone", Type: "int"}},
	})
	assert.ErrorContains(t, err, `"gone"`)

	expectAlterTable(mock, "appdb", "users", "", alterColumn{field: "id", typ: "int", null: "NO"})
	_, err = session.AlterTableStatements(ctx, "appdb", "users", dbdriver.AlterTableRequest{Drops: []string{"gone"}})
	assert.ErrorContains(t, err, `"gone"`)
	require.NoError(t, mock.ExpectationsWereMet())
}
