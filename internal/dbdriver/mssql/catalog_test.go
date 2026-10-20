package mssql

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

// 系统库（database_id 1~4：master / tempdb / model / msdb）由查询条件排除，只列用户库。
func TestListDatabasesExcludesSystemDatabases(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SELECT name FROM sys.databases WHERE database_id > 4 ORDER BY name").
		WillReturnRows(names("name", "appdb"))

	databases, err := session.ListDatabases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"appdb"}, databases)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListTablesQualifiesWithSchema(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SELECT TABLE_SCHEMA + '.' + TABLE_NAME AS name FROM INFORMATION_SCHEMA.TABLES " +
		"WHERE TABLE_TYPE IN ('BASE TABLE', 'VIEW') ORDER BY TABLE_SCHEMA, TABLE_NAME").
		WillReturnRows(names("name", "dbo.users", "sales.orders"))

	tables, err := session.ListTables(context.Background(), "appdb")
	require.NoError(t, err)
	assert.Equal(t, []string{"dbo.users", "sales.orders"}, tables)
	require.NoError(t, mock.ExpectationsWereMet())
}

var zeroDefault = "((0))"

func TestTableMetadataSplitsTheQualifiedName(t *testing.T) {
	session, mock := newMock(t)

	mock.ExpectQuery(columnsSQL).
		WillReturnRows(sqlmock.NewRows([]string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE", "COLUMN_DEFAULT", "COLUMN_COMMENT"}).
			AddRow("id", "int", "NO", "((0))", "event id").AddRow("note", "nvarchar", "YES", nil, nil))
	mock.ExpectQuery("SELECT kcu.COLUMN_NAME FROM INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc " +
		"JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu " +
		"ON tc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME AND tc.TABLE_SCHEMA = kcu.TABLE_SCHEMA " +
		"WHERE tc.CONSTRAINT_TYPE = 'PRIMARY KEY' " +
		"AND tc.TABLE_SCHEMA = @p1 AND tc.TABLE_NAME = @p2 " +
		"ORDER BY kcu.ORDINAL_POSITION").
		WillReturnRows(names("COLUMN_NAME", "id"))
	mock.ExpectQuery("SELECT i.name, i.is_unique, c.name " +
		"FROM sys.indexes i " +
		"JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id " +
		"JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id " +
		"JOIN sys.objects o ON o.object_id = i.object_id " +
		"JOIN sys.schemas s ON s.schema_id = o.schema_id " +
		"WHERE s.name = @p1 AND o.name = @p2 AND i.name IS NOT NULL " +
		"ORDER BY i.name, ic.key_ordinal").
		WillReturnRows(sqlmock.NewRows([]string{"name", "is_unique", "name"}).AddRow("PK_orders", true, "id"))
	mock.ExpectQuery("SELECT fk.name, pc.name, rt.name, rc.name " +
		"FROM sys.foreign_keys fk " +
		"JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id " +
		"JOIN sys.tables pt ON pt.object_id = fk.parent_object_id " +
		"JOIN sys.schemas s ON s.schema_id = pt.schema_id " +
		"JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id " +
		"JOIN sys.tables rt ON rt.object_id = fkc.referenced_object_id " +
		"JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id " +
		"WHERE s.name = @p1 AND pt.name = @p2 ORDER BY fk.name").
		WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c", "d"}).AddRow("FK_user", "user_id", "users", "id"))

	meta, err := session.TableMetadata(context.Background(), "appdb", "sales.orders")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ColumnMeta{
		{Name: "id", Type: "int", PrimaryKey: true, Comment: "event id", Default: &zeroDefault},
		{Name: "note", Type: "nvarchar", Nullable: true},
	}, meta.Columns)
	assert.Equal(t, []dbdriver.IndexMeta{{Name: "PK_orders", Columns: []string{"id"}, Unique: true}}, meta.Indexes)
	assert.Equal(t, []dbdriver.ForeignKeyMeta{{Name: "FK_user", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id"}}, meta.ForeignKeys)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 裸表名按 dbo 解析。
func TestBareTableNameDefaultsToDbo(t *testing.T) {
	schema, table := splitSchemaTable("users")
	assert.Equal(t, "dbo", schema)
	assert.Equal(t, "users", table)
}

func TestListObjectsSchemaQualifiesNames(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery("SELECT s.name + '.' + v.name FROM sys.views v JOIN sys.schemas s ON s.schema_id = v.schema_id ORDER BY 1").
		WillReturnRows(names("v", "dbo.v_users"))
	mock.ExpectQuery("SELECT s.name + '.' + p.name FROM sys.procedures p JOIN sys.schemas s ON s.schema_id = p.schema_id ORDER BY 1").
		WillReturnRows(names("p", "dbo.p_sync"))
	mock.ExpectQuery("SELECT s.name + '.' + o.name FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id WHERE o.type IN ('FN','IF','TF') ORDER BY 1").
		WillReturnRows(names("f", "dbo.f_total"))
	mock.ExpectQuery("SELECT CASE WHEN s.name IS NULL THEN tr.name ELSE s.name + '.' + tr.name END " +
		"FROM sys.triggers tr " +
		"LEFT JOIN sys.objects o ON o.object_id = tr.parent_id " +
		"LEFT JOIN sys.schemas s ON s.schema_id = o.schema_id " +
		"WHERE tr.is_ms_shipped = 0 ORDER BY 1").
		WillReturnRows(names("t", "dbo.trg_audit", "ddl_guard"))

	objects, err := session.ListObjects(context.Background(), "appdb")
	require.NoError(t, err)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "dbo.v_users", Type: "view"}}, objects.Views)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "dbo.p_sync", Type: "procedure"}}, objects.Procedures)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "dbo.f_total", Type: "function"}}, objects.Functions)
	assert.Equal(t, []dbdriver.ObjectMeta{{Name: "dbo.trg_audit", Type: "trigger"}, {Name: "ddl_guard", Type: "trigger"}}, objects.Triggers)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestObjectSourceUsesObjectDefinition(t *testing.T) {
	session, mock := newMock(t)
	ctx := context.Background()

	mock.ExpectQuery("SELECT OBJECT_DEFINITION(OBJECT_ID(@p1))").WillReturnRows(names("def", "CREATE VIEW dbo.v_users AS SELECT 1"))
	source, err := session.ObjectSource(ctx, "appdb", "view", "dbo.v_users", "")
	require.NoError(t, err)
	assert.Equal(t, "CREATE VIEW dbo.v_users AS SELECT 1", source)

	_, err = session.ObjectSource(ctx, "appdb", "sequence", "dbo.s", "")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	require.NoError(t, mock.ExpectationsWereMet())
}
