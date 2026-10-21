package assettype

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// registerOptionsDriver 注册一个声明了专属字段的测试驱动。
func registerOptionsDriver(t *testing.T) {
	t.Helper()
	fake := dbdrivertest.NewFake()
	fake.Desc.DriverFields = []dbdriver.DriverField{
		{Key: "region", Type: dbdriver.FieldTypeText, Tab: dbdriver.TabConnection, Required: true},
		{Key: "retries", Type: dbdriver.FieldTypeNumber, Tab: dbdriver.TabAdvanced, Default: 3},
		{Key: "mode", Type: dbdriver.FieldTypeEnum, Tab: dbdriver.TabAdvanced, Enum: []string{"a", "b"}, Default: "a"},
		{Key: "token", Type: dbdriver.FieldTypePassword, Tab: dbdriver.TabConnection},
	}
	dbdrivertest.Register(t, fake)
}

func createWithOptions(t *testing.T, opts map[string]any) (*asset_entity.Asset, error) {
	t.Helper()
	prepared, err := PrepareCreate(asset_entity.AssetTypeDatabase, map[string]any{
		"driver": "fakedb", "host": "fake.internal", "driver_options": opts,
	})
	if err != nil {
		return nil, err
	}
	a := &asset_entity.Asset{Name: "x", Type: asset_entity.AssetTypeDatabase}
	return a, prepared.Handler.ApplyCreateArgs(t.Context(), a, prepared.Config)
}

func TestDriverOptionsCreate(t *testing.T) {
	registerOptionsDriver(t)

	t.Run("defaults are applied and the password is stored encrypted", func(t *testing.T) {
		a, err := createWithOptions(t, map[string]any{"region": "eu", "token": "s3cret"})
		require.NoError(t, err)
		cfg, err := a.GetDatabaseConfig()
		require.NoError(t, err)
		assert.Equal(t, "eu", cfg.DriverOptions["region"])
		assert.EqualValues(t, 3, cfg.DriverOptions["retries"])
		assert.Equal(t, "a", cfg.DriverOptions["mode"])
		stored, _ := cfg.DriverOptions["token"].(string)
		assert.NotEqual(t, "s3cret", stored)
		assert.NotContains(t, a.Config, "s3cret")
		plain, err := credential_svc.Default().Decrypt(stored)
		require.NoError(t, err)
		assert.Equal(t, "s3cret", plain)
	})

	t.Run("invalid input is rejected before anything is stored", func(t *testing.T) {
		for name, tc := range map[string]struct {
			opts map[string]any
			want string
		}{
			"unknown key":   {map[string]any{"region": "eu", "regoin": "x"}, `"regoin"`},
			"wrong type":    {map[string]any{"region": "eu", "retries": "many"}, `"retries"`},
			"bad enum":      {map[string]any{"region": "eu", "mode": "z"}, `"mode"`},
			"missing field": {map[string]any{"retries": 1}, `"region"`},
		} {
			_, err := createWithOptions(t, tc.opts)
			require.Error(t, err, name)
			assert.Contains(t, err.Error(), tc.want, name)
		}
		_, err := PrepareCreate(asset_entity.AssetTypeDatabase, map[string]any{
			"driver": "fakedb", "host": "h", "driver_options": "not-an-object",
		})
		require.Error(t, err)
	})

	t.Run("approval view carries no driver option values", func(t *testing.T) {
		prepared, err := PrepareCreate(asset_entity.AssetTypeDatabase, map[string]any{
			"driver": "fakedb", "host": "fake.internal", "driver_options": map[string]any{"region": "eu", "token": "s3cret"},
		})
		require.NoError(t, err)
		data, err := json.Marshal(prepared.Approval)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "s3cret")
	})
}

