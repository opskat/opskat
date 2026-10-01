package command_review_svc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/command_review_entity"
	"github.com/opskat/opskat/internal/repository/command_review_repo/mock_command_review_repo"
)

func TestRepoCacheRoundTripsResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_command_review_repo.NewMockCommandReviewRepo(ctrl)
	now := time.Unix(1000, 0)
	c := &repoCache{repo: repo, now: func() time.Time { return now }}

	var stored *command_review_entity.CommandReview
	repo.EXPECT().Put(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, e *command_review_entity.CommandReview) error {
		stored = e
		return nil
	})
	in := Result{Outcome: OutcomeReject, Failed: []string{QuestionDisruptive}, Model: "jev-1.13.0", Scores: map[string]float64{QuestionDisruptive: 0.9}}
	require.NoError(t, c.Put(context.Background(), "key", in, time.Hour))
	assert.Equal(t, "key", stored.CacheKey)
	assert.Equal(t, int64(1000), stored.Createtime)
	assert.Equal(t, int64(1000+3600), stored.Expiretime)

	repo.EXPECT().Get(gomock.Any(), "key", int64(1000)).Return(stored, nil)
	got, err := c.Get(context.Background(), "key")
	require.NoError(t, err)
	assert.Equal(t, in, *got)
}

func TestRepoCacheGetMissReturnsNil(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_command_review_repo.NewMockCommandReviewRepo(ctrl)
	c := &repoCache{repo: repo, now: time.Now}

	repo.EXPECT().Get(gomock.Any(), "key", gomock.Any()).Return(nil, nil)
	got, err := c.Get(context.Background(), "key")
	require.NoError(t, err)
	assert.Nil(t, got)
}
