package asset_repo

import (
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestAssetRepo_FindByIDs(t *testing.T) {
	ctx, r := setupAssetRepo(t)

	id1 := createAsset(t, ctx, r, "srv-1", `{"host":"10.0.0.1"}`)
	id2 := createAsset(t, ctx, r, "srv-2", `{"host":"10.0.0.2"}`)
	deletedID := createAsset(t, ctx, r, "srv-deleted", `{"host":"10.0.0.3"}`)
	require.NoError(t, r.Delete(ctx, deletedID))

	// Empty input is a safe no-op.
	empty, err := r.FindByIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// Returns only active rows; ignores soft-deleted and non-existent IDs.
	found, err := r.FindByIDs(ctx, []int64{id1, id2, deletedID, 99999})
	require.NoError(t, err)
	require.Len(t, found, 2)
	gotIDs := []int64{found[0].ID, found[1].ID}
	assert.ElementsMatch(t, []int64{id1, id2}, gotIDs)
}

func TestAssetRepo_UpdateGroupIDs(t *testing.T) {
	ctx, r := setupAssetRepo(t)

	id1 := createAsset(t, ctx, r, "srv-1", `{}`)
	id2 := createAsset(t, ctx, r, "srv-2", `{}`)
	untouchedID := createAsset(t, ctx, r, "srv-3", `{}`)
	deletedID := createAsset(t, ctx, r, "srv-deleted", `{}`)
	require.NoError(t, r.Delete(ctx, deletedID))

	// Empty input is a safe no-op.
	require.NoError(t, r.UpdateGroupIDs(ctx, nil, 10))

	require.NoError(t, r.UpdateGroupIDs(ctx, []int64{id1, id2, deletedID}, 42))

	a1, err := r.Find(ctx, id1)
	require.NoError(t, err)
	assert.Equal(t, int64(42), a1.GroupID)

	a2, err := r.Find(ctx, id2)
	require.NoError(t, err)
	assert.Equal(t, int64(42), a2.GroupID)

	a3, err := r.Find(ctx, untouchedID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), a3.GroupID)

	// Soft-deleted row must not be mutated.
	var rawDeleted asset_entity.Asset
	require.NoError(t, db.Ctx(ctx).Where("id = ?", deletedID).First(&rawDeleted).Error)
	assert.Equal(t, int64(0), rawDeleted.GroupID)
	assert.Equal(t, asset_entity.StatusDeleted, rawDeleted.Status)
}

func TestAssetRepo_DeleteByIDs(t *testing.T) {
	ctx, r := setupAssetRepo(t)

	createWithPolicy := func(name, config, policy string) int64 {
		t.Helper()
		a := &asset_entity.Asset{
			Name:       name,
			Type:       asset_entity.AssetTypeSSH,
			Config:     config,
			CmdPolicy:  policy,
			Status:     asset_entity.StatusActive,
			Createtime: 1,
		}
		require.NoError(t, r.Create(ctx, a))
		return a.ID
	}

	id1 := createWithPolicy("srv-1", `{"host":"h1","password":"secret"}`, `{"allow_list":["ls"]}`)
	id2 := createWithPolicy("srv-2", `{"host":"h2","password":"secret2"}`, `{"deny_list":["rm"]}`)
	untouchedID := createWithPolicy("srv-keep", `{"host":"h3"}`, `{"allow_list":["pwd"]}`)

	// Empty input is a safe no-op.
	require.NoError(t, r.DeleteByIDs(ctx, nil))

	require.NoError(t, r.DeleteByIDs(ctx, []int64{id1, id2, 99999}))

	// Active queries must no longer return deleted rows.
	active, err := r.FindByIDs(ctx, []int64{id1, id2, untouchedID})
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, untouchedID, active[0].ID)

	// Verify soft-delete semantics: status = StatusDeleted, config & command_policy cleared.
	for _, id := range []int64{id1, id2} {
		var raw asset_entity.Asset
		require.NoError(t, db.Ctx(ctx).Where("id = ?", id).First(&raw).Error)
		assert.Equal(t, asset_entity.StatusDeleted, raw.Status)
		assert.Empty(t, raw.Config)
		assert.Empty(t, raw.CmdPolicy)
	}

	// Untouched active row keeps its config and command_policy intact.
	kept, err := r.Find(ctx, untouchedID)
	require.NoError(t, err)
	assert.Equal(t, `{"host":"h3"}`, kept.Config)
	assert.Equal(t, `{"allow_list":["pwd"]}`, kept.CmdPolicy)
}
