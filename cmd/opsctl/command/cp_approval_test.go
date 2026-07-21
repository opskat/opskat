package command

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/sshpool"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type recordingAuditRepo struct {
	logs []*audit_entity.AuditLog
}

func (r *recordingAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	entry := *log
	r.logs = append(r.logs, &entry)
	return nil
}

func (r *recordingAuditRepo) List(context.Context, audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	return nil, 0, nil
}

func (r *recordingAuditRepo) ListSessions(context.Context, int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

// registerCpTestAsset 注册一个只认 assetID=1 的 mock asset repo，
// 让 "1:/path" 形式的远端路径能解析。
func registerCpTestAsset(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
		Return(&asset_entity.Asset{ID: 1, Name: "web-01", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() {
		if orig != nil {
			asset_repo.RegisterAsset(orig)
		}
	})
}

func TestCmdCpRequiresApproval(t *testing.T) {
	Convey("cp 被拒时不得发起任何传输", t, func() {
		registerCpTestAsset(t)
		originalProxyClient := cpSSHProxyClientFn
		cpSSHProxyClientFn = func() *sshpool.Client { return nil }
		defer func() { cpSSHProxyClientFn = originalProxyClient }()

		mockAudit := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mockAudit
		defer func() { opsctlAuditWriter = origWriter }()

		called := false
		handlers := map[string]tool.ToolHandlerFunc{
			"upload_file": func(_ context.Context, _ map[string]any) (string, error) {
				called = true
				return "", nil
			},
			"download_file": func(_ context.Context, _ map[string]any) (string, error) {
				called = true
				return "", nil
			},
		}

		var seen []approval.ApprovalRequest
		origApproval := cpApprovalFn
		cpApprovalFn = func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
			seen = append(seen, req)
			return ApprovalResult{Decision: aictx.Deny}, errors.New("user denied")
		}
		defer func() { cpApprovalFn = origApproval }()

		Convey("上传：审批主体是目的端路径", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{"/tmp/payload", "1:/etc/cron.d/backup"}, "")

			So(exitCode, ShouldEqual, 1)
			So(called, ShouldBeFalse)
			So(seen, ShouldHaveLength, 1)
			So(seen[0].Type, ShouldEqual, "cp")
			So(seen[0].AssetID, ShouldEqual, int64(1))
			So(seen[0].Command, ShouldEqual, "/etc/cron.d/backup")
		})

		Convey("下载：审批主体是源端路径", func() {
			exitCode := cmdCp(context.Background(), handlers, []string{"1:/etc/shadow", "/tmp/stolen"}, "")

			So(exitCode, ShouldEqual, 1)
			So(called, ShouldBeFalse)
			So(seen, ShouldHaveLength, 1)
			So(seen[0].Command, ShouldEqual, "/etc/shadow")
		})

		// 反证：called=false 必须是"被审批拦住"，不能是路径没解析成远端之类的提前返回
		Convey("批准后传输照常发起", func() {
			cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
				return ApprovalResult{Decision: aictx.Allow, SessionID: "sess-cp"}, nil
			}

			exitCode := cmdCp(context.Background(), handlers, []string{"/tmp/payload", "1:/etc/cron.d/backup"}, "")

			So(exitCode, ShouldEqual, 0)
			So(called, ShouldBeTrue)
		})
	})
}

