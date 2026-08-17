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
		name          string
		toolName      string
		argsJSON      string
		infoAssetID   int64
		infoAssetName string
		execErr       error
		result        string
		decision      aictx.CheckResult
		wantToolName  string
		wantCommand   string
		wantSuccess   int
		wantErrorSet  bool
	}{
		// 传输面只剩 cp 一个工具名（upload_file / download_file 及其 RegisterToolAlias
		// 一并退役，spec §6.3 / §7），但它有**两个生产者**，各自把资产归属交给这里的方式
		// 不同——两条 cp 用例因此一边一个，不能都用同一种参数形状：那样另一个生产者就没人
		// 覆盖了，而它恰恰回归过（AI 侧的 cp 一度全部落成 asset_id=0）。
		//
		//   - AI 工具调用：参数只有端点串（src/dst），里面没有任何 id 可解析。归属由
		//     runner.auditMiddleware 预先解析好，经 ToolCallInfo.AssetID/AssetName 传进来
		//     （package audit 依赖不了 assetref，会与 permission 成环）。这一行同时钉住
		//     "预解析优先于 args 解析"：它的 args 里没有 asset_id，靠回落是解析不出东西的。
		//   - opsctl：自己 resolveAsset 之后把数字 id 写进 args["asset_id"]，走 WriteToolCall
		//     里的 args 回落链。
		//
		// 两条用例的**决策**也各不相同：被拒的那条必须落成 success=0 且 error 非空，
		// 否则一次被拒绝的传输在审计里与一次成功的传输长得一样。
		{
			name:          "cp denied (AI tool call: endpoints only, attribution pre-resolved)",
			toolName:      "cp",
			argsJSON:      `{"src":"/tmp/deny.bin","dst":"controlled-sftp:/srv/deny.bin"}`,
			infoAssetID:   1,
			infoAssetName: "controlled-sftp",
			execErr:       errors.New("USER DENIED: transfer rejected"),
			decision:      aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny},
			wantToolName:  "cp",
			wantCommand:   "cp /tmp/deny.bin → controlled-sftp:/srv/deny.bin",
			wantSuccess:   0,
			wantErrorSet:  true,
		},
		{
			name:         "cp allowed (opsctl: asset id in args)",
			toolName:     "cp",
			argsJSON:     `{"asset_id":1,"src":"controlled-sftp:/srv/app.log","dst":"/tmp/app.log"}`,
			decision:     aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow},
			wantToolName: "cp",
			wantCommand:  "cp controlled-sftp:/srv/app.log → /tmp/app.log",
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
				ToolName:  tc.toolName,
				ArgsJSON:  tc.argsJSON,
				AssetID:   tc.infoAssetID,
				AssetName: tc.infoAssetName,
				Result:    tc.result,
				Error:     tc.execErr,
				Decision:  &tc.decision,
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

	argsJSON := `{"name":"prod","config":{"host":"db.internal","password":"<redacted>","private_key":"stored-private-key"}}`
	result := `{"id":7,"config":{"apiKey":"stored-result-secret"}}`
	command := `client --token <redacted>`
	errMsg := "Authorization: Bearer stored-error-secret"
	writer.WriteToolCall(aictx.WithSessionID(context.Background(), "sensitive-fields"), ToolCallInfo{
		ToolName: "put_asset",
		ArgsJSON: argsJSON,
		Result:   result,
		Command:  command,
		Error:    errors.New(errMsg),
	})
	var sensitive audit_entity.AuditLog
	require.NoError(t, gdb.Where("session_id = ?", "sensitive-fields").First(&sensitive).Error)
	// 默认 writer 原样落库：既有秘密文本与字面 <redacted> 都按原值保存（spec §“Audit
	// raw-by-default and producer projections”）。
	require.Equal(t, argsJSON, sensitive.Request)
	require.Equal(t, result, sensitive.Result)
	require.Equal(t, command, sensitive.Command)
	require.Equal(t, errMsg, sensitive.Error)
	require.Contains(t, sensitive.Request, "db.internal")
	require.Contains(t, sensitive.Request, "<redacted>")
	require.Contains(t, sensitive.Result, "stored-result-secret")
	require.Contains(t, sensitive.Error, "stored-error-secret")
}
