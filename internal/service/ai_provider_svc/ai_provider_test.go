package ai_provider_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/repository/ai_provider_repo"
	"github.com/opskat/opskat/internal/repository/ai_provider_repo/mock_ai_provider_repo"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"
)

func TestGetActiveNormalizesOnlyNotFound(t *testing.T) {
	oldRepo := ai_provider_repo.AIProvider()
	t.Cleanup(func() { ai_provider_repo.RegisterAIProvider(oldRepo) })

	t.Run("no active provider", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
		ai_provider_repo.RegisterAIProvider(repo)
		repo.EXPECT().GetActive(gomock.Any()).Return(nil, gorm.ErrRecordNotFound)

		got, err := AIProvider().GetActive(context.Background())

		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("repository failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mock_ai_provider_repo.NewMockAIProviderRepo(ctrl)
		ai_provider_repo.RegisterAIProvider(repo)
		repo.EXPECT().GetActive(gomock.Any()).Return(nil, errors.New("database offline"))

		got, err := AIProvider().GetActive(context.Background())

		require.ErrorContains(t, err, "database offline")
		require.Nil(t, got)
	})
}
