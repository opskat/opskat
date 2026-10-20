package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/query_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

type stubLang struct{}

func (stubLang) Lang() string { return "en" }

// newPanelQuery 造一个只装了数据库面板缓存的 Query，并让资产 1 是一份用 cfg 的数据库资产。
func newPanelQuery(t *testing.T, cfg *asset_entity.DatabaseConfig) *Query {
	t.Helper()
	asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase}
	require.NoError(t, asset.SetDatabaseConfig(cfg))

	repo := mock_asset_repo.NewMockAssetRepo(gomock.NewController(t))
	repo.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(repo)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })

	q := &Query{
		ctx:          context.Background(),
		lang:         stubLang{},
		dbPanelCache: newPanelConnCache[dbdriver.Session]("database", time.Minute),
	}
	t.Cleanup(func() { _ = q.dbPanelCache.Close() })
	return q
}

// 查询面板执行 SQL：连接与执行都经资产的驱动。假驱动的会话不是 *sql.DB。
func TestExecuteSQLRunsThroughTheAssetsDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	result, err := q.ExecuteSQL(1, "SELECT * FROM t", "reporting")
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"columns":["id","name"],"rows":[{"id":1,"name":"alice"},{"id":2,"name":"bob"},{"id":3,"name":"carol"}],"count":3}`,
		result)

	result, err = q.ExecuteSQL(1, "DELETE FROM t", "reporting")
	require.NoError(t, err)
	assert.Equal(t, `{"affected_rows":3}`, result)

	// 面板缓存里放的就是驱动的会话：同一个库的两次执行共用一条，换库另开一条。
	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, "reporting", sessions[0].Request.Config.Database)
	assert.Equal(t, []string{"SELECT * FROM t", "DELETE FROM t"}, sessions[0].Statements())
	assert.False(t, sessions[0].Closed())

	_, err = q.ExecuteSQL(1, "SELECT * FROM t", "")
	require.NoError(t, err)
	sessions = fake.Sessions()
	require.Len(t, sessions, 2)
	assert.Equal(t, "app", sessions[1].Request.Config.Database)

	// 资产配置变更时面板丢弃它的连接：会话由缓存关闭。
	q.dbPanelCache.DropAsset(1)
	assert.True(t, sessions[0].Closed())
	assert.True(t, sessions[1].Closed())
}

func TestExecuteSQLPagedRunsThroughTheAssetsDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	result, err := q.ExecuteSQLPaged(1, "SELECT * FROM t", "", 1, 2)
	require.NoError(t, err)
	assert.JSONEq(t, `{"columns":["id","name"],"rows":[{"id":3,"name":"carol"}],"count":1,"total_count":3}`, result)

	result, err = q.ExecuteSQLPaged(1, "SELECT * FROM t", "", 0, 2)
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"columns":["id","name"],"rows":[{"id":1,"name":"alice"},{"id":2,"name":"bob"}],"count":2,"total_count":3}`,
		result)
	assert.Len(t, fake.Sessions(), 1)
}

func TestPanelReturnsTheDriversConnectionError(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.OpenErr = errors.New("fakedb: connection refused")
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.ExecuteSQL(1, "SELECT * FROM t", "")
	require.ErrorIs(t, err, fake.OpenErr)
}

// 资产配置的驱动没注册：打开查询面板（执行、分页执行）报「驱动不可用」并点名驱动。
func TestPanelRejectsUnregisteredDriver(t *testing.T) {
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "oracle", Host: "127.0.0.1", Port: 1521, Username: "u"})

	_, err := q.ExecuteSQL(1, "SELECT 1", "")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")

	_, err = q.ExecuteSQLPaged(1, "SELECT 1", "", 0, 10)
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
}

// XLSX 导出由驱动在自己的会话上取数：假驱动的会话不是 *sql.DB，到它手里的是结构化的筛选
// 条件与排序项；它给出的行原样写进工作簿，没有任何语句。
func TestExportTableToXlsxRunsOnTheDriverSession(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})
	filters := []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpContains, Value: "a"}}
	sorts := []dbdriver.SortItem{{Column: "name", Direction: dbdriver.SortDescending}}

	path := filepath.Join(t.TempDir(), "out.xlsx")
	err := q.ExportTableToXlsx(1, "reporting", "users", query_svc.TableExportRequest{
		Filters: filters, Sorts: sorts, Scope: query_svc.ExportScopePage, Page: 0, PageSize: 2,
	}, path)
	require.NoError(t, err)

	file, err := os.Open(path) //nolint:gosec // test temp file
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	parsed, err := query_svc.ParseXlsx(file, "")
	require.NoError(t, err)
	assert.Equal(t, fake.Columns, parsed.Headers)
	assert.Equal(t, [][]string{{"1", "alice"}, {"2", "bob"}}, parsed.Rows)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []dbdrivertest.TableCall{
		{Op: "rows", Database: "reporting", Table: "users", Query: dbdriver.TableQuery{Filters: filters, Sorts: sorts}, Page: dbdriver.Page{Index: 0, Size: 2}},
	}, sessions[0].TableCalls())
	assert.Empty(t, sessions[0].Statements())
}

