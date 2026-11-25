package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/group_repo/mock_group_repo"
)

func setupGroupRepoMap(t *testing.T, groups map[int64]*group_entity.Group) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRepo := mock_group_repo.NewMockGroupRepo(ctrl)
	orig := group_repo.Group()
	group_repo.RegisterGroup(mockRepo)
	t.Cleanup(func() {
		if orig != nil {
			group_repo.RegisterGroup(orig)
		}
	})

	mockRepo.EXPECT().Find(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*group_entity.Group, error) {
			g, ok := groups[id]
			if !ok {
				return nil, errors.New("not found")
			}
			return g, nil
		},
	).AnyTimes()
}

func TestResolveGroupChain_DeepNestingBeyondFiveLevels(t *testing.T) {
	// 7 层嵌套（1 <- 2 <- 3 <- 4 <- 5 <- 6 <- 7）：移除了 5 层硬上限后必须完整返回。
	groups := map[int64]*group_entity.Group{
		1: {ID: 1, ParentID: 0, Name: "L1", PermissionMode: "autopilot"},
		2: {ID: 2, ParentID: 1, Name: "L2"},
		3: {ID: 3, ParentID: 2, Name: "L3"},
		4: {ID: 4, ParentID: 3, Name: "L4"},
		5: {ID: 5, ParentID: 4, Name: "L5"},
		6: {ID: 6, ParentID: 5, Name: "L6"},
		7: {ID: 7, ParentID: 6, Name: "L7"},
	}
	setupGroupRepoMap(t, groups)

	chain := ResolveGroupChain(context.Background(), 7)
	require.Len(t, chain, 7)
	ids := make([]int64, len(chain))
	for i, g := range chain {
		ids[i] = g.ID
	}
	assert.Equal(t, []int64{7, 6, 5, 4, 3, 2, 1}, ids)
}

func TestResolveGroupChain_BreaksOnCycles(t *testing.T) {
	t.Run("multi-node cycle", func(t *testing.T) {
		groups := map[int64]*group_entity.Group{
			1: {ID: 1, ParentID: 3, Name: "A"},
			2: {ID: 2, ParentID: 1, Name: "B"},
			3: {ID: 3, ParentID: 2, Name: "C"},
		}
		setupGroupRepoMap(t, groups)

		chain := ResolveGroupChain(context.Background(), 2)
		require.Len(t, chain, 3)
		assert.Equal(t, int64(2), chain[0].ID)
		assert.Equal(t, int64(1), chain[1].ID)
		assert.Equal(t, int64(3), chain[2].ID)
	})

	t.Run("self cycle", func(t *testing.T) {
		groups := map[int64]*group_entity.Group{
			5: {ID: 5, ParentID: 5, Name: "Self"},
		}
		setupGroupRepoMap(t, groups)

		chain := ResolveGroupChain(context.Background(), 5)
		require.Len(t, chain, 1)
		assert.Equal(t, int64(5), chain[0].ID)
	})
}

func setupTestScopeServices(t *testing.T, groups map[int64]*group_entity.Group, assets map[int64]*asset_entity.Asset) {
	t.Helper()
	setupGroupRepoMap(t, groups)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAssetRepo := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAssetRepo := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAssetRepo)
	t.Cleanup(func() {
		if origAssetRepo != nil {
			asset_repo.RegisterAsset(origAssetRepo)
		}
	})
	mockAssetRepo.EXPECT().Find(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id int64) (*asset_entity.Asset, error) {
			a, ok := assets[id]
			if !ok {
				return nil, errors.New("not found")
			}
			return a, nil
		},
	).AnyTimes()
}

func TestResolveTestScope_DeepNestingBeyondFiveLevels(t *testing.T) {
	groups := map[int64]*group_entity.Group{
		1: {ID: 1, ParentID: 0, Name: "L1"},
		2: {ID: 2, ParentID: 1, Name: "L2"},
		3: {ID: 3, ParentID: 2, Name: "L3"},
		4: {ID: 4, ParentID: 3, Name: "L4"},
		5: {ID: 5, ParentID: 4, Name: "L5"},
		6: {ID: 6, ParentID: 5, Name: "L6"},
		7: {ID: 7, ParentID: 6, Name: "L7"},
	}
	assets := map[int64]*asset_entity.Asset{
		100: {ID: 100, Name: "deep-host", GroupID: 7},
	}
	setupTestScopeServices(t, groups, assets)

	t.Run("by assetID resolves full 7-level chain", func(t *testing.T) {
		asset, chain := resolveTestScope(context.Background(), 100, 0)
		require.NotNil(t, asset)
		assert.Equal(t, int64(100), asset.ID)
		require.Len(t, chain, 7)
		ids := make([]int64, len(chain))
		for i, g := range chain {
			ids[i] = g.ID
		}
		assert.Equal(t, []int64{7, 6, 5, 4, 3, 2, 1}, ids)
	})

	t.Run("by groupID resolves all 6 ancestors above target group", func(t *testing.T) {
		asset, chain := resolveTestScope(context.Background(), 0, 7)
		assert.Nil(t, asset)
		require.Len(t, chain, 6)
		ids := make([]int64, len(chain))
		for i, g := range chain {
			ids[i] = g.ID
		}
		assert.Equal(t, []int64{6, 5, 4, 3, 2, 1}, ids)
	})
}

func TestResolveTestScope_BreaksOnCycles(t *testing.T) {
	t.Run("multi-node cycle via assetID", func(t *testing.T) {
		groups := map[int64]*group_entity.Group{
			1: {ID: 1, ParentID: 3, Name: "A"},
			2: {ID: 2, ParentID: 1, Name: "B"},
			3: {ID: 3, ParentID: 2, Name: "C"},
		}
		assets := map[int64]*asset_entity.Asset{
			10: {ID: 10, Name: "cyclic-host", GroupID: 2},
		}
		setupTestScopeServices(t, groups, assets)

		_, chain := resolveTestScope(context.Background(), 10, 0)
		require.Len(t, chain, 3)
		assert.Equal(t, []int64{2, 1, 3}, []int64{chain[0].ID, chain[1].ID, chain[2].ID})
	})

	t.Run("self cycle via groupID terminates with empty ancestors", func(t *testing.T) {
		groups := map[int64]*group_entity.Group{
			5: {ID: 5, ParentID: 5, Name: "Self"},
		}
		setupTestScopeServices(t, groups, nil)

		asset, chain := resolveTestScope(context.Background(), 0, 5)
		assert.Nil(t, asset)
		assert.Empty(t, chain)
	})
}
