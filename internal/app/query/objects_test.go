package query

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/query_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 树的浏览（列库、列表、表元数据、对象列表、对象源码）由驱动在自己的会话上完成，
// 假驱动的结果原样呈现；它的会话不是 *sql.DB，也没有任何语句经过共享代码。
func TestBrowsingRunsOnTheDriverSession(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	databases, err := q.ListDatabases(1)
	require.NoError(t, err)
	assert.Equal(t, fake.Databases, databases)

	tables, err := q.ListTables(1, "reporting")
	require.NoError(t, err)
	assert.Equal(t, fake.Tables["reporting"], tables)

	meta, err := q.GetTableMetadata(1, "reporting", "users")
	require.NoError(t, err)
	assertSameJSON(t, fake.Metadata, meta)

	objects, err := q.ListDatabaseObjects(1, "reporting")
	require.NoError(t, err)
	assertSameJSON(t, fake.Objects, objects)

	source, err := q.GetObjectSource(1, "reporting", "view", "v_users", "")
	require.NoError(t, err)
	assert.Equal(t, fake.Sources["view:v_users"], source)

	sessions := fake.Sessions()
	require.NotEmpty(t, sessions)
	for _, s := range sessions {
		assert.Empty(t, s.Statements(), "the driver answers browsing itself; no statement goes through Execute")
	}
}

// 驱动没提供的操作明确报「不支持」，且不执行任何语句。
func TestBrowsingRefusesWhatTheDriverDoesNotProvide(t *testing.T) {
	fake := dbdrivertest.NewFake() // 只声明了视图，没有存储过程
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.GetObjectSource(1, "", "procedure", "p_users", "")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
	}
}

// 数据库返回的错误原样到达调用方。
func TestBrowsingReturnsTheDriversError(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.BrowseErr = assert.AnError
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.ListDatabases(1)
	require.ErrorIs(t, err, fake.BrowseErr)
	_, err = q.ListTables(1, "x")
	require.ErrorIs(t, err, fake.BrowseErr)
}

// 库里一张表也没有、资产上没有可浏览的库：交给前端的是空列表，不是 null——它直接遍历结果。
func TestBrowsingReturnsEmptyListsNotNull(t *testing.T) {
	requireEmptyJSONList := func(t *testing.T, names []string, err error) {
		t.Helper()
		require.NoError(t, err)
		payload, err := json.Marshal(names)
		require.NoError(t, err)
		assert.Equal(t, "[]", string(payload))
	}

	t.Run("a built-in driver's database without tables", func(t *testing.T) {
		q := newPanelQuery(t, &asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "empty.db"),
		})
		tables, err := q.ListTables(1, "")
		requireEmptyJSONList(t, tables, err)
	})

	t.Run("a driver that lists nothing", func(t *testing.T) {
		fake := dbdrivertest.NewFake()
		fake.Databases = nil
		fake.Tables = nil
		dbdrivertest.Register(t, fake)
		q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

		databases, err := q.ListDatabases(1)
		requireEmptyJSONList(t, databases, err)
		tables, err := q.ListTables(1, "reporting")
		requireEmptyJSONList(t, tables, err)
	})
}

// 绑定返回的类型与驱动的类型是两套定义；它们序列化给前端的 JSON 必须一致。
func assertSameJSON(t *testing.T, want, got any) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantJSON), string(gotJSON))
}

// 查看建表语句与起始查询语句由驱动在自己的会话上生成：假驱动的会话不是 *sql.DB，到它手里的是
// 库名、表名与行数，没有任何语句。
func TestTableTextsAreProducedByTheDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapViewDDL)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	ddl, err := q.GetTableDDL(1, "reporting", "events")
	require.NoError(t, err)
	assert.Equal(t, fake.DDL, ddl)

	starter, err := q.GetStarterQuery(1, "reporting", "events", 100)
	require.NoError(t, err)
	assert.Equal(t, "FAKE SELECT FIRST 100 FROM reporting.events", starter)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []dbdrivertest.TableCall{
		{Op: "ddl", Database: "reporting", Table: "events"},
		{Op: "starter", Database: "reporting", Table: "events", Page: dbdriver.Page{Size: 100}},
	}, sessions[0].TableCalls())
	assert.Empty(t, sessions[0].Statements())
}

// 没有声明查看建表语句的驱动明确报「不支持」，且不执行任何语句；数据库的错误原样到达调用方。
func TestTableDDLRefusalAndErrors(t *testing.T) {
	fake := dbdrivertest.NewFake() // 没有声明 view_ddl
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.GetTableDDL(1, "", "events")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
		assert.Empty(t, s.TableCalls())
	}

	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapViewDDL)
	fake.TableErr = errors.New(`fakedb: permission denied for table "events"`)
	_, err = q.GetTableDDL(1, "", "events")
	require.ErrorIs(t, err, fake.TableErr)
}

