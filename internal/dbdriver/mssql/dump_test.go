package mssql

import (
	"context"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

const (
	listViewsSQL      = "SELECT s.name + '.' + v.name FROM sys.views v JOIN sys.schemas s ON s.schema_id = v.schema_id ORDER BY 1"
	listProceduresSQL = "SELECT s.name + '.' + p.name FROM sys.procedures p JOIN sys.schemas s ON s.schema_id = p.schema_id ORDER BY 1"
	listFunctionsSQL  = "SELECT s.name + '.' + o.name FROM sys.objects o JOIN sys.schemas s ON s.schema_id = o.schema_id " +
		"WHERE o.type IN ('FN','IF','TF') ORDER BY 1"
	listTriggersSQL = "SELECT CASE WHEN s.name IS NULL THEN tr.name ELSE s.name + '.' + tr.name END FROM sys.triggers tr " +
		"LEFT JOIN sys.objects o ON o.object_id = tr.parent_id LEFT JOIN sys.schemas s ON s.schema_id = o.schema_id " +
		"WHERE tr.is_ms_shipped = 0 ORDER BY 1"
	listTablesSQL = "SELECT TABLE_SCHEMA + '.' + TABLE_NAME AS name FROM INFORMATION_SCHEMA.TABLES " +
		"WHERE TABLE_TYPE IN ('BASE TABLE', 'VIEW') ORDER BY TABLE_SCHEMA, TABLE_NAME"

	objectDefinitionSQL = "SELECT OBJECT_DEFINITION(OBJECT_ID(@p1))"
	findTriggerSQL      = "FROM sys.triggers tr WHERE (tr.parent_class = 0 AND tr.name = @p1) OR tr.object_id = OBJECT_ID(@p2) " +
		"ORDER BY tr.parent_class"
	triggerSourceSQL = "SELECT OBJECT_DEFINITION((SELECT TOP 1 tr.object_id " + findTriggerSQL + "))"
	triggerStateSQL  = "SELECT TOP 1 tr.parent_class, tr.is_disabled, OBJECT_SCHEMA_NAME(tr.parent_id), OBJECT_NAME(tr.parent_id) " +
		findTriggerSQL

	tableForeignKeysSQL = "SELECT fk.name, OBJECT_SCHEMA_NAME(fk.referenced_object_id), OBJECT_NAME(fk.referenced_object_id), " +
		"pc.name, rc.name, fk.delete_referential_action_desc, fk.update_referential_action_desc, " +
		"fk.is_disabled, fk.is_not_trusted, fk.is_not_for_replication " +
		"FROM sys.foreign_keys fk JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id " +
		"JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id " +
		"JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id " +
		"WHERE fk.parent_object_id = OBJECT_ID(@p1) ORDER BY fk.name, fkc.constraint_column_id"
	tableIdentitySQL    = "SELECT CAST(last_value AS NVARCHAR(40)) FROM sys.identity_columns WHERE object_id = OBJECT_ID(@p1)"
	insertableColumnSQL = "SELECT name FROM sys.columns WHERE object_id = OBJECT_ID(@p1) " +
		"AND is_computed = 0 AND system_type_id <> 189 ORDER BY column_id"
	viewDependenciesSQL = "SELECT DISTINCT s.name + '.' + o.name FROM sys.sql_expression_dependencies d " +
		"JOIN sys.objects o ON o.object_id = d.referenced_id JOIN sys.schemas s ON s.schema_id = o.schema_id " +
		"WHERE d.referencing_id = OBJECT_ID(@p1)"
)

// guidRaw 是 6F9619FF-8B86-D011-B42D-00C04FC964FF 在 SQL Server 里的 16 个字节（前三段按小端存放）。
const guidRaw = "\xff\x19\x96\x6f\x86\x8b\x11\xd0\xb4\x2d\x00\xc0\x4f\xc9\x64\xff"

func dumpHeader() dbdriver.DumpHeader {
	return dbdriver.DumpHeader{Version: "1.16.0", Asset: "prod-mssql", Time: time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)}
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS("testdata"), name)
	require.NoError(t, err)
	return string(data)
}

