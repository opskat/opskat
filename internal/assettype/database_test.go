package assettype

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestDatabaseHandlerApplyCreateArgsSQLite(t *testing.T) {
	convey.Convey("SQLite ApplyCreateArgs 写入 Path 字段", t, func() {
		h := &databaseHandler{}
		a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
		err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
			"driver": "sqlite",
			"path":   "/tmp/x.db",
		})
		convey.So(err, convey.ShouldBeNil)
		cfg, err := a.GetDatabaseConfig()
		convey.So(err, convey.ShouldBeNil)
		convey.So(cfg.Driver, convey.ShouldEqual, asset_entity.DriverSQLite)
		convey.So(cfg.Path, convey.ShouldEqual, "/tmp/x.db")
		convey.So(cfg.Host, convey.ShouldEqual, "")
	})

	convey.Convey("SQLite ApplyCreateArgs 写入远端 VFS 字段", t, func() {
		h := &databaseHandler{}
		a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
		err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
			"driver":        "sqlite",
			"path":          "/tmp/x.db",
			"sqlite_source": "remote_ssh_vfs",
			"ssh_asset_id":  float64(9),
		})
		convey.So(err, convey.ShouldBeNil)
		cfg, err := a.GetDatabaseConfig()
		convey.So(err, convey.ShouldBeNil)
		convey.So(cfg.Driver, convey.ShouldEqual, asset_entity.DriverSQLite)
		convey.So(cfg.SQLiteSource, convey.ShouldEqual, asset_entity.SQLiteSourceRemoteSSHVFS)
		convey.So(cfg.SSHAssetID, convey.ShouldEqual, 9)
	})
}

func TestDatabaseHandlerSafeViewSQLite(t *testing.T) {
	convey.Convey("SQLite SafeView 返回 path 不返回 host", t, func() {
		h := &databaseHandler{}
		a := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
		_ = a.SetDatabaseConfig(&asset_entity.DatabaseConfig{
			Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db",
		})
		view := h.SafeView(a)
		convey.So(view["driver"], convey.ShouldEqual, "sqlite")
		convey.So(view["path"], convey.ShouldEqual, "/tmp/x.db")
		_, hasHost := view["host"]
		convey.So(hasHost, convey.ShouldBeFalse)
	})
}

func TestDatabaseHandler(t *testing.T) {
	convey.Convey("Database Handler", t, func() {
		h := &databaseHandler{}
		convey.Convey("SafeView", func() {
			a := &asset_entity.Asset{Type: "database", Status: 1}
			_ = a.SetDatabaseConfig(&asset_entity.DatabaseConfig{
				Driver: asset_entity.DriverMySQL, Host: "10.0.0.1", Port: 3306,
				Username: "admin", Password: "secret", Database: "mydb", ReadOnly: true,
			})
			view := h.SafeView(a)
			convey.So(view["host"], convey.ShouldEqual, "10.0.0.1")
			convey.So(view["port"], convey.ShouldEqual, 3306)
			convey.So(view["username"], convey.ShouldEqual, "admin")
			convey.So(view["driver"], convey.ShouldEqual, "mysql")
			convey.So(view["database"], convey.ShouldEqual, "mydb")
			convey.So(view["read_only"], convey.ShouldEqual, true)
			_, hasPassword := view["password"]
			convey.So(hasPassword, convey.ShouldBeFalse)
		})
		convey.Convey("ApplyCreateArgs", func() {
			a := &asset_entity.Asset{Type: "database"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"driver": "mysql", "host": "10.0.0.1", "port": float64(3306),
				"username": "admin", "database": "mydb", "read_only": "true",
				"ssh_asset_id": float64(42),
			})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetDatabaseConfig()
			convey.So(cfg.Driver, convey.ShouldEqual, asset_entity.DriverMySQL)
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.1")
			convey.So(cfg.Port, convey.ShouldEqual, 3306)
			convey.So(cfg.Database, convey.ShouldEqual, "mydb")
			convey.So(cfg.ReadOnly, convey.ShouldBeTrue)
			convey.So(cfg.SSHAssetID, convey.ShouldEqual, 42)
		})
		convey.Convey("ApplyCreateArgs requires driver", func() {
			a := &asset_entity.Asset{Type: "database"}
			err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
				"host": "10.0.0.1", "port": float64(3306),
			})
			convey.So(err, convey.ShouldNotBeNil)
		})
		convey.Convey("ApplyUpdateArgs", func() {
			a := &asset_entity.Asset{Type: "database"}
			_ = a.SetDatabaseConfig(&asset_entity.DatabaseConfig{
				Driver: asset_entity.DriverMySQL, Host: "10.0.0.1", Port: 3306,
				Username: "admin", Database: "mydb",
			})
			err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{"host": "10.0.0.2", "database": "newdb"})
			convey.So(err, convey.ShouldBeNil)
			cfg, _ := a.GetDatabaseConfig()
			convey.So(cfg.Host, convey.ShouldEqual, "10.0.0.2")
			convey.So(cfg.Port, convey.ShouldEqual, 3306)
			convey.So(cfg.Username, convey.ShouldEqual, "admin")
			convey.So(cfg.Database, convey.ShouldEqual, "newdb")
		})
	})
}

