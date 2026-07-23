package audit

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/migrations"
)

func TestDefaultAuditWriterPersistsToolAuditSemantics(t *testing.T) {
	dataDir := t.TempDir()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(dataDir, "opskat.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, migrations.RunMigrations(gdb, dataDir))

	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	originalAssetRepo := asset_repo.Asset()
	originalAuditRepo := audit_repo.Audit()
	db.SetDefault(gdb)
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	audit_repo.RegisterAudit(audit_repo.NewAudit())
	t.Cleanup(func() {
		audit_repo.RegisterAudit(originalAuditRepo)
		asset_repo.RegisterAsset(originalAssetRepo)
		require.NoError(t, sqlDB.Close())
	})

	asset := &asset_entity.Asset{
		Name:   "controlled-sftp",
		Type:   asset_entity.AssetTypeSSH,
		Status: asset_entity.StatusActive,
	}
	require.NoError(t, gdb.Create(asset).Error)

	tests := []struct {
		name         string
		toolName     string
		argsJSON     string
		execErr      error
		result       string
		decision     aictx.CheckResult
		wantToolName string
		wantCommand  string
		wantSuccess  int
		wantErrorSet bool
	}{
		{
			name:         "upload denied",
			toolName:     "upload_file",
			argsJSON:     `{"asset_id":1,"local_path":"/tmp/deny.bin","remote_path":"/srv/deny.bin"}`,
			execErr:      errors.New("USER DENIED: transfer rejected"),
			decision:     aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny},
			wantToolName: "cp",
			wantCommand:  "upload /tmp/deny.bin → /srv/deny.bin",
			wantSuccess:  0,
			wantErrorSet: true,
		},
		{
			name:         "upload allowed",
			toolName:     "upload_file",
			argsJSON:     `{"asset_id":1,"local_path":"/tmp/upload.bin","remote_path":"/srv/upload.bin"}`,
			decision:     aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow},
			wantToolName: "cp",
			wantCommand:  "upload /tmp/upload.bin → /srv/upload.bin",
			wantSuccess:  1,
		},
		{
			name:         "download allowed",
			toolName:     "download_file",
			argsJSON:     `{"asset_id":1,"remote_path":"/srv/download.bin","local_path":"/tmp/download.bin"}`,
			decision:     aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow},
			wantToolName: "cp",
			wantCommand:  "download /srv/download.bin → /tmp/download.bin",
			wantSuccess:  1,
		},
		{
			name:         "command denied without handler error",
			toolName:     "exec",
			argsJSON:     `{"asset":"1","command":"cat /etc/shadow"}`,
			decision:     aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny},
			result:       "USER DENIED: command rejected",
			wantToolName: "exec",
			wantCommand:  "cat /etc/shadow",
			wantSuccess:  0,
			wantErrorSet: true,
		},
	}

	writer := NewDefaultAuditWriter()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := aictx.WithAuditSource(context.Background(), "ai")
			ctx = aictx.WithSessionID(ctx, tc.name)
			writer.WriteToolCall(ctx, ToolCallInfo{
				ToolName: tc.toolName,
				ArgsJSON: tc.argsJSON,
				Result:   tc.result,
				Error:    tc.execErr,
				Decision: &tc.decision,
			})

			var stored audit_entity.AuditLog
			require.NoError(t, gdb.Where("session_id = ?", tc.name).First(&stored).Error)
			require.Equal(t, "ai", stored.Source)
			require.Equal(t, tc.wantToolName, stored.ToolName)
			require.Equal(t, asset.ID, stored.AssetID)
			require.Equal(t, asset.Name, stored.AssetName)
			require.Equal(t, tc.wantCommand, stored.Command)
			require.Equal(t, tc.wantSuccess, stored.Success)
			require.Equal(t, tc.decision.DecisionString(), stored.Decision)
			require.Equal(t, tc.decision.DecisionSource, stored.DecisionSource)
			require.Equal(t, tc.wantErrorSet, stored.Error != "")
		})
	}
}
