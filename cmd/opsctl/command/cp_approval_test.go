package command

import (
	"context"
	"errors"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

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