// 加一个驱动只需要注册：下面的假驱动只存在于测试里，共享代码里没有任何一行认识它。
func TestDatabaseHandlerServesADriverAddedByRegistrationAlone(t *testing.T) {
	dbdrivertest.Register(t, dbdrivertest.NewFake())
	h, ok := Get(asset_entity.AssetTypeDatabase)
	require.True(t, ok)

	// 创建：没有 username（内置网络驱动的规则会拒绝），端口由假驱动自己补默认值。
	prepared, err := PrepareCreate(asset_entity.AssetTypeDatabase, map[string]any{
		"driver": "fakedb", "host": "fake.internal", "database": "main", "password": "s3cret",
	})
	require.NoError(t, err)
	assert.Equal(t, dbdrivertest.FakeDefaultPort, prepared.Config["port"])
	assert.Equal(t, dbdrivertest.FakeDefaultPort, prepared.Approval["port"], "审批人看到的是将要落库的端口")

	a := &asset_entity.Asset{Name: "fake", Type: asset_entity.AssetTypeDatabase}
	require.NoError(t, h.ApplyCreateArgs(t.Context(), a, prepared.Config))
	require.NoError(t, a.Validate())
	cfg, err := a.GetDatabaseConfig()
	require.NoError(t, err)
	assert.Equal(t, asset_entity.DatabaseDriver("fakedb"), cfg.Driver)
	assert.Equal(t, "fake.internal", cfg.Host)
	assert.Equal(t, dbdrivertest.FakeDefaultPort, cfg.Port)
	assert.Equal(t, "main", cfg.Database)
	// 假驱动声明了口令字段，所以口令被接受并加密。
	decrypted, err := credential_svc.Default().Decrypt(cfg.Password)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", decrypted)

	// 必填规则也是假驱动自己的。
	_, err = PrepareCreate(asset_entity.AssetTypeDatabase, map[string]any{"driver": "fakedb"})
	require.ErrorIs(t, err, dbdrivertest.ErrFakeHostRequired)

	// 更新：只带改动的字段，其余保留。
	update, err := PrepareUpdate(asset_entity.AssetTypeDatabase, map[string]any{"host": "fake-2.internal", "read_only": true})
	require.NoError(t, err)
	require.NoError(t, h.ApplyUpdateArgs(t.Context(), a, update.Config))
	require.NoError(t, a.Validate())
	cfg, err = a.GetDatabaseConfig()
	require.NoError(t, err)
	assert.Equal(t, "fake-2.internal", cfg.Host)
	assert.Equal(t, dbdrivertest.FakeDefaultPort, cfg.Port)
	assert.True(t, cfg.ReadOnly)

	view := h.SafeView(a)
	assert.Equal(t, "fakedb", view["driver"])
	assert.Equal(t, "fake-2.internal", view["host"])
	assert.NotContains(t, view, "username", "视图里的连接字段由驱动决定")
	assert.NotContains(t, view, "password")
}