func TestDriverOptionsRejectedForDriversWithoutFields(t *testing.T) {
	_, err := PrepareCreate(asset_entity.AssetTypeDatabase, map[string]any{
		"driver": "mysql", "host": "h", "port": 3306, "username": "u", "driver_options": map[string]any{"x": 1},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"x"`)

	// 内置驱动照旧：配置里没有 driver_options 键。
	prepared, err := PrepareCreate(asset_entity.AssetTypeDatabase, map[string]any{
		"driver": "mysql", "host": "h", "port": 3306, "username": "u",
	})
	require.NoError(t, err)
	a := &asset_entity.Asset{Name: "m", Type: asset_entity.AssetTypeDatabase}
	require.NoError(t, prepared.Handler.ApplyCreateArgs(t.Context(), a, prepared.Config))
	assert.NotContains(t, a.Config, "driver_options")
}

func TestDriverOptionsUpdate(t *testing.T) {
	registerOptionsDriver(t)
	h := &databaseHandler{}
	a, err := createWithOptions(t, map[string]any{"region": "eu", "token": "old"})
	require.NoError(t, err)

	t.Run("partial update merges and keeps untouched values", func(t *testing.T) {
		require.NoError(t, h.ApplyUpdateArgs(t.Context(), a, map[string]any{
			"driver_options": map[string]any{"retries": 5, "token": "new"},
		}))
		cfg, err := a.GetDatabaseConfig()
		require.NoError(t, err)
		assert.Equal(t, "eu", cfg.DriverOptions["region"])
		assert.EqualValues(t, 5, cfg.DriverOptions["retries"])
		plain, err := credential_svc.Default().Decrypt(cfg.DriverOptions["token"].(string))
		require.NoError(t, err)
		assert.Equal(t, "new", plain)
	})

	t.Run("an empty password leaves the stored one", func(t *testing.T) {
		before, _ := a.GetDatabaseConfig()
		require.NoError(t, h.ApplyUpdateArgs(t.Context(), a, map[string]any{
			"driver_options": map[string]any{"token": ""},
		}))
		after, _ := a.GetDatabaseConfig()
		assert.Equal(t, before.DriverOptions["token"], after.DriverOptions["token"])
	})

	t.Run("unknown key is rejected, named, and nothing changes", func(t *testing.T) {
		before := a.Config
		err := h.ApplyUpdateArgs(t.Context(), a, map[string]any{"driver_options": map[string]any{"nope": 1}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"nope"`)
		assert.Equal(t, before, a.Config)
		// 审批前的校验同样挡住它。
		_, err = h.AutomationUpdateContext(a, map[string]any{"driver_options": map[string]any{"nope": 1}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"nope"`)
	})

	t.Run("approval checks the merged options the commit will store", func(t *testing.T) {
		// 清空必填项：审批前就拒绝，而不是用户点了同意之后落库时才失败。
		args := map[string]any{"driver_options": map[string]any{"region": ""}}
		_, err := h.AutomationUpdateContext(a, args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"region"`)
		require.Error(t, h.ApplyUpdateArgs(t.Context(), a, args))
		// 口令类留空表示沿用：审批前同样不把它当成清空。
		_, err = h.AutomationUpdateContext(a, map[string]any{"driver_options": map[string]any{"token": ""}})
		require.NoError(t, err)
	})

	t.Run("an update without driver_options keeps them", func(t *testing.T) {
		before, _ := a.GetDatabaseConfig()
		require.NoError(t, h.ApplyUpdateArgs(t.Context(), a, map[string]any{"host": "fake-2.internal"}))
		after, _ := a.GetDatabaseConfig()
		assert.Equal(t, before.DriverOptions, after.DriverOptions)
	})
}

