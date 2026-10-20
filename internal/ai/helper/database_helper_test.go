package helper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 连接串、只读、分页方言与关闭顺序的断言随代码迁到了驱动一侧（pkg/dbdriver/sqldb 与
// 各驱动包）；这里测的是共享的这一段：经资产的驱动拿会话，把驱动给的结构化结果排成对外
// 的 JSON。假驱动的会话不是 *sql.DB，所以这些测试同时证明共享代码不依赖它。

func fakeDatabaseAsset(t *testing.T, cfg *asset_entity.DatabaseConfig) *asset_entity.Asset {
	t.Helper()
	asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeDatabase}
	require.NoError(t, asset.SetDatabaseConfig(cfg))
	return asset
}

func openFakeSession(t *testing.T, fake *dbdrivertest.Fake) dbdriver.Session {
	t.Helper()
	session, err := fake.Open(context.Background(), dbdriver.OpenRequest{
		Config: &dbdriver.Config{Driver: fake.Desc.ID, Host: "h"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestExecuteSQLFormatsTheDriversResult(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Columns = []string{"name", "nullable"}
	fake.Rows = [][]any{{"alice", nil}}
	session := openFakeSession(t, fake)

	result, err := ExecuteSQL(context.Background(), session, "SELECT name, nullable FROM t")
	require.NoError(t, err)
	// 行按列名成对象；没分页的结果不带 total_count。
	assert.JSONEq(t, `{"columns":["name","nullable"],"rows":[{"name":"alice","nullable":null}],"count":1}`, result)

	result, err = ExecuteSQL(context.Background(), session, "DELETE FROM t")
	require.NoError(t, err)
	assert.Equal(t, `{"affected_rows":1}`, result)
}

func TestExecuteSQLKeepsAnEmptyResultSetAsNullRows(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Rows = nil
	session := openFakeSession(t, fake)

	result, err := ExecuteSQL(context.Background(), session, "SELECT * FROM t")
	require.NoError(t, err)
	assert.JSONEq(t, `{"columns":["id","name"],"rows":null,"count":0}`, result)
}

func TestExecuteSQLPagedReportsThePageAndTheTotal(t *testing.T) {
	session := openFakeSession(t, dbdrivertest.NewFake())

	result, err := ExecuteSQLPaged(context.Background(), session, "SELECT * FROM t", 1, 2)
	require.NoError(t, err)
	assert.JSONEq(t, `{"columns":["id","name"],"rows":[{"id":3,"name":"carol"}],"count":1,"total_count":3}`, result)

	// 驱动没有分页的语句：照普通执行的格式返回，不带 total_count。
	result, err = ExecuteSQLPaged(context.Background(), session, "DELETE FROM t", 0, 2)
	require.NoError(t, err)
	assert.Equal(t, `{"affected_rows":3}`, result)
}

func TestExecuteSQLReturnsTheDriversError(t *testing.T) {
	session := openFakeSession(t, dbdrivertest.NewFake())
	require.NoError(t, session.Close())

	_, err := ExecuteSQL(context.Background(), session, "SELECT * FROM t")
	require.ErrorIs(t, err, dbdrivertest.ErrFakeSessionClosed)
	_, err = ExecuteSQLPaged(context.Background(), session, "SELECT * FROM t", 0, 10)
	require.ErrorIs(t, err, dbdrivertest.ErrFakeSessionClosed)
}

// AI / opsctl 执行 SQL：连接与执行都经资产的驱动；不带缓存时会话用完即关。
func TestExecSQLOnAssetRunsThroughTheAssetsDriver(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	asset := fakeDatabaseAsset(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777, Database: "app"})

	result, err := ExecSQLOnAsset(context.Background(), asset, "SELECT * FROM t", "reporting")
	require.NoError(t, err)
	assert.JSONEq(t,
		`{"columns":["id","name"],"rows":[{"id":1,"name":"alice"},{"id":2,"name":"bob"},{"id":3,"name":"carol"}],"count":3}`,
		result)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, "reporting", sessions[0].Request.Config.Database, "scope overrides the asset's default database")
	assert.Equal(t, []string{"SELECT * FROM t"}, sessions[0].Statements())
	assert.True(t, sessions[0].Closed(), "an uncached session is closed once the statement has run")
}

// 同一次 AI Send 里的多条语句共用一条会话，由缓存统一关闭。
func TestExecSQLOnAssetReusesTheCachedSession(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)
	asset := fakeDatabaseAsset(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})
	cache := NewDatabaseClientCache()
	ctx := WithDatabaseCache(context.Background(), cache)

	_, err := ExecSQLOnAsset(ctx, asset, "SELECT * FROM t", "")
	require.NoError(t, err)
	_, err = ExecSQLOnAsset(ctx, asset, "DELETE FROM t", "")
	require.NoError(t, err)

	sessions := fake.Sessions()
	require.Len(t, sessions, 1)
	assert.Equal(t, []string{"SELECT * FROM t", "DELETE FROM t"}, sessions[0].Statements())
	assert.False(t, sessions[0].Closed())

	require.NoError(t, cache.Close())
	assert.True(t, sessions[0].Closed())
}

func TestExecSQLOnAssetReturnsTheDriversConnectionError(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.OpenErr = assert.AnError
	dbdrivertest.Register(t, fake)
	asset := fakeDatabaseAsset(t, &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777})

	_, err := ExecSQLOnAsset(context.Background(), asset, "SELECT * FROM t", "")
	require.ErrorIs(t, err, assert.AnError)
}

// 资产配置的驱动没注册：AI / opsctl 执行 SQL 必须报「驱动不可用」并点名驱动。
func TestExecSQLOnAssetRejectsUnregisteredDriver(t *testing.T) {
	asset := fakeDatabaseAsset(t, &asset_entity.DatabaseConfig{
		Driver: "oracle", Host: "127.0.0.1", Port: 1521, Username: "u",
	})

	_, err := ExecSQLOnAsset(context.Background(), asset, "SELECT 1", "")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
}
