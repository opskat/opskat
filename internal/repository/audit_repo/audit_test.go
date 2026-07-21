package audit_repo

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/migrations"
)

func TestAuditRepoCreatePreservesExplicitSuccess(t *testing.T) {
	dataDir := t.TempDir()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opskat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.RunMigrations(gdb, dataDir))

	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqlDB.Close())
	})

	var databaseDefault string
	require.NoError(t, gdb.Raw(
		"SELECT dflt_value FROM pragma_table_info('audit_logs') WHERE name = 'success'",
	).Scan(&databaseDefault).Error)
	require.Equal(t, "1", databaseDefault, "the production schema must retain its historical default")

	db.SetDefault(gdb)
	repo := NewAudit()
	for _, success := range []int{0, 1} {
		t.Run("success="+strconv.Itoa(success), func(t *testing.T) {
			entry := &audit_entity.AuditLog{
				Source:     "ai",
				ToolName:   "exec",
				Success:    success,
				Createtime: 1,
			}
			require.NoError(t, repo.Create(context.Background(), entry))

			var stored audit_entity.AuditLog
			require.NoError(t, gdb.First(&stored, entry.ID).Error)
			require.Equal(t, success, stored.Success)
		})
	}
}