func TestCmdCpDeniedAuditIsComplete(t *testing.T) {
	registerCpTestAsset(t)

	recordingRepo := &recordingAuditRepo{}
	originalRepo := audit_repo.Audit()
	originalWriter := opsctlAuditWriter
	originalApproval := cpApprovalFn
	audit_repo.RegisterAudit(recordingRepo)
	opsctlAuditWriter = audit.NewDefaultAuditWriter()
	cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourceUserDeny,
			SessionID:      "opsctl-cp-deny",
		}, errors.New("operation denied: user denied")
	}
	t.Cleanup(func() {
		cpApprovalFn = originalApproval
		opsctlAuditWriter = originalWriter
		audit_repo.RegisterAudit(originalRepo)
	})

	exitCode := cmdCp(context.Background(), nil, []string{"/tmp/payload.bin", "1:/srv/payload.bin"}, "")
	require.Equal(t, 1, exitCode)
	require.Len(t, recordingRepo.logs, 1)

	entry := recordingRepo.logs[0]
	require.Equal(t, "opsctl", entry.Source)
	require.Equal(t, "cp", entry.ToolName)
	require.Equal(t, int64(1), entry.AssetID)
	require.Equal(t, "web-01", entry.AssetName)
	require.Equal(t, "cp /tmp/payload.bin → 1:/srv/payload.bin", entry.Command)
	require.Equal(t, "deny", entry.Decision)
	require.Equal(t, aictx.SourceUserDeny, entry.DecisionSource)
	require.Zero(t, entry.Success)
	require.NotEmpty(t, entry.Error)
	require.Equal(t, "opsctl-cp-deny", entry.SessionID)
}

func TestCmdCpDirectAuditIsSingleAndConsistent(t *testing.T) {
	tests := []struct {
		name        string
		handlerErr  error
		wantExit    int
		wantSuccess int
	}{
		{name: "success", wantExit: 0, wantSuccess: 1},
		{name: "failure", handlerErr: errors.New("sftp write failed"), wantExit: 1, wantSuccess: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registerCpTestAsset(t)

			recordingRepo := &recordingAuditRepo{}
			originalRepo := audit_repo.Audit()
			originalWriter := opsctlAuditWriter
			originalApproval := cpApprovalFn
			originalProxyClient := cpSSHProxyClientFn
			audit_repo.RegisterAudit(recordingRepo)
			opsctlAuditWriter = audit.NewDefaultAuditWriter()
			cpSSHProxyClientFn = func() *sshpool.Client { return nil }
			cpApprovalFn = func(_ context.Context, _ approval.ApprovalRequest) (ApprovalResult, error) {
				return ApprovalResult{
					Decision:       aictx.Allow,
					DecisionSource: aictx.SourceUserAllow,
					SessionID:      "opsctl-cp-direct",
				}, nil
			}
			t.Cleanup(func() {
				cpSSHProxyClientFn = originalProxyClient
				cpApprovalFn = originalApproval
				opsctlAuditWriter = originalWriter
				audit_repo.RegisterAudit(originalRepo)
			})

			handlers := map[string]tool.ToolHandlerFunc{
				"upload_file": func(_ context.Context, _ map[string]any) (string, error) {
					return `{"status":"completed"}`, tc.handlerErr
				},
			}
			exitCode := cmdCp(context.Background(), handlers, []string{"/tmp/payload.bin", "1:/srv/payload.bin"}, "")
			require.Equal(t, tc.wantExit, exitCode)
			require.Len(t, recordingRepo.logs, 1, "cp must write exactly one audit row")

			entry := recordingRepo.logs[0]
			require.Equal(t, "opsctl", entry.Source)
			require.Equal(t, "cp", entry.ToolName)
			require.Equal(t, int64(1), entry.AssetID)
			require.Equal(t, "web-01", entry.AssetName)
			require.Equal(t, "upload /tmp/payload.bin → /srv/payload.bin", entry.Command)
			require.Equal(t, "allow", entry.Decision)
			require.Equal(t, aictx.SourceUserAllow, entry.DecisionSource)
			require.Equal(t, tc.wantSuccess, entry.Success)
			require.Equal(t, tc.handlerErr != nil, entry.Error != "")
			require.Equal(t, "opsctl-cp-direct", entry.SessionID)
		})
	}
}

func TestBuildCpAuditArgsIncludesBothRemoteAssets(t *testing.T) {
	argsJSON, err := buildCpAuditArgs("1:/srv/source.bin", "2:/srv/destination.bin", 1, 2, 2)
	require.NoError(t, err)

	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(argsJSON), &args))
	require.Equal(t, float64(2), args["asset_id"])
	require.Equal(t, float64(1), args["source_asset_id"])
	require.Equal(t, float64(2), args["destination_asset_id"])
	require.Equal(t, "cp 1:/srv/source.bin → 2:/srv/destination.bin", audit.ExtractCommandForAudit("cp", args))
}
