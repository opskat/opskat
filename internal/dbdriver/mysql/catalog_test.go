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
	mock.ExpectQuery("SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME " +
		"FROM information_schema.KEY_COLUMN_USAGE " +
		"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL " +
		"ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION").WithArgs("orders").WillReturnRows(
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

func TestQuoting(t *testing.T) {
	assert.Equal(t, "`user`", quoteIdent("user"))
	assert.Equal(t, "`a``b`", quoteIdent("a`b"))
	assert.Equal(t, "`mydb`.`users`", quoteTableRef("mydb", "users"))
}
