package extension_settings_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/extension_settings_entity"
)

func setupRepo(t *testing.T) (context.Context, ExtensionSettingsRepo) {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&extension_settings_entity.ExtensionSettings{}))
	return db.WithContextDB(context.Background(), gdb), NewExtensionSettings()
}

func TestFindReturnsNilWhenNothingIsStored(t *testing.T) {
	ctx, r := setupRepo(t)
	got, err := r.Find(ctx, "jev")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSaveReplacesTheExtensionsOneRow(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Save(ctx, "jev", `{"a":1}`))
	require.NoError(t, r.Save(ctx, "jev", `{"a":2}`))
	require.NoError(t, r.Save(ctx, "other", `{"b":1}`))

	got, err := r.Find(ctx, "jev")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, `{"a":2}`, got.Value)
	assert.NotZero(t, got.Updatetime)
}

func TestDeleteRemovesOnlyThatExtension(t *testing.T) {
	ctx, r := setupRepo(t)
	require.NoError(t, r.Save(ctx, "jev", `{"a":1}`))
	require.NoError(t, r.Save(ctx, "other", `{"b":1}`))
	require.NoError(t, r.Delete(ctx, "jev"))

	got, err := r.Find(ctx, "jev")
	require.NoError(t, err)
	assert.Nil(t, got)
	kept, err := r.Find(ctx, "other")
	require.NoError(t, err)
	require.NotNil(t, kept)
}
