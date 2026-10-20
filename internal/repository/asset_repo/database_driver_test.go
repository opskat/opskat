package asset_repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestAssetRepo_ListByDatabaseDriver(t *testing.T) {
	ctx, r := setupAssetRepo(t)
	create := func(name, assetType, config string, status int) int64 {
		t.Helper()
		a := &asset_entity.Asset{Name: name, Type: assetType, Config: config, Status: status, Createtime: 1}
		require.NoError(t, r.Create(ctx, a))
		return a.ID
	}
	a := create("a", asset_entity.AssetTypeDatabase, `{"driver":"fixturedb","host":"h"}`, asset_entity.StatusActive)
	b := create("b", asset_entity.AssetTypeDatabase, `{"driver":"fixturedb","host":"h2"}`, asset_entity.StatusActive)
	create("mysql", asset_entity.AssetTypeDatabase, `{"driver":"mysql","host":"h"}`, asset_entity.StatusActive)
	create("deleted", asset_entity.AssetTypeDatabase, `{"driver":"fixturedb"}`, asset_entity.StatusDeleted)
	create("not a database", "fixture-store", `{"driver":"fixturedb"}`, asset_entity.StatusActive)

	assets, err := r.List(ctx, ListOptions{DatabaseDriver: "fixturedb"})
	require.NoError(t, err)
	ids := make([]int64, 0, len(assets))
	for _, asset := range assets {
		ids = append(ids, asset.ID)
	}
	assert.ElementsMatch(t, []int64{a, b}, ids)

	assets, err = r.List(ctx, ListOptions{DatabaseDriver: "oracle"})
	require.NoError(t, err)
	assert.Empty(t, assets)
}