// expectSampleObjects 预期 ListObjects 的四次查询与 ListTables：两张表、两个视图（dbo 的依赖 sales 的）、一个函数、
// 一个过程、一个 DML 触发器与一个库级 DDL 触发器。
func expectSampleObjects(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(listViewsSQL).WillReturnRows(names("v", "dbo.v_named", "sales.v_names"))
	mock.ExpectQuery(listProceduresSQL).WillReturnRows(names("p", "sales.p_rename"))
	mock.ExpectQuery(listFunctionsSQL).WillReturnRows(names("f", "dbo.f_net"))
	mock.ExpectQuery(listTriggersSQL).WillReturnRows(names("t", "sales.tr_customers_upd", "tr_ddl"))
	mock.ExpectQuery(listTablesSQL).WillReturnRows(names("name", "dbo.orders", "dbo.v_named", "sales.customers", "sales.v_names"))
}

// expectTableDDL 预期 TableDDL 的三次查询（列、索引、CHECK），没有索引与 CHECK 之外给出的主键。
func expectTableDDL(mock sqlmock.Sqlmock, object string, columns *sqlmock.Rows, primaryKey string, keyColumn string) {
	mock.ExpectQuery(ddlColumnsQuery).WithArgs(object).WillReturnRows(columns)
	indexes := ddlIndexRows()
	if primaryKey != "" {
		indexes.AddRow(primaryKey, true, false, true, int64(1), nil, keyColumn, false, false)
	}
	mock.ExpectQuery(ddlIndexesQuery).WithArgs(object).WillReturnRows(indexes)
	mock.ExpectQuery(ddlChecksQuery).WithArgs(object).WillReturnRows(ddlCheckRows())
}

func foreignKeyRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"name", "ref_schema", "ref_table", "column", "ref_column", "on_delete", "on_update",
		"is_disabled", "is_not_trusted", "is_not_for_replication"})
}

func expectCustomersStructure(mock sqlmock.Sqlmock) {
	expectTableDDL(mock, "[sales].[customers]", ddlColumnRows().
		AddRow("id", "int", int64(4), int64(10), int64(0), false, int64(100), int64(5), nil, nil, nil, false).
		AddRow("parent_id", "int", int64(4), int64(10), int64(0), true, nil, nil, nil, nil, nil, false).
		AddRow("name", "nvarchar", int64(-1), int64(0), int64(0), true, nil, nil, nil, nil, nil, false), "PK_customers", "id")
	mock.ExpectQuery(tableForeignKeysSQL).WithArgs("[sales].[customers]").WillReturnRows(foreignKeyRows().
		AddRow("FK_customers_parent", "sales", "customers", "parent_id", "id", "NO_ACTION", "SET_NULL", true, true, true))
	mock.ExpectQuery(tableIdentitySQL).WithArgs("[sales].[customers]").WillReturnRows(names("last_value", "110"))
}

func expectView(mock sqlmock.Sqlmock, name, object, definition string, dependencies ...string) {
	expectTableDDL(mock, object, ddlColumnRows().
		AddRow("id", "int", int64(4), int64(10), int64(0), false, nil, nil, nil, nil, nil, false), "", "")
	mock.ExpectQuery(objectDefinitionSQL).WithArgs(object).WillReturnRows(names("definition", definition))
	mock.ExpectQuery(viewDependenciesSQL).WithArgs(object).WillReturnRows(names("name", dependencies...))
}

func triggerStateRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"parent_class", "is_disabled", "table_schema", "table_name"})
}

func expectTableTrigger(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(triggerSourceSQL).WithArgs("sales.tr_customers_upd", "[sales].[tr_customers_upd]").
		WillReturnRows(names("definition", "CREATE TRIGGER sales.tr_customers_upd ON sales.customers AFTER UPDATE AS SET NOCOUNT ON;"))
	mock.ExpectQuery(triggerStateSQL).WithArgs("sales.tr_customers_upd", "[sales].[tr_customers_upd]").
		WillReturnRows(triggerStateRows().AddRow(int64(1), true, "sales", "customers"))
}