// 导出时驱动返回的错误（数据库错误、不支持）原样到达调用方，不执行任何语句。
func TestExportTableToXlsxReturnsTheDriversError(t *testing.T) {
	for name, tableErr := range map[string]error{
		"a database error":            errors.New("fakedb: relation \"users\" does not exist"),
		"a driver without table rows": fmt.Errorf("%w: fakedb does not browse tables", dbdriver.ErrUnsupported),
	} {
		t.Run(name, func(t *testing.T) {
			fake := dbdrivertest.NewFake()
			fake.TableErr = tableErr
			dbdrivertest.Register(t, fake)
			q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

			err := q.ExportTableToXlsx(1, "", "users", query_svc.TableExportRequest{Scope: query_svc.ExportScopeAll}, filepath.Join(t.TempDir(), "out.xlsx"))
			require.ErrorIs(t, err, tableErr)
			for _, s := range fake.Sessions() {
				assert.Empty(t, s.Statements())
			}
		})
	}
}

// 内置驱动导出的行与列：SQLite 真库上带筛选与排序，只写选中的列。
func TestExportTableToXlsxWorksForABuiltInDriver(t *testing.T) {
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	for _, statement := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)",
		"INSERT INTO users (id, name) VALUES (1, 'alice'), (2, 'bob'), (3, 'carol')",
	} {
		_, err := q.ExecuteSQL(1, statement, "")
		require.NoError(t, err)
	}

	path := filepath.Join(t.TempDir(), "out.xlsx")
	err := q.ExportTableToXlsx(1, "", "users", query_svc.TableExportRequest{
		Filters: []dbdriver.FilterItem{{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpNotEqual, Value: "alice"}},
		Sorts:   []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}},
		Columns: []string{"name", "id"},
		Scope:   query_svc.ExportScopeAll,
	}, path)
	require.NoError(t, err)

	file, err := os.Open(path) //nolint:gosec // test temp file
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	parsed, err := query_svc.ParseXlsx(file, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"name", "id"}, parsed.Headers)
	assert.Equal(t, [][]string{{"carol", "3"}, {"bob", "2"}}, parsed.Rows)
}

