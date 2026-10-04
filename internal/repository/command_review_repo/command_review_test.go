package command_review_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/command_review_entity"
)

func setupRepo(t *testing.T) (context.Context, CommandReviewRepo) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&command_review_entity.CommandReview{}))
	db.SetDefault(gdb)
	return context.Background(), NewCommandReview()
}

func TestGetReturnsNilWhenMissing(t *testing.T) {
	ctx, r := setupRepo(t)
	got, err := r.Get(ctx, "nope", 100)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestPutThenGetBeforeExpiry(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Put(ctx, &command_review_entity.CommandReview{CacheKey: "k", Result: `{"outcome":"pass"}`, Createtime: 100, Expiretime: 200}))

	got, err := r.Get(ctx, "k", 150)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, `{"outcome":"pass"}`, got.Result)
}

func TestGetIgnoresExpiredEntries(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Put(ctx, &command_review_entity.CommandReview{CacheKey: "k", Result: "{}", Createtime: 100, Expiretime: 200}))

	got, err := r.Get(ctx, "k", 200)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestPutReplacesExistingKey(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Put(ctx, &command_review_entity.CommandReview{CacheKey: "k", Result: `{"outcome":"pass"}`, Createtime: 100, Expiretime: 200}))
	require.NoError(t, r.Put(ctx, &command_review_entity.CommandReview{CacheKey: "k", Result: `{"outcome":"reject"}`, Createtime: 300, Expiretime: 400}))

	got, err := r.Get(ctx, "k", 350)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, `{"outcome":"reject"}`, got.Result)

	var count int64
	require.NoError(t, db.Ctx(ctx).Model(&command_review_entity.CommandReview{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
