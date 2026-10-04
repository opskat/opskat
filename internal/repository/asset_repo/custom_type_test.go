package asset_repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestAssetRepo_ListByCustomType(t *testing.T) {
	ctx, r := setupAssetRepo(t)
	create := func(name, assetType, config string) int64 {
		t.Helper()
		asset := &asset_entity.Asset{
			Name: name, Type: assetType, Config: config,
			Status: asset_entity.StatusActive, Createtime: 1,
		}
		require.NoError(t, r.Create(ctx, asset))
		return asset.ID
	}

	a := create("grafana-a", asset_entity.AssetTypeGeneric, `{"custom_type":"grafana","values":{"host":{"value":"a"}}}`)
	b := create("grafana-b", asset_entity.AssetTypeGeneric, `{"custom_type":"grafana"}`)
	create("aws", asset_entity.AssetTypeGeneric, `{"custom_type":"aws"}`)
	// 非通用资产的 config 恰好有同名键，不能算作引用。
	create("ssh", asset_entity.AssetTypeSSH, `{"custom_type":"grafana"}`)
	deleted := create("deleted", asset_entity.AssetTypeGeneric, `{"custom_type":"grafana"}`)
	require.NoError(t, r.Delete(ctx, deleted))

	assets, err := r.ListByCustomType(ctx, "grafana")
	require.NoError(t, err)
	got := make([]int64, 0, len(assets))
	for _, asset := range assets {
		got = append(got, asset.ID)
	}
	assert.Equal(t, []int64{a, b}, got)

	none, err := r.ListByCustomType(ctx, "missing")
	require.NoError(t, err)
	assert.Empty(t, none)
}
