package extension_state_repo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/migrations"
)

// A disabled extension must stay disabled for every process: the first row ever
// written for it has to carry enabled=0 rather than the column's default.
func TestCreatePersistsDisabledState(t *testing.T) {
	dataDir := t.TempDir()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opskat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.RunMigrations(gdb, dataDir))
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	db.SetDefault(gdb)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	repo := NewExtensionState()
	ctx := context.Background()
	for name, enabled := range map[string]bool{"off": false, "on": true} {
		require.NoError(t, repo.Create(ctx, &extension_state_entity.ExtensionState{Name: name, Enabled: enabled}))

		var stored int
		require.NoError(t, gdb.Raw("SELECT enabled FROM extension_state WHERE name = ?", name).Scan(&stored).Error)
		require.Equal(t, map[bool]int{false: 0, true: 1}[enabled], stored, name)

		row, err := repo.Find(ctx, name)
		require.NoError(t, err)
		require.Equal(t, enabled, row.Enabled, name)
	}
}