// 口令是否适用由驱动的描述决定，而不是由共享代码按驱动名判断。
func TestDatabaseHandlerRejectsPasswordForADriverWithoutPasswordField(t *testing.T) {
	fake := dbdrivertest.NewFake()
	fake.Desc.Form[0].Fields = []dbdriver.FormField{dbdriver.FieldHost, dbdriver.FieldPort}
	dbdrivertest.Register(t, fake)

	for _, field := range []string{"password", "credential_id"} {
		args := map[string]any{"driver": "fakedb", "host": "fake.internal", field: "1"}
		_, err := PrepareCreate(asset_entity.AssetTypeDatabase, args)
		require.Error(t, err, field)
		assert.Contains(t, err.Error(), field+" is not applicable")
	}

	// 事后绑定托管凭据同样被拒绝；内置的 SQLite 走的是同一条规则，报错文本不变。
	for _, tt := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"driver": "fakedb", "host": "fake.internal"}, "credential_id is not applicable to FakeDB"},
		{map[string]any{"driver": "sqlite", "path": "/tmp/x.db"}, "credential_id is not applicable to SQLite"},
	} {
		prepared, err := PrepareCreate(asset_entity.AssetTypeDatabase, tt.args)
		require.NoError(t, err)
		_, err = prepared.BindCredential(CredentialBinding{ID: 7, Type: credential_entity.TypePassword})
		require.EqualError(t, err, tt.want)
	}
}

// 未注册的驱动不可用，并且点名：绝不能被当成某个内置驱动继续处理。
func TestDatabaseHandlerReportsUnregisteredDriverAsUnavailable(t *testing.T) {
	h, ok := Get(asset_entity.AssetTypeDatabase)
	require.True(t, ok)
	args := map[string]any{"driver": "oracle", "host": "ora.internal", "port": float64(1521), "username": "system"}

	_, err := PrepareCreate(asset_entity.AssetTypeDatabase, args)
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")

	require.ErrorIs(t, h.ValidateCreateArgs(args), dbregistry.ErrDriverUnavailable)
	err = h.ApplyCreateArgs(t.Context(), &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}, args)
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)

	// 已经存着一个不可用驱动的资产（例如提供它的扩展没加载）：带连接配置的更新同样报错，而
	// 不是按网络驱动合并字段。
	stored := &asset_entity.Asset{Name: "ora", Type: asset_entity.AssetTypeDatabase}
	require.NoError(t, stored.SetDatabaseConfig(&asset_entity.DatabaseConfig{
		Driver: "oracle", Host: "ora.internal", Port: 1521, Username: "system",
	}))
	storedConfig := stored.Config
	err = h.ApplyUpdateArgs(t.Context(), stored, map[string]any{"host": "ora-2.internal"})
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
	assert.Equal(t, storedConfig, stored.Config)

	// 资产本身仍可编辑：不带连接配置的更新（改名、分组）不动配置，落库前的校验也放行。
	require.NoError(t, h.ApplyUpdateArgs(t.Context(), stored, map[string]any{}))
	assert.Equal(t, storedConfig, stored.Config)
	require.NoError(t, stored.Validate())
}

func TestMissingDriverParameterListsRegisteredDrivers(t *testing.T) {
	h, ok := Get(asset_entity.AssetTypeDatabase)
	require.True(t, ok)
	args := map[string]any{"host": "db.internal", "username": "app"}

	// 只有内置驱动时，文本与改动前逐字相同。
	require.EqualError(t, h.ValidateCreateArgs(args), "database type requires driver parameter (mysql, postgresql, mssql, sqlite)")

	// 后注册的驱动按它自己声明的顺序排进来，而不是按注册先后或名字。
	dbdrivertest.Register(t, dbdrivertest.NewFake())

	_, prepareErr := PrepareCreate(asset_entity.AssetTypeDatabase, args)
	for name, err := range map[string]error{
		"PrepareCreate":      prepareErr,
		"ValidateCreateArgs": h.ValidateCreateArgs(args),
		"ApplyCreateArgs":    h.ApplyCreateArgs(t.Context(), &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}, args),
	} {
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "database type requires driver parameter (mysql, postgresql, mssql, sqlite, fakedb)", name)
	}
}