// 复制 / 导出为 SQL 的语句由驱动在自己的会话上生成：前端发来的请求（JSON，键名与前端一致）
// 原样到达驱动，驱动给的语句原样返回，没有任何语句被执行。
func TestRowStatementsAreProducedByTheDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapExportSQL)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	var request dbdriver.RowStatementsRequest
	require.NoError(t, json.Unmarshal([]byte(
		`{"kind":"insert","columns":["id","name"],"rows":[{"id":1,"name":"alice"},{"id":2,"name":null}],"primaryKeys":["id"]}`,
	), &request))
	statements, err := q.GetRowStatements(1, "reporting", "events", request)
	require.NoError(t, err)
	assert.Equal(t, []string{"FAKE INSERT ROW 1 OF reporting.events", "FAKE INSERT ROW 2 OF reporting.events"}, statements)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []dbdrivertest.TableCall{
		{Op: "prepare_row_statements", Database: "reporting", Table: "events"},
		{Op: "row_statements", Database: "reporting", Table: "events", RowStatements: &dbdriver.RowStatementsRequest{
			Kind:        dbdriver.RowInsert,
			Columns:     []string{"id", "name"},
			Rows:        []map[string]any{{"id": float64(1), "name": "alice"}, {"id": float64(2), "name": nil}},
			PrimaryKeys: []string{"id"},
		}},
	}, sessions[0].TableCalls())
	assert.Empty(t, sessions[0].Statements())
}

// 表格编辑的语句由驱动在自己的会话上生成：前端发来的请求（JSON，键名与前端一致）原样到达驱动，
// 驱动给的三组语句原样返回，没有任何语句被执行。
func TestGridEditStatementsAreProducedByTheDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapGridEdit)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	var request dbdriver.GridEditRequest
	require.NoError(t, json.Unmarshal([]byte(`{"columns":["id","name"],"primaryKeys":["id"],`+
		`"inserts":[{"values":[{"column":"name","value":"dan"}]},{"values":[]}],`+
		`"updates":[{"row":{"id":1,"name":"alice"},"set":[{"column":"name","value":null}]}],`+
		`"deletes":[{"id":2,"name":"bob"}]}`), &request))
	statements, err := q.GetGridEditStatements(1, "reporting", "events", request)
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.GridEditStatements{
		Inserts: []string{"FAKE INSERT ROW 1 OF reporting.events", "FAKE INSERT ROW 2 OF reporting.events"},
		Updates: []string{"FAKE UPDATE ROW 1 OF reporting.events"},
		Deletes: []string{"FAKE DELETE ROW 1 OF reporting.events"},
	}, statements)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []dbdrivertest.TableCall{{Op: "grid_edit", Database: "reporting", Table: "events", GridEdit: &dbdriver.GridEditRequest{
		Columns:     []string{"id", "name"},
		PrimaryKeys: []string{"id"},
		Inserts: []dbdriver.GridInsert{
			{Values: []dbdriver.ColumnValue{{Column: "name", Value: "dan"}}},
			{Values: []dbdriver.ColumnValue{}},
		},
		Updates: []dbdriver.GridUpdate{{
			Row: map[string]any{"id": float64(1), "name": "alice"},
			Set: []dbdriver.ColumnValue{{Column: "name", Value: nil}},
		}},
		Deletes: []map[string]any{{"id": float64(2), "name": "bob"}},
	}}}, sessions[0].TableCalls())
	assert.Empty(t, sessions[0].Statements())
}

// 没有声明表格编辑的驱动明确报「不支持」，且不执行任何语句。
func TestGridEditStatementsRefusedWhenTheDriverDoesNotDeclareGridEdit(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.GetGridEditStatements(1, "", "events", dbdriver.GridEditRequest{
		Columns: []string{"id"}, Deletes: []map[string]any{{"id": float64(1)}},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
		assert.Empty(t, s.TableCalls())
	}
}

// 导入的语句由驱动在自己的会话上生成：前端发来的请求（JSON，键名与前端一致）原样到达驱动，驱动
// 给的语句原样返回，没有任何语句被执行。
func TestImportStatementsAreProducedByTheDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapImport)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	var request dbdriver.ImportRequest
	require.NoError(t, json.Unmarshal([]byte(`{"mode":"append-update","columns":["id","name","avatar"],"keys":["id"],`+
		`"rows":[[{"kind":"text","text":"1"},{"kind":"null"},{"kind":"binary","hex":"00ff"}],`+
		`[{"kind":"text","text":"2"},{"kind":"text","text":""},{"kind":"binary"}]],`+
		`"extendedInsert":true,"maxStatementSizeKb":64}`), &request))
	statements, err := q.GetImportStatements(1, "reporting", "events", request)
	require.NoError(t, err)
	assert.Equal(t, []string{"FAKE IMPORT APPEND-UPDATE ROW 1 OF reporting.events", "FAKE IMPORT APPEND-UPDATE ROW 2 OF reporting.events"}, statements)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []dbdrivertest.TableCall{{Op: "import", Database: "reporting", Table: "events", Import: &dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppendUpdate, Columns: []string{"id", "name", "avatar"}, Keys: []string{"id"},
		Rows: [][]dbdriver.ImportValue{
			{{Kind: dbdriver.ImportText, Text: "1"}, {Kind: dbdriver.ImportNull}, {Kind: dbdriver.ImportBinary, Hex: "00ff"}},
			{{Kind: dbdriver.ImportText, Text: "2"}, {Kind: dbdriver.ImportText}, {Kind: dbdriver.ImportBinary}},
		},
		ExtendedInsert: true, MaxStatementSizeKB: 64,
	}}}, sessions[0].TableCalls())
	assert.Empty(t, sessions[0].Statements())
}