// 切换驱动时专属字段跟着驱动走：旧驱动的取值不写进新驱动的配置，新驱动的必填与默认值照创建时校验。
func TestDriverOptionsDriverSwitch(t *testing.T) {
	registerOptionsDriver(t)
	other := dbdrivertest.NewFake()
	other.Desc.ID, other.Desc.Name, other.Desc.Aliases = "otherdb", "OtherDB", nil
	other.Desc.DriverFields = []dbdriver.DriverField{
		{Key: "zone", Type: dbdriver.FieldTypeText, Tab: dbdriver.TabConnection, Required: true},
		{Key: "level", Type: dbdriver.FieldTypeNumber, Tab: dbdriver.TabAdvanced, Default: 2},
	}
	dbdrivertest.Register(t, other)
	h := &databaseHandler{}

	t.Run("the new driver's required options are checked", func(t *testing.T) {
		a, err := createWithOptions(t, map[string]any{"region": "eu", "token": "s3cret"})
		require.NoError(t, err)
		before := a.Config
		_, err = h.AutomationUpdateContext(a, map[string]any{"driver": "otherdb"})
		require.Error(t, err, "审批前就挡住")
		assert.Contains(t, err.Error(), `"zone"`)
		err = h.ApplyUpdateArgs(t.Context(), a, map[string]any{"driver": "otherdb"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"zone"`)
		assert.Equal(t, before, a.Config)
	})

	t.Run("the old driver's options are dropped and the new defaults applied", func(t *testing.T) {
		a, err := createWithOptions(t, map[string]any{"region": "eu", "token": "s3cret"})
		require.NoError(t, err)
		args := map[string]any{"driver": "otherdb", "driver_options": map[string]any{"zone": "z1"}}
		_, err = h.AutomationUpdateContext(a, args)
		require.NoError(t, err)
		require.NoError(t, h.ApplyUpdateArgs(t.Context(), a, args))
		cfg, err := a.GetDatabaseConfig()
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"zone": "z1", "level": float64(2)}, cfg.DriverOptions)
	})

	t.Run("naming the same driver is not a switch", func(t *testing.T) {
		a, err := createWithOptions(t, map[string]any{"region": "eu"})
		require.NoError(t, err)
		before, _ := a.GetDatabaseConfig()
		require.NoError(t, h.ApplyUpdateArgs(t.Context(), a, map[string]any{"driver": "fakedb"}))
		after, _ := a.GetDatabaseConfig()
		assert.Equal(t, before.DriverOptions, after.DriverOptions)
	})
}

func TestDriverOptionsKeptWhenDriverUnavailable(t *testing.T) {
	registerOptionsDriver(t)
	h := &databaseHandler{}
	a, err := createWithOptions(t, map[string]any{"region": "eu", "token": "s3cret"})
	require.NoError(t, err)
	stored := a.Config

	dbregistry.Unregister("fakedb") // 提供驱动的扩展停用
	// 改名、移动分组：不带配置的更新，配置原样保留。
	require.NoError(t, h.ApplyUpdateArgs(t.Context(), a, map[string]any{}))
	assert.Equal(t, stored, a.Config)
	cfg, err := a.GetDatabaseConfig()
	require.NoError(t, err)
	assert.Equal(t, "eu", cfg.DriverOptions["region"])

	// 驱动不可用时视图里没有 driver_options：没有谁能说出哪些是口令。
	view := h.SafeView(a)
	assert.NotContains(t, view, "driver_options")
	data, _ := json.Marshal(view)
	assert.NotContains(t, string(data), "s3cret")
}

func TestDriverOptionsSafeView(t *testing.T) {
	registerOptionsDriver(t)
	h := &databaseHandler{}
	a, err := createWithOptions(t, map[string]any{"region": "eu", "token": "s3cret"})
	require.NoError(t, err)

	view := h.SafeView(a)
	values, ok := view["driver_options"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "eu", values["region"])
	assert.NotContains(t, values, "token")
	assert.Equal(t, []string{"token"}, view["driver_options_secrets_set"])
	data, _ := json.Marshal(view)
	assert.NotContains(t, string(data), "s3cret")
	cfg, _ := a.GetDatabaseConfig()
	assert.NotContains(t, string(data), cfg.DriverOptions["token"].(string), "密文也不展示")
}

// 没有口令类取值的资产，视图里不出现「已设置的口令」这一项（而不是 null）。
func TestDriverOptionsSafeViewWithoutSecrets(t *testing.T) {
	registerOptionsDriver(t)
	a, err := createWithOptions(t, map[string]any{"region": "eu"})
	require.NoError(t, err)

	view := (&databaseHandler{}).SafeView(a)
	assert.Contains(t, view, "driver_options")
	assert.NotContains(t, view, "driver_options_secrets_set")
}

func TestDriverOptionsHaveSecret(t *testing.T) {
	registerOptionsDriver(t)
	create := func(opts map[string]any) map[string]any {
		return map[string]any{"driver": "fakedb", "driver_options": opts}
	}
	assert.True(t, DriverOptionsHaveSecret(create(map[string]any{"token": "x"}), nil))
	assert.False(t, DriverOptionsHaveSecret(create(map[string]any{"region": "eu"}), nil), "非口令类不算明文")
	assert.False(t, DriverOptionsHaveSecret(create(map[string]any{"token": ""}), nil))
	assert.False(t, DriverOptionsHaveSecret(map[string]any{"host": "h"}, nil))

	// 局部更新不带 driver：取已存资产的驱动。
	a, err := createWithOptions(t, map[string]any{"region": "eu"})
	require.NoError(t, err)
	assert.True(t, DriverOptionsHaveSecret(map[string]any{"driver_options": map[string]any{"token": "x"}}, a))
	assert.False(t, DriverOptionsHaveSecret(map[string]any{"driver_options": map[string]any{"token": "x"}}, nil))
}