// 内置驱动走的是同一条路：SQLite 的真库上读表元数据、打开表、带筛选与排序翻页、计数。
func TestBrowsingWorksForABuiltInDriver(t *testing.T) {
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	for _, statement := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)",
		"INSERT INTO users (id, name) VALUES (1, 'alice'), (2, 'bob'), (3, 'carol')",
	} {
		_, err := q.ExecuteSQL(1, statement, "")
		require.NoError(t, err)
	}

	meta, err := q.GetTableMetadata(1, "", "users")
	require.NoError(t, err)
	require.Len(t, meta.Columns, 2)
	assert.Equal(t, "id", meta.Columns[0].Name)
	assert.True(t, meta.Columns[0].PrimaryKey)

	opened, err := q.OpenTable(1, "", "users", 2)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"columns": ["id", "name"],
		"columnTypes": {"id": "INTEGER", "name": "TEXT"},
		"columnCategories": {"id": "number", "name": "text"},
		"columnRules": [
			{"name": "id", "nullable": true, "hasDefault": false, "autoIncrement": false},
			{"name": "name", "nullable": true, "hasDefault": false, "autoIncrement": false}
		],
		"primaryKeys": ["id"],
		"totalCount": 3,
		"firstPage": [{"id": 1, "name": "alice"}, {"id": 2, "name": "bob"}],
		"pageSize": 2
	}`, opened)

	notAlice := []dbdriver.FilterItem{{
		Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "name", Operator: dbdriver.OpNotEqual, Value: "alice",
	}}
	rows, err := q.QueryTableRows(1, "", "users", query_svc.TableRowsRequest{
		Filters:  notAlice,
		Sorts:    []dbdriver.SortItem{{Column: "id", Direction: dbdriver.SortDescending}},
		Page:     0,
		PageSize: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"id", "name"}, rows.Columns)
	assert.Equal(t, []map[string]any{{"id": int64(3), "name": "carol"}, {"id": int64(2), "name": "bob"}}, rows.Rows)

	total, err := q.CountTableRows(1, "", "users", notAlice)
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
}

// 表浏览（打开表、翻页、计数）由驱动在自己的会话上完成：假驱动的会话不是 *sql.DB，到它
// 手里的是结构化的筛选条件与排序项，没有任何语句；它给出的结果原样呈现。
func TestTableBrowsingRunsOnTheDriverSession(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	opened, err := q.OpenTable(1, "reporting", "users", 2)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"columns": ["id", "name"],
		"columnTypes": {"id": "int", "name": "text"},
		"columnCategories": {"id": "number", "name": "text"},
		"columnRules": [
			{"name": "id", "nullable": false, "hasDefault": false, "autoIncrement": true},
			{"name": "name", "nullable": true, "hasDefault": false, "autoIncrement": false}
		],
		"primaryKeys": ["id"],
		"totalCount": 3,
		"firstPage": [{"id": 1, "name": "alice"}, {"id": 2, "name": "bob"}],
		"pageSize": 2
	}`, opened)

	filters := []dbdriver.FilterItem{
		{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinOr, Column: "name", Operator: dbdriver.OpContains, Value: "a"},
		{Kind: dbdriver.FilterGroup, Join: dbdriver.JoinAnd, Negated: true, Items: []dbdriver.FilterItem{
			{Kind: dbdriver.FilterCondition, Join: dbdriver.JoinAnd, Column: "id", Operator: dbdriver.OpBetween, Value: []any{1, 2}},
		}},
	}
	sorts := []dbdriver.SortItem{{Column: "name", Direction: dbdriver.SortDescending}}

	rows, err := q.QueryTableRows(1, "reporting", "users", query_svc.TableRowsRequest{Filters: filters, Sorts: sorts, Page: 1, PageSize: 2})
	require.NoError(t, err)
	assert.Equal(t, fake.Columns, rows.Columns)
	assert.Equal(t, []map[string]any{{"id": 3, "name": "carol"}}, rows.Rows, "the second page of two holds the third row")

	total, err := q.CountTableRows(1, "reporting", "users", filters)
	require.NoError(t, err)
	assert.Equal(t, int64(len(fake.Rows)), total)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []dbdrivertest.TableCall{
		{Op: "open", Database: "reporting", Table: "users", Page: dbdriver.Page{Index: 0, Size: 2}},
		{Op: "rows", Database: "reporting", Table: "users", Query: dbdriver.TableQuery{Filters: filters, Sorts: sorts}, Page: dbdriver.Page{Index: 1, Size: 2}},
		{Op: "count", Database: "reporting", Table: "users", Query: dbdriver.TableQuery{Filters: filters}},
	}, sessions[0].TableCalls())
	assert.Empty(t, sessions[0].Statements(), "the driver answers table browsing itself; no statement goes through Execute")
}

// 一页没有行时返回的是空列表，不是 null：前端直接取它的长度。
func TestQueryTableRowsReturnsAnEmptyListPastTheLastPage(t *testing.T) {
	dbdrivertest.Register(t, dbdrivertest.NewFake())
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	rows, err := q.QueryTableRows(1, "", "users", query_svc.TableRowsRequest{Page: 9, PageSize: 2})
	require.NoError(t, err)
	payload, err := json.Marshal(rows)
	require.NoError(t, err)
	assert.JSONEq(t, `{"columns":["id","name"],"rows":[]}`, string(payload))
}

// 打开表时没有给出每页行数（<= 0）：按 1000 行取第一页。
func TestOpenTableDefaultsThePageSize(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	opened, err := q.OpenTable(1, "", "users", 0)
	require.NoError(t, err)
	assert.Contains(t, opened, `"pageSize":1000`)
	assert.Equal(t, dbdriver.Page{Index: 0, Size: 1000}, fake.Sessions()[0].TableCalls()[0].Page)
}

// 驱动返回的错误原样到达调用方：数据库的错误，以及驱动不提供表浏览时的「不支持」——后者
// 没有执行任何语句。
func TestTableBrowsingReturnsTheDriversError(t *testing.T) {
	for name, tableErr := range map[string]error{
		"a database error":            errors.New("fakedb: relation \"users\" does not exist"),
		"a driver without table rows": fmt.Errorf("%w: fakedb does not browse tables", dbdriver.ErrUnsupported),
	} {
		t.Run(name, func(t *testing.T) {
			fake := dbdrivertest.NewFake()
			fake.TableErr = tableErr
			dbdrivertest.Register(t, fake)
			q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

			_, err := q.OpenTable(1, "", "users", 10)
			require.ErrorIs(t, err, tableErr)
			_, err = q.QueryTableRows(1, "", "users", query_svc.TableRowsRequest{PageSize: 10})
			require.ErrorIs(t, err, tableErr)
			_, err = q.CountTableRows(1, "", "users", nil)
			require.ErrorIs(t, err, tableErr)

			for _, s := range fake.Sessions() {
				assert.Empty(t, s.Statements())
			}
		})
	}
}

