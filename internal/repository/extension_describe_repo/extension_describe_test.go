package extension_describe_repo

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/opskat/opskat/internal/model/entity/extension_describe_entity"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDescriptorCacheIsKeyedByExtensionAndRefreshedByHash(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&extension_describe_entity.ExtensionDescribe{}))
	db.SetDefault(gdb)

	ctx := context.Background()
	repo := NewExtensionDescribe()

	// Nothing cached yet is an ordinary answer, not an error: the extension has
	// simply never been loaded on this machine.
	missing, err := repo.Find(ctx, "oss")
	require.NoError(t, err)
	require.Nil(t, missing)

	require.NoError(t, repo.Save(ctx, &extension_describe_entity.ExtensionDescribe{
		Name: "oss", WasmHash: "hash-a", Descriptor: `{"tools":[]}`,
	}))
	stored, err := repo.Find(ctx, "oss")
	require.NoError(t, err)
	require.Equal(t, "hash-a", stored.WasmHash)

	// A rebuilt extension replaces the entry rather than adding a second row —
	// otherwise Find would keep answering with whichever one it hit first.
	require.NoError(t, repo.Save(ctx, &extension_describe_entity.ExtensionDescribe{
		Name: "oss", WasmHash: "hash-b", Descriptor: `{"tools":[{"name":"list_objects"}]}`,
	}))
	refreshed, err := repo.Find(ctx, "oss")
	require.NoError(t, err)
	require.Equal(t, "hash-b", refreshed.WasmHash)
	require.Equal(t, stored.ID, refreshed.ID)

	var count int64
	require.NoError(t, gdb.Model(&extension_describe_entity.ExtensionDescribe{}).Count(&count).Error)
	require.Equal(t, int64(1), count)

	require.NoError(t, repo.Delete(ctx, "oss"))
	gone, err := repo.Find(ctx, "oss")
	require.NoError(t, err)
	require.Nil(t, gone)
}