// 没有声明导入的驱动明确报「不支持」，且不执行任何语句。
func TestImportStatementsRefusedWhenTheDriverDoesNotDeclareImport(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.GetImportStatements(1, "", "events", dbdriver.ImportRequest{
		Mode: dbdriver.ImportAppend, Columns: []string{"id"}, Rows: [][]dbdriver.ImportValue{{{Kind: dbdriver.ImportText, Text: "1"}}},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
		assert.Empty(t, s.TableCalls())
	}
}

// 建表、删表、清空、建库的语句由驱动在自己的会话上生成：前端发来的请求（JSON，键名与前端一致）
// 原样到达驱动，驱动给的语句原样返回，没有任何语句被执行。
func TestSchemaStatementsAreProducedByTheDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities,
		dbdriver.CapCreateTable, dbdriver.CapDropTable, dbdriver.CapTruncateTable, dbdriver.CapCreateDatabase)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	var create dbdriver.CreateTableRequest
	require.NoError(t, json.Unmarshal([]byte(
		`{"name":"users","columns":[{"name":"id","type":"INT","nullable":false,"defaultValue":"1"}]}`), &create))
	statements, err := q.GetCreateTableStatements(1, "reporting", create)
	require.NoError(t, err)
	assert.Equal(t, []string{"FAKE CREATE TABLE reporting.users"}, statements)

	statements, err = q.GetDropTableStatements(1, "reporting", "users")
	require.NoError(t, err)
	assert.Equal(t, []string{"FAKE DROP TABLE reporting.users"}, statements)

	statements, err = q.GetTruncateTableStatements(1, "reporting", "users")
	require.NoError(t, err)
	assert.Equal(t, []string{"FAKE TRUNCATE TABLE reporting.users"}, statements)

	var createDB dbdriver.CreateDatabaseRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"reports","options":{"collation":"c"}}`), &createDB))
	statements, err = q.GetCreateDatabaseStatements(1, "app", createDB)
	require.NoError(t, err)
	assert.Equal(t, []string{"FAKE CREATE DATABASE reports"}, statements)

	var calls []dbdrivertest.TableCall
	for _, session := range fake.Sessions() {
		calls = append(calls, session.TableCalls()...)
		assert.Empty(t, session.Statements())
	}
	assert.ElementsMatch(t, []dbdrivertest.TableCall{
		{Op: "create_table", Database: "reporting", Table: "users", CreateTable: &dbdriver.CreateTableRequest{
			Name: "users", Columns: []dbdriver.NewColumn{{Name: "id", Type: "INT", DefaultValue: "1"}},
		}},
		{Op: "drop_table", Database: "reporting", Table: "users"},
		{Op: "truncate_table", Database: "reporting", Table: "users"},
		{Op: "create_database", Table: "reports", CreateDatabase: &dbdriver.CreateDatabaseRequest{
			Name: "reports", Options: map[string]string{"collation": "c"},
		}},
	}, calls)
}

// 没有声明对应功能的驱动明确报「不支持」，且不执行任何语句。
func TestSchemaStatementsRefusedWhenTheDriverDoesNotDeclareThem(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.GetCreateTableStatements(1, "", dbdriver.CreateTableRequest{Name: "t"})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = q.GetDropTableStatements(1, "", "t")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = q.GetTruncateTableStatements(1, "", "t")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = q.GetCreateDatabaseStatements(1, "", dbdriver.CreateDatabaseRequest{Name: "d"})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
		assert.Empty(t, s.TableCalls())
	}
}

// 改表：对话框打开时的表与改表语句都由驱动在自己的会话上给出；前端发来的请求（JSON，键名与前端
// 一致）原样到达驱动，没有任何语句被执行。没有声明改表的驱动明确报「不支持」，同样不执行任何语句。
func TestAlterTableIsProducedByTheDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	_, err := q.GetAlterTableModel(1, "app", "users")
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	_, err = q.GetAlterTableStatements(1, "app", "users", dbdriver.AlterTableRequest{Name: "people"})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)

	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapAlterTable)
	model, err := q.GetAlterTableModel(1, "app", "users")
	require.NoError(t, err)
	assert.Equal(t, "users", model.Name)
	assert.Len(t, model.Columns, len(fake.Metadata.Columns))

	var request dbdriver.AlterTableRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"people","comment":"c","columns":[`+
		`{"originalName":"id","name":"id","type":"int","nullable":false,"defaultValue":"","comment":"pk"},`+
		`{"originalName":"","name":"age","type":"INT","nullable":true,"defaultValue":"1","comment":""}],"drops":["name"]}`), &request))
	result, err := q.GetAlterTableStatements(1, "app", "users", request)
	require.NoError(t, err)
	assert.Equal(t, &dbdriver.AlterTableStatements{Statements: []string{"FAKE ALTER TABLE app.users"}, Table: "people"}, result)

	var calls []dbdrivertest.TableCall
	for _, session := range fake.Sessions() {
		calls = append(calls, session.TableCalls()...)
		assert.Empty(t, session.Statements())
	}
	assert.ElementsMatch(t, []dbdrivertest.TableCall{
		{Op: "alter_model", Database: "app", Table: "users"},
		{Op: "alter_table", Database: "app", Table: "users", AlterTable: &dbdriver.AlterTableRequest{
			Name: "people", Comment: "c",
			Columns: []dbdriver.AlterColumn{
				{OriginalName: "id", Name: "id", Type: "int", Comment: "pk"},
				{Name: "age", Type: "INT", Nullable: true, DefaultValue: "1"},
			},
			Drops: []string{"name"},
		}},
	}, calls)
}

