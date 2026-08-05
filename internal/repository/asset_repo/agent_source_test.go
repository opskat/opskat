package asset_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func setupAssetRepo(t *testing.T) (context.Context, AssetRepo) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&asset_entity.Asset{}))
	db.SetDefault(gdb)
	return context.Background(), NewAsset()
}

// createAsset inserts an asset with the given raw config JSON.
func createAsset(t *testing.T, ctx context.Context, r AssetRepo, name, config string) int64 {
	t.Helper()
	asset := &asset_entity.Asset{
		Name:       name,
		Type:       asset_entity.AssetTypeSSH,
		Config:     config,
		Status:     asset_entity.StatusActive,
		Createtime: 1,
	}
	require.NoError(t, r.Create(ctx, asset))
	return asset.ID
}

func TestAssetRepo_AgentAuthSourceQueries(t *testing.T) {
	ctx, r := setupAssetRepo(t)

	// 引用来源 1 的 Agent 认证 SSH 资产（config 为 JSON，agent_source_id 在
	// json_extract 查询路径上）。
	id1 := createAsset(t, ctx, r, "agent-a", `{"host":"h1","port":22,"username":"u","auth_type":"agent","agent_source_id":1}`)
	id2 := createAsset(t, ctx, r, "agent-b", `{"host":"h2","port":22,"username":"u","auth_type":"agent","agent_source_id":1}`)
	// 引用来源 2，不应计入来源 1。
	createAsset(t, ctx, r, "agent-other", `{"host":"h3","port":22,"username":"u","auth_type":"agent","agent_source_id":2}`)
	// password 认证、非 agent，不应计入。
	createAsset(t, ctx, r, "password", `{"host":"h4","port":22,"username":"u","auth_type":"password"}`)

	count, err := r.CountAgentAuthBySourceID(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = r.CountAgentAuthBySourceID(ctx, 99)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	assets, err := r.ListAgentAuthBySourceID(ctx, 1)
	require.NoError(t, err)
	require.Len(t, assets, 2)
	got := make([]int64, 0, len(assets))
	for _, a := range assets {
		got = append(got, a.ID)
	}
	assert.ElementsMatch(t, []int64{id1, id2}, got)
}

func TestAssetRepo_AgentAuthSourceIgnoresDeleted(t *testing.T) {
	ctx, r := setupAssetRepo(t)
	id := createAsset(t, ctx, r, "agent-a", `{"host":"h1","port":22,"username":"u","auth_type":"agent","agent_source_id":7}`)

	// 软删除后不再计入引用。
	require.NoError(t, r.Delete(ctx, id))

	count, err := r.CountAgentAuthBySourceID(ctx, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}
