package dbdriver_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestLookupUnregisteredDriverNamesIt(t *testing.T) {
	_, err := dbregistry.Lookup("oracle")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
}

func TestRegisteredDriverIsFoundByIDAndByAlias(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)

	got, err := dbregistry.Lookup("fakedb")
	require.NoError(t, err)
	assert.Same(t, fake, got)

	// 存进配置的只有 id：别名不是合法的 driver 取值。
	_, err = dbregistry.Lookup("fake")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)

	for _, name := range []string{"fakedb", "fake"} {
		byName, ok := dbregistry.ByName(name)
		require.True(t, ok, name)
		assert.Same(t, fake, byName)
	}
	_, ok := dbregistry.ByName("oracle")
	assert.False(t, ok)
}

func TestRegisterRejectsNamesAnotherDriverOwns(t *testing.T) {
	dbdrivertest.Register(t, dbdrivertest.NewFake())

	for name, desc := range map[string]dbdriver.Descriptor{
		"same id":              {ID: "fakedb"},
		"id taken as alias":    {ID: "fake"},
		"alias taken as id":    {ID: "other", Aliases: []string{"fakedb"}},
		"alias taken as alias": {ID: "other", Aliases: []string{"fake"}},
		"empty id":             {},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Panics(t, func() { dbregistry.Register(&dbdrivertest.Fake{Desc: desc}) })
			_, err := dbregistry.Lookup("other")
			assert.ErrorIs(t, err, dbregistry.ErrDriverUnavailable, "a rejected driver must not stay half-registered")
		})
	}
}

// 扩展在运行期注册驱动：撞名是加载失败而不是编程错误，TryRegister 不 panic，返回点名冲突的
// 名字与占用它的驱动的错误，整个驱动都不注册。
func TestTryRegisterReportsTheNameAndItsOwnerWithoutPanicking(t *testing.T) {
	dbdrivertest.Register(t, dbdrivertest.NewFake())

	for name, tc := range map[string]struct {
		desc  dbdriver.Descriptor
		taken string
	}{
		"same id":              {dbdriver.Descriptor{ID: "fakedb"}, "fakedb"},
		"id taken as alias":    {dbdriver.Descriptor{ID: "fake"}, "fake"},
		"alias taken as id":    {dbdriver.Descriptor{ID: "other", Aliases: []string{"free", "fakedb"}}, "fakedb"},
		"alias taken as alias": {dbdriver.Descriptor{ID: "other", Aliases: []string{"fake"}}, "fake"},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() { err = dbregistry.TryRegister(&dbdrivertest.Fake{Desc: tc.desc}) })
			var taken *dbregistry.NameTakenError
			require.ErrorAs(t, err, &taken)
			assert.Equal(t, tc.taken, taken.Name)
			assert.Equal(t, dbdriver.DriverID("fakedb"), taken.Owner)
			assert.Contains(t, err.Error(), `"`+tc.taken+`"`)
			assert.Contains(t, err.Error(), `"fakedb"`)

			for _, n := range []string{"other", "free"} {
				_, ok := dbregistry.ByName(n)
				assert.False(t, ok, "a refused driver must not stay half-registered (%s)", n)
			}
		})
	}

	require.Error(t, dbregistry.TryRegister(&dbdrivertest.Fake{}), "a driver without an id is refused")

	other := dbdrivertest.NewFake()
	other.Desc.ID, other.Desc.Aliases = "other", []string{"free"}
	require.NoError(t, dbregistry.TryRegister(other))
	t.Cleanup(func() { dbregistry.Unregister("other") })
	got, err := dbregistry.Lookup("other")
	require.NoError(t, err)
	assert.Same(t, other, got)
}

func TestUnregisterFreesIDAndAliases(t *testing.T) {
	dbregistry.Register(dbdrivertest.NewFake())
	dbregistry.Unregister("fakedb")

	_, err := dbregistry.Lookup("fakedb")
	require.ErrorIs(t, err, dbregistry.ErrDriverUnavailable)
	_, ok := dbregistry.ByName("fake")
	assert.False(t, ok)
	// 名字确实腾出来了：同一个驱动可以再注册一次。
	dbdrivertest.Register(t, dbdrivertest.NewFake())
}

// 顺序由各驱动自己声明的 Order 决定，与注册先后无关；Order 相同的按 id 排。
func TestListFollowsEachDriversDeclaredOrder(t *testing.T) {
	for _, d := range []struct {
		id    asset_entity.DatabaseDriver
		order int
	}{
		{"zz", 10}, {"mm", 30}, {"bb", 20}, {"aa", 20},
	} {
		fake := dbdrivertest.NewFake()
		fake.Desc.ID = d.id
		fake.Desc.Order = d.order
		fake.Desc.Aliases = nil
		dbdrivertest.Register(t, fake)
	}
	ids := make([]asset_entity.DatabaseDriver, 0, 4)
	for _, d := range dbregistry.List() {
		ids = append(ids, d.Descriptor().ID)
	}
	assert.Equal(t, []asset_entity.DatabaseDriver{"zz", "aa", "bb", "mm"}, ids)
	assert.Equal(t, []string{"zz", "aa", "bb", "mm"}, dbregistry.IDs())
}

// 实体层的 Validate 经注册表找到驱动：驱动自己的规则生效，未注册的驱动报「不可用」。
func TestAssetValidationUsesTheRegisteredDriver(t *testing.T) {
	dbdrivertest.Register(t, dbdrivertest.NewFake())

	asset := func(cfg *asset_entity.DatabaseConfig) *asset_entity.Asset {
		a := &asset_entity.Asset{Name: "x", Type: asset_entity.AssetTypeDatabase}
		require.NoError(t, a.SetDatabaseConfig(cfg))
		return a
	}

	// 假驱动只要求 host —— 没有 username 也通过，说明走的不是别的驱动的规则。
	require.NoError(t, asset(&asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h", Port: 7777}).Validate())

	err := asset(&asset_entity.DatabaseConfig{Driver: "fakedb"}).Validate()
	require.ErrorIs(t, err, dbdrivertest.ErrFakeHostRequired)

	// 没注册的驱动没有规则可跑：资产仍然通过校验（它连不上，但要能改名、保存），驱动无关的
	// 规则照常生效。
	require.NoError(t, asset(&asset_entity.DatabaseConfig{Driver: "oracle"}).Validate())
	require.Error(t, asset(&asset_entity.DatabaseConfig{Driver: "oracle", QueryTimeoutSeconds: 3}).Validate())
}
