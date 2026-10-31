package mysql

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

func TestListDatabasesAndTables(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()

	mock.ExpectQuery("SHOW DATABASES").WillReturnRows(names("Database", "app", "mysql"))
	databases, err := session.ListDatabases(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "mysql"}, databases)

	// 库名按反引号转义，不能借表名拼出别的语句。
	mock.ExpectQuery("SHOW TABLES FROM `a``b`").WillReturnRows(names("Tables_in_a`b", "users", "orders"))
	tables, err := session.ListTables(ctx, "a`b")
	require.NoError(t, err)
	assert.Equal(t, []string{"users", "orders"}, tables)
	require.NoError(t, mock.ExpectationsWereMet())
}

var emptyDefault = ""

func TestTableMetadata(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery("SHOW FULL COLUMNS FROM `app`.`orders`").WillReturnRows(
		sqlmock.NewRows([]string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}).
			AddRow("id", "int", nil, "NO", "PRI", nil, "auto_increment", "select", "order id").
			AddRow("code", "varchar(20)", "utf8mb4_bin", "YES", "", "", "", "select", ""))
	mock.ExpectQuery("SHOW KEYS FROM `app`.`orders` WHERE Key_name = 'PRIMARY'").WillReturnRows(
		sqlmock.NewRows([]string{"Table", "Non_unique", "Key_name", "Seq_in_index", "Column_name"}).AddRow("orders", 0, "PRIMARY", 1, "id"))
	mock.ExpectQuery("SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS " +
		"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX").WithArgs("orders").WillReturnRows(
		sqlmock.NewRows([]string{"INDEX_NAME", "NON_UNIQUE", "COLUMN_NAME"}).
			AddRow("PRIMARY", 0, "id").AddRow("idx_code", 1, "code").AddRow("idx_code", 1, "id"))
	mock.ExpectQuery(foreignKeysSQL).WithArgs("orders").WillReturnRows(
		sqlmock.NewRows([]string{"c", "col", "rt", "rc"}).AddRow("fk_user", "user_id", "users", "id"))

	meta, err := session.TableMetadata(context.Background(), "app", "orders")
	require.NoError(t, err)

	assert.Equal(t, []dbdriver.ColumnMeta{
		{Name: "id", Type: "int", PrimaryKey: true, Comment: "order id"},
		{Name: "code", Type: "varchar(20)", Nullable: true, Default: &emptyDefault},
	}, meta.Columns)
	assert.Equal(t, []dbdriver.IndexMeta{
		{Name: "PRIMARY", Columns: []string{"id"}, Unique: true},
		{Name: "idx_code", Columns: []string{"code", "id"}},
	}, meta.Indexes)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{{Name: "fk_user", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id"}}, meta.ForeignKeys)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListObjectsGroupsByKind(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery("SELECT TABLE_NAME FROM information_schema.VIEWS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME").
		WillReturnRows(names("TABLE_NAME", "v_users"))
	mock.ExpectQuery("SELECT ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = 'PROCEDURE' ORDER BY ROUTINE_NAME").
		WillReturnRows(names("ROUTINE_NAME", "p_sync"))
	mock.ExpectQuery("SELECT ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_TYPE = 'FUNCTION' ORDER BY ROUTINE_NAME").
		WillReturnRows(names("ROUTINE_NAME", "f_total"))
	mock.ExpectQuery("SELECT TRIGGER_NAME FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() ORDER BY TRIGGER_NAME").
		WillReturnRows(names("TRIGGER_NAME", "trg_audit"))

	objects, err := session.ListObjects(context.Background(), "app")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "v_users", Type: "view"}}, objects.Views)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "p_sync", Type: "procedure"}}, objects.Procedures)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "f_total", Type: "function"}}, objects.Functions)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "trg_audit", Type: "trigger"}}, objects.Triggers)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestObjectSourceReadsTheCreateStatement(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()

	mock.ExpectQuery("SHOW CREATE PROCEDURE `p_sync`").WillReturnRows(
		sqlmock.NewRows([]string{"Procedure", "sql_mode", "Create Procedure"}).AddRow("p_sync", "", "CREATE PROCEDURE p_sync() BEGIN END"))
	source, err := session.ObjectSource(ctx, "app", "procedure", "p_sync", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE PROCEDURE p_sync() BEGIN END", source)

	mock.ExpectQuery("SHOW CREATE VIEW `gone`").WillReturnRows(sqlmock.NewRows([]string{"View", "Create View"}))
	_, err = session.ObjectSource(ctx, "app", "view", "gone", "")
	require.ErrorContains(t, err, "object not found: gone")

	// 不认识的对象类型：拒绝，不执行任何语句（mock 没有再预期任何查询）。
	_, err = session.ObjectSource(ctx, "app", "sequence", "s", "")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 表达式（函数）索引的键在 information_schema.STATISTICS 里 COLUMN_NAME 为 NULL、表达式在 EXPRESSION 列
// （MySQL 8.0.13 起）：读结构不因它整体失败，表达式按次序作为这一列显示。
func TestTableMetadataReadsExpressionIndexes(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery("SHOW FULL COLUMNS FROM `app`.`users`").WillReturnRows(
		sqlmock.NewRows([]string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}).
			AddRow("id", "int", nil, "NO", "PRI", nil, "", "select", "").
			AddRow("email", "varchar(100)", "utf8mb4_bin", "YES", "", nil, "", "select", ""))
	mock.ExpectQuery("SHOW KEYS FROM `app`.`users` WHERE Key_name = 'PRIMARY'").WillReturnRows(
		sqlmock.NewRows([]string{"Table", "Key_name", "Column_name"}).AddRow("users", "PRIMARY", "id"))
	mock.ExpectQuery("SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS " +
		"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX").WithArgs("users").WillReturnRows(
		sqlmock.NewRows([]string{"INDEX_NAME", "NON_UNIQUE", "COLUMN_NAME"}).
			AddRow("PRIMARY", 0, "id").
			AddRow("idx_lower_email", 1, nil).AddRow("idx_lower_email", 1, "id"))
	mock.ExpectQuery("SELECT INDEX_NAME, SEQ_IN_INDEX, EXPRESSION FROM information_schema.STATISTICS " +
		"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME IS NULL ORDER BY INDEX_NAME, SEQ_IN_INDEX").WithArgs("users").WillReturnRows(
		sqlmock.NewRows([]string{"INDEX_NAME", "SEQ_IN_INDEX", "EXPRESSION"}).AddRow("idx_lower_email", 1, "lower(`email`)"))
	mock.ExpectQuery(foreignKeysSQL).WithArgs("users").WillReturnRows(
		sqlmock.NewRows([]string{"c", "col", "rt", "rc", "u", "d"}))

	meta, err := session.TableMetadata(context.Background(), "app", "users")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.IndexMeta{
		{Name: "PRIMARY", Columns: []string{"id"}, Unique: true},
		{Name: "idx_lower_email", Columns: []string{"lower(`email`)", "id"}},
	}, meta.Indexes)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 外键带上 ON UPDATE / ON DELETE 的动作（information_schema.REFERENTIAL_CONSTRAINTS 的 UPDATE_RULE / DELETE_RULE）。
func TestTableMetadataReadsForeignKeyActions(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery("SHOW FULL COLUMNS FROM `app`.`orders`").WillReturnRows(
		sqlmock.NewRows([]string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}).
			AddRow("user_id", "int", nil, "NO", "MUL", nil, "", "select", ""))
	mock.ExpectQuery("SHOW KEYS FROM `app`.`orders` WHERE Key_name = 'PRIMARY'").WillReturnRows(
		sqlmock.NewRows([]string{"Table", "Key_name", "Column_name"}))
	mock.ExpectQuery("SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS " +
		"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX").WithArgs("orders").WillReturnRows(
		sqlmock.NewRows([]string{"INDEX_NAME", "NON_UNIQUE", "COLUMN_NAME"}).AddRow("fk_user", 1, "user_id"))
	mock.ExpectQuery("SELECT k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE " +
		"FROM information_schema.KEY_COLUMN_USAGE k " +
		"LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME " +
		"WHERE k.TABLE_SCHEMA = DATABASE() AND k.TABLE_NAME = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL " +
		"ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION").WithArgs("orders").WillReturnRows(
		sqlmock.NewRows([]string{"c", "col", "rt", "rc", "u", "d"}).AddRow("fk_user", "user_id", "users", "id", "CASCADE", "SET NULL"))

	meta, err := session.TableMetadata(context.Background(), "app", "orders")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{{
		Name: "fk_user", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id", OnUpdate: "CASCADE", OnDelete: "SET NULL",
	}}, meta.ForeignKeys)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestQuoting(t *testing.T) {
	assert.Equal(t, "`user`", quoteIdent("user"))
	assert.Equal(t, "`a``b`", quoteIdent("a`b"))
	assert.Equal(t, "`mydb`.`users`", quoteTableRef("mydb", "users"))
}