func TestQueryTimeout(t *testing.T) {
	t.Run("configured value wins", func(t *testing.T) {
		cfg := &asset_entity.DatabaseConfig{QueryTimeoutSeconds: 120}
		if got := queryTimeout(cfg); got != 120*time.Second {
			t.Fatalf("queryTimeout() = %v, want %v", got, 120*time.Second)
		}
	})
	t.Run("zero falls back to default", func(t *testing.T) {
		cfg := &asset_entity.DatabaseConfig{QueryTimeoutSeconds: 0}
		if got := queryTimeout(cfg); got != defaultQueryTimeout {
			t.Fatalf("queryTimeout() = %v, want %v", got, defaultQueryTimeout)
		}
	})
}

func TestPanelDBCacheKey(t *testing.T) {
	key := func(t *testing.T, cfg *asset_entity.DatabaseConfig) string {
		t.Helper()
		got, err := panelDBCacheKey(7, cfg)
		require.NoError(t, err)
		return got
	}

	t.Run("SQLite reuses one panel connection across schema names", func(t *testing.T) {
		cfg := &asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Database: "main"}
		if got := key(t, cfg); got != "7" {
			t.Fatalf("panelDBCacheKey() = %q, want %q", got, "7")
		}
		cfg.Database = ""
		if got := key(t, cfg); got != "7" {
			t.Fatalf("panelDBCacheKey() = %q, want %q", got, "7")
		}
	})

	t.Run("network databases keep database in the cache key", func(t *testing.T) {
		for _, driver := range []asset_entity.DatabaseDriver{
			asset_entity.DriverMySQL, asset_entity.DriverPostgreSQL, asset_entity.DriverMSSQL,
		} {
			cfg := &asset_entity.DatabaseConfig{Driver: driver, Database: "app"}
			if got := key(t, cfg); got != "7:app" {
				t.Fatalf("panelDBCacheKey(%s) = %q, want %q", driver, got, "7:app")
			}
			cfg.Database = ""
			if got := key(t, cfg); got != "7:" {
				t.Fatalf("panelDBCacheKey(%s) = %q, want %q", driver, got, "7:")
			}
		}
	})

	// 粒度是驱动声明的，不是按驱动名判断的：同一个假驱动改一下声明，键就跟着变。
	t.Run("a driver declares whether its sessions are per database", func(t *testing.T) {
		fake := dbdrivertest.NewFake()
		dbdrivertest.Register(t, fake)
		cfg := &asset_entity.DatabaseConfig{Driver: "fakedb", Database: "app"}
		assert.Equal(t, "7:app", key(t, cfg))

		fake.SessionPerAsset = true
		assert.Equal(t, "7", key(t, cfg))
	})

	t.Run("an unregistered driver has no key", func(t *testing.T) {
		_, err := panelDBCacheKey(7, &asset_entity.DatabaseConfig{Driver: "oracle", Database: "app"})
		require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	})
}

// 声明「一个资产一条会话」的驱动：面板换 schema 名执行仍共用同一条会话（远端 SQLite 只抢
// 一次文件锁靠的就是这个）。
func TestPanelSharesOneSessionAcrossDatabasesWhenTheDriverSaysSo(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.SessionPerAsset = true
	dbdrivertest.Register(t, fake)
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.ExecuteSQL(1, "SELECT * FROM t", "main")
	require.NoError(t, err)
	_, err = q.ExecuteSQLPaged(1, "SELECT * FROM t", "", 0, 10)
	require.NoError(t, err)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []string{"SELECT * FROM t", "SELECT * FROM t"}, sessions[0].Statements())
}

func TestFinishPanelDBOperation(t *testing.T) {
	t.Run("returns cleanup error when operation succeeds", func(t *testing.T) {
		cleanupErr := errors.New("remove lock failed")
		err := finishPanelDBOperation(nil, func() error { return cleanupErr })
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("finishPanelDBOperation() = %v, want cleanup error", err)
		}
	})

	t.Run("keeps both operation and cleanup errors", func(t *testing.T) {
		opErr := errors.New("query failed")
		cleanupErr := errors.New("remove lock failed")
		err := finishPanelDBOperation(opErr, func() error { return cleanupErr })
		if !errors.Is(err, opErr) {
			t.Fatalf("finishPanelDBOperation() = %v, want operation error", err)
		}
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("finishPanelDBOperation() = %v, want cleanup error", err)
		}
	})

	t.Run("returns operation error when cleanup succeeds", func(t *testing.T) {
		opErr := errors.New("query failed")
		err := finishPanelDBOperation(opErr, func() error { return nil })
		if !errors.Is(err, opErr) {
			t.Fatalf("finishPanelDBOperation() = %v, want operation error", err)
		}
	})
}
