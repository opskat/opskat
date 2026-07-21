package tool

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// withDenyingChecker 注册一个必然拒绝的审批回调，并返回收集到的审批项。
// 同时注册 mock asset repo —— HandleConfirm 会解析资产名，asset_repo 未注册时
// 是空指针崩溃而不是测试失败。
func withDenyingChecker(t *testing.T) (context.Context, *[]permission.ApprovalItem) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
		Return(&asset_entity.Asset{ID: 1, Name: "web-01", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() {
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
	})

	seen := &[]permission.ApprovalItem{}
	checker := permission.NewCommandPolicyChecker(
		func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
			So(kind, ShouldEqual, "single")
			*seen = append(*seen, items...)
			return permission.ApprovalResponse{Decision: "deny"}
		})
	return permission.WithPolicyChecker(context.Background(), checker), seen
}

func TestUploadFileRequiresApproval(t *testing.T) {
	Convey("upload_file 在传输前必须过审批", t, func() {
		ctx, seen := withDenyingChecker(t)

		_, err := handleUploadFile(ctx, map[string]any{
			"asset_id":    float64(1),
			"local_path":  "/tmp/payload",
			"remote_path": "/etc/cron.d/backup",
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "USER DENIED")
		So(*seen, ShouldHaveLength, 1)
		So((*seen)[0].Type, ShouldEqual, "cp")
		// 审批主体是远端路径：grant 按资产存，本地路径不属于任何资产
		So((*seen)[0].Command, ShouldEqual, "/etc/cron.d/backup")
		So((*seen)[0].Detail, ShouldContainSubstring, "/tmp/payload")
	})
}

func TestDownloadFileRequiresApproval(t *testing.T) {
	Convey("download_file 在传输前必须过审批", t, func() {
		ctx, seen := withDenyingChecker(t)

		_, err := handleDownloadFile(ctx, map[string]any{
			"asset_id":    float64(1),
			"remote_path": "/etc/shadow",
			"local_path":  "/tmp/stolen",
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "USER DENIED")
		So(*seen, ShouldHaveLength, 1)
		So((*seen)[0].Type, ShouldEqual, "cp")
		So((*seen)[0].Command, ShouldEqual, "/etc/shadow")
		So((*seen)[0].Detail, ShouldContainSubstring, "/tmp/stolen")
	})
}
