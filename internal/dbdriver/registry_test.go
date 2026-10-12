package dbdriver_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestLookupUnregisteredDriverNamesIt(t *testing.T) {
	_, err := dbdriver.Lookup("oracle")
	require.ErrorIs(t, err, dbdriver.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
}

func TestRegisteredDriverIsFoundByIDAndByAlias(t *testing.T) {
	fake := dbdrivertest.NewFake()
	dbdrivertest.Register(t, fake)

	got, err := dbdriver.Lookup("fakedb")
	require.NoError(t, err)
	assert.Same(t, fake, got)

	// 存进配置的只有 id：别名不是合法的 driver 取值。
	_, err = dbdriver.Lookup("fake")
	require.ErrorIs(t, err, dbdriver.ErrDriverUnavailable)

	for _, name := range []string{"fakedb", "fake"} {
		byName, ok := dbdriver.ByName(name)
		require.True(t, ok, name)
		assert.Same(t, fake, byName)
	}
	_, ok := dbdriver.ByName("oracle")
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
			assert.Panics(t, func() { dbdriver.Register(&dbdrivertest.Fake{Desc: desc}) })
			_, err := dbdriver.Lookup("other")
			assert.ErrorIs(t, err, dbdriver.ErrDriverUnavailable, "a rejected driver must not stay half-registered")
		})
	}
}

func TestUnregisterFreesIDAndAliases(t *testing.T) {
	dbdriver.Register(dbdrivertest.NewFake())
	dbdriver.Unregister("fakedb")

	_, err := dbdriver.Lookup("fakedb")
	require.ErrorIs(t, err, dbdriver.ErrDriverUnavailable)
	_, ok := dbdriver.ByName("fake")
	assert.False(t, ok)
	// 名字确实腾出来了：同一个驱动可以再注册一次。
	dbdrivertest.Register(t, dbdrivertest.NewFake())
}

func TestListIsOrderedByID(t *testing.T) {
	for _, id := range []asset_entity.DatabaseDriver{"zz", "aa", "mm"} {
		fake := dbdrivertest.NewFake()
		fake.Desc.ID = id
		fake.Desc.Aliases = nil
		dbdrivertest.Register(t, fake)
	}
	ids := make([]asset_entity.DatabaseDriver, 0, 3)
	for _, d := range dbdriver.List() {
		ids = append(ids, d.Descriptor().ID)
	}
	assert.Equal(t, []asset_entity.DatabaseDriver{"aa", "mm", "zz"}, ids)
	assert.Equal(t, []string{"aa", "mm", "zz"}, dbdriver.IDs())
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

	err = asset(&asset_entity.DatabaseConfig{Driver: "oracle", Host: "h", Port: 1521, Username: "u"}).Validate()
	require.ErrorIs(t, err, dbdriver.ErrDriverUnavailable)
	assert.Contains(t, err.Error(), "oracle")
}
