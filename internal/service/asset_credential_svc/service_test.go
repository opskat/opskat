package asset_credential_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func setupAssetRepo(t *testing.T) *mock_asset_repo.MockAssetRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	asset_repo.RegisterAsset(repo)
	return repo
}

func TestResolvePassword(t *testing.T) {
	t.Run("preserves asset lookup error contract", func(t *testing.T) {
		repo := setupAssetRepo(t)
		repoErr := errors.New("database unavailable")
		repo.EXPECT().Find(gomock.Any(), int64(7)).Return(nil, repoErr)

		password, err := Default().ResolvePassword(context.Background(), 7)

		assert.Empty(t, password)
		assert.ErrorIs(t, err, repoErr)
		assert.EqualError(t, err, "asset not found: database unavailable")
	})

	t.Run("preserves unsupported asset type error", func(t *testing.T) {
		repo := setupAssetRepo(t)
		repo.EXPECT().Find(gomock.Any(), int64(8)).Return(&asset_entity.Asset{
			ID:   8,
			Type: "unknown",
		}, nil)

		password, err := Default().ResolvePassword(context.Background(), 8)

		assert.Empty(t, password)
		assert.EqualError(t, err, "unsupported asset type: unknown")
	})
}

func TestUsageAssetsReturnsOnlySafeSummaries(t *testing.T) {
	repo := setupAssetRepo(t)
	repo.EXPECT().FindByCredentialID(gomock.Any(), int64(3)).Return([]*asset_entity.Asset{
		{ID: 2, Name: "database", Type: "database", Config: `{"password":"secret"}`},
		{ID: 1, Name: "server", Type: "ssh", Description: "private notes"},
	}, nil)

	assets, err := Default().UsageAssets(context.Background(), 3)

	assert.NoError(t, err)
	assert.Equal(t, []AssetUsage{
		{ID: 2, Name: "database", Type: "database"},
		{ID: 1, Name: "server", Type: "ssh"},
	}, assets)
}

func TestUsageAssetNames(t *testing.T) {
	t.Run("projects names in repository order", func(t *testing.T) {
		repo := setupAssetRepo(t)
		repo.EXPECT().FindByCredentialID(gomock.Any(), int64(3)).Return([]*asset_entity.Asset{
			{ID: 2, Name: "database"},
			{ID: 1, Name: "server"},
		}, nil)

		names, err := Default().UsageAssetNames(context.Background(), 3)

		assert.NoError(t, err)
		assert.Equal(t, []string{"database", "server"}, names)
	})

	t.Run("returns repository error unchanged", func(t *testing.T) {
		repo := setupAssetRepo(t)
		repoErr := errors.New("database unavailable")
		repo.EXPECT().FindByCredentialID(gomock.Any(), int64(3)).Return(nil, repoErr)

		names, err := Default().UsageAssetNames(context.Background(), 3)

		assert.Nil(t, names)
		assert.ErrorIs(t, err, repoErr)
	})
}
