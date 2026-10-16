package query

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/query_svc"
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
	assert.Equal(t, []dbdrivertest.TableCall{{Op: "row_statements", Database: "reporting", Table: "events", RowStatements: &dbdriver.RowStatementsRequest{
		Kind:        dbdriver.RowInsert,
		Columns:     []string{"id", "name"},
		Rows:        []map[string]any{{"id": float64(1), "name": "alice"}, {"id": float64(2), "name": nil}},
		PrimaryKeys: []string{"id"},
	}}}, sessions[0].TableCalls())
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