// 整库转储（结构和数据）写出的文件：每批之后 GO；每个对象前 DROP ... IF EXISTS（库级 DDL 触发器带 ON
// DATABASE）；表的 INSERT 不写计算列与 rowversion，uniqueidentifier 写成 GUID、二进制写成 0x…、2^53 以上的
// bigint 不丢位；有 IDENTITY 列的表数据前后开、关 IDENTITY_INSERT；外键（含自引用，禁用的、不受信任的保持原状）
// 在全部数据之后；视图写服务器的原文、被依赖的在前（不在列出的次序）；函数、过程、触发器写服务器的原文、不补
// 分号，禁用的触发器建出后再禁用；最后把 IDENTITY 的当前值设回源库的。
func TestDumpWritesRestorableSQLServerFile(t *testing.T) {
	session, mock := newMock(t)
	expectSampleObjects(mock)

	expectTableDDL(mock, "[dbo].[orders]", ddlColumnRows().
		AddRow("id", "bigint", int64(8), int64(19), int64(0), false, nil, nil, nil, nil, nil, false).
		AddRow("customer_id", "int", int64(4), int64(10), int64(0), false, nil, nil, nil, nil, nil, false).
		AddRow("total", "int", int64(4), int64(10), int64(0), true, nil, nil, nil, nil, "([customer_id]*(2))", true).
		AddRow("payload", "varbinary", int64(-1), int64(0), int64(0), true, nil, nil, nil, nil, nil, false).
		AddRow("rv", "timestamp", int64(8), int64(0), int64(0), false, nil, nil, nil, nil, nil, false).
		AddRow("guid", "uniqueidentifier", int64(16), int64(0), int64(0), true, nil, nil, "(newid())", "DF_orders_guid", nil, false),
		"PK_orders", "id")
	mock.ExpectQuery(tableForeignKeysSQL).WithArgs("[dbo].[orders]").WillReturnRows(foreignKeyRows().
		AddRow("FK_orders_customer", "sales", "customers", "customer_id", "id", "CASCADE", "NO_ACTION", false, false, false))
	mock.ExpectQuery(tableIdentitySQL).WithArgs("[dbo].[orders]").WillReturnRows(names("last_value"))
	mock.ExpectQuery(insertableColumnSQL).WithArgs("[dbo].[orders]").WillReturnRows(names("name", "id", "customer_id", "payload", "guid"))
	expectColumns(mock, "dbo", "orders", [2]string{"id", "bigint"}, [2]string{"customer_id", "int"}, [2]string{"total", "int"},
		[2]string{"payload", "varbinary"}, [2]string{"rv", "timestamp"}, [2]string{"guid", "uniqueidentifier"})
	mock.ExpectQuery("SELECT * FROM [dbo].[orders]").WillReturnRows(
		sqlmock.NewRows([]string{"id", "customer_id", "total", "payload", "rv", "guid"}).
			AddRow(int64(9007199254740993), int64(100), int64(200), []byte("\xff\x00\xfe"), []byte("\x00\x00\x00\x00\x00\x00\x07\xd1"), []byte(guidRaw)))

	expectCustomersStructure(mock)
	mock.ExpectQuery(insertableColumnSQL).WithArgs("[sales].[customers]").WillReturnRows(names("name", "id", "parent_id", "name"))
	expectColumns(mock, "sales", "customers", [2]string{"id", "int"}, [2]string{"parent_id", "int"}, [2]string{"name", "nvarchar"})
	mock.ExpectQuery("SELECT * FROM [sales].[customers]").WillReturnRows(sqlmock.NewRows([]string{"id", "parent_id", "name"}).
		AddRow(int64(100), nil, "multi\nGO\nline O'Reilly").AddRow(int64(105), int64(100), "中文"))

	expectView(mock, "dbo.v_named", "[dbo].[v_named]",
		"CREATE VIEW dbo.v_named AS\nSELECT o.id, n.name FROM dbo.orders o JOIN sales.v_names n ON n.id = o.customer_id\n",
		"dbo.orders", "sales.v_names")
	expectView(mock, "sales.v_names", "[sales].[v_names]", "CREATE VIEW sales.v_names AS SELECT id, name FROM sales.customers",
		"sales.customers")

	mock.ExpectQuery(objectDefinitionSQL).WithArgs("dbo.f_net").WillReturnRows(names("definition",
		"CREATE FUNCTION dbo.f_net(@price decimal(18, 4)) RETURNS decimal(18, 4)\nAS\nBEGIN\n  RETURN @price * 0.9;\nEND\r\n\r\n"))
	mock.ExpectQuery(objectDefinitionSQL).WithArgs("sales.p_rename").WillReturnRows(names("definition",
		"CREATE PROCEDURE sales.p_rename @id int, @name nvarchar(100) AS\nUPDATE sales.customers SET name = @name WHERE id = @id;"))
	expectTableTrigger(mock)
	mock.ExpectQuery(triggerSourceSQL).WithArgs("tr_ddl", "[dbo].[tr_ddl]").
		WillReturnRows(names("definition", "CREATE TRIGGER tr_ddl ON DATABASE FOR CREATE_TABLE AS PRINT 'ddl'"))
	mock.ExpectQuery(triggerStateSQL).WithArgs("tr_ddl", "[dbo].[tr_ddl]").
		WillReturnRows(triggerStateRows().AddRow(int64(0), true, nil, nil))

	var out strings.Builder
	result, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "appdb", Whole: true, WithData: true, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, readGolden(t, "dump_whole.sql"), out.String())
	assert.Equal(t, 8, result.Objects)
	assert.EqualValues(t, 3, result.Rows)
}