// 导入的整批执行经资产的驱动会话：前端发来的请求（JSON，键名与前端一致）连同目标表到达驱动，
// 复制模式放进一个事务；驱动的执行结果按语句种类计成新增 / 更新 / 删除。
func TestExecuteTableImportRunsTheBatchOnTheDriverSession(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapImport)
	fake.Desc.CanDisableForeignKeyChecks = true
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	var request query_svc.TableImportBatchRequest
	require.NoError(t, json.Unmarshal([]byte(
		`{"statements":["DELETE FROM users;","INSERT INTO users VALUES (1);"],"mode":"copy","continueOnError":true,"disableForeignKeyChecks":true,"table":"dbo.users"}`,
	), &request))
	result, err := q.ExecuteTableImport(1, "reporting", request)
	require.NoError(t, err)
	// 假会话的每条写语句都改动表里的 3 行。
	assert.Equal(t, &query_svc.TableImportBatchResult{Processed: 2, Added: 3, Deleted: 3}, result)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []dbdrivertest.TableCall{{Op: "batch", Database: "reporting", Table: "dbo.users", Batch: &dbdriver.BatchRequest{
		Statements:              []string{"DELETE FROM users;", "INSERT INTO users VALUES (1);"},
		Atomic:                  true,
		ContinueOnError:         true,
		DisableForeignKeyChecks: true,
		Database:                "reporting",
		Table:                   "dbo.users",
	}}}, sessions[0].TableCalls())
	assert.Equal(t, []string{"DELETE FROM users;", "INSERT INTO users VALUES (1);"}, sessions[0].Statements())
}

// 没有声明能关闭外键检查的驱动明确报「不支持」，且不执行任何语句。
func TestExecuteTableImportRefusesForeignKeyToggleTheDriverDoesNotDeclare(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Capabilities = append(fake.Desc.Capabilities, dbdriver.CapImport)
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.ExecuteTableImport(1, "", query_svc.TableImportBatchRequest{
		Statements: []string{"INSERT INTO users VALUES (1);"}, Mode: "append", ContinueOnError: true,
		DisableForeignKeyChecks: true, Table: "users",
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
	}
}

// 没有声明复制 / 导出为 SQL 的驱动明确报「不支持」，且不执行任何语句。
func TestRowStatementsRefusedWhenTheDriverDoesNotDeclareThem(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.GetRowStatements(1, "", "events", dbdriver.RowStatementsRequest{
		Kind: dbdriver.RowInsert, Columns: []string{"id"}, Rows: []map[string]any{{"id": float64(1)}},
	})
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	for _, s := range fake.Sessions() {
		assert.Empty(t, s.Statements())
		assert.Empty(t, s.TableCalls())
	}
}
