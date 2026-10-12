package query

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/dbdriver"
	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
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
	require.ErrorIs(t, err, dbdriver.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")

	_, err = q.ExecuteSQLPaged(1, "SELECT 1", "", 0, 10)
	require.ErrorIs(t, err, dbdriver.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
}

// 还没迁到驱动上的操作（打开表、XLSX 导出）经过渡桥拿 *sql.DB：database/sql 之上的内置驱动照常工作……
func TestUnmigratedOperationsStillWorkForDatabaseSQLDrivers(t *testing.T) {
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "panel.db"),
	})
	_, err := q.ExecuteSQL(1, "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)", "")
	require.NoError(t, err)

	meta, err := q.GetTableMetadata(1, "", "users")
	require.NoError(t, err)
	require.Len(t, meta.Columns, 2)
	assert.Equal(t, "id", meta.Columns[0].Name)
	assert.True(t, meta.Columns[0].PrimaryKey)

	opened, err := q.OpenTable(1, "", "users", 10)
	require.NoError(t, err)
	assert.Contains(t, opened, `"name"`)
}

// ……而会话不是 *sql.DB 的驱动明确报「不支持」，不会 panic，也不会被当成别的驱动。
func TestUnmigratedOperationsAreUnsupportedForOtherSessions(t *testing.T) {
	dbdrivertest.Register(t, dbdrivertest.NewFake())
	q := newPanelQuery(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := q.OpenTable(1, "", "users", 10)
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
	err = q.ExportTableToXlsx(1, "", "SELECT * FROM users", filepath.Join(t.TempDir(), "out.xlsx"))
	require.ErrorIs(t, err, dbdriver.ErrUnsupported)
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
		require.ErrorIs(t, err, dbdriver.ErrDriverUnavailable)
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