// 转储整个 schema：只有这个 schema 的表、视图、过程与触发器；别的 schema 的对象与不属于 schema 的库级 DDL 触发器
// 都不写，也不查。
func TestDumpOfASchemaKeepsToTheSchema(t *testing.T) {
	session, mock := newMock(t)
	expectSampleObjects(mock)
	expectCustomersStructure(mock)
	expectView(mock, "sales.v_names", "[sales].[v_names]", "CREATE VIEW sales.v_names AS SELECT id, name FROM sales.customers",
		"sales.customers")
	mock.ExpectQuery(objectDefinitionSQL).WithArgs("sales.p_rename").WillReturnRows(names("definition",
		"CREATE PROCEDURE sales.p_rename AS SELECT 1"))
	expectTableTrigger(mock)

	var out strings.Builder
	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "appdb", Schema: "sales", Whole: true, Header: dumpHeader(), Writer: &out,
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, []string{
		"-- Table structure for sales.customers",
		"-- Foreign keys",
		"-- View structure for sales.v_names",
		"-- Procedure structure for sales.p_rename",
		"-- Trigger structure for sales.tr_customers_upd",
		"-- Sequence and auto-increment values",
	}, regexp.MustCompile(`(?m)^-- (Table|View|Procedure|Function|Trigger) structure for .*$|^-- Foreign keys$|^-- Sequence and auto-increment values$`).
		FindAllString(out.String(), -1))
	assert.NotContains(t, out.String(), "dbo")
	assert.NotContains(t, out.String(), "IDENTITY_INSERT", "structure only writes no data")
}

// 视图建有 WITH ENCRYPTION 时服务器不给原文：转储停在这个视图上并说明原因，而不是写出按列拼的建表语句。
func TestDumpStopsAtAViewWithoutADefinition(t *testing.T) {
	session, mock := newMock(t)
	mock.ExpectQuery(listViewsSQL).WillReturnRows(names("v", "dbo.v_secret"))
	mock.ExpectQuery(listProceduresSQL).WillReturnRows(names("p"))
	mock.ExpectQuery(listFunctionsSQL).WillReturnRows(names("f"))
	mock.ExpectQuery(listTriggersSQL).WillReturnRows(names("t"))
	expectTableDDL(mock, "[dbo].[v_secret]", ddlColumnRows().
		AddRow("id", "int", int64(4), int64(10), int64(0), false, nil, nil, nil, nil, nil, false), "", "")
	mock.ExpectQuery(objectDefinitionSQL).WithArgs("[dbo].[v_secret]").
		WillReturnRows(sqlmock.NewRows([]string{"definition"}).AddRow(nil))

	var out strings.Builder
	_, err := session.Dump(context.Background(), dbdriver.DumpRequest{
		Database: "appdb", Tables: []string{"dbo.v_secret"}, Header: dumpHeader(), Writer: &out,
	})
	var objectErr *dbdriver.DumpObjectError
	require.ErrorAs(t, err, &objectErr)
	assert.Equal(t, "dbo.v_secret", objectErr.Object)
	assert.Contains(t, err.Error(), "WITH ENCRYPTION")
	require.NoError(t, mock.ExpectationsWereMet())
}
