package bootstrap

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/command_review_svc"
)

func TestCommandReviewTimeoutDefaultsWhenUnset(t *testing.T) {
	orig := appConfig
	t.Cleanup(func() { appConfig = orig })

	appConfig = &AppConfig{}
	assert.Equal(t, command_review_svc.DefaultTimeout, CommandReviewTimeout())
	appConfig = &AppConfig{CommandReviewTimeoutMs: 8000}
	assert.Equal(t, 8*time.Second, CommandReviewTimeout())
}

// 还没有审核器来源：审核按"没有审核器"失败。辅助审批照常问人，Autopilot 拒绝，
// 默认模式和以前一样，绝不因审核器缺失而放行。
func TestNoReviewerFailsClosedPerPermissionMode(t *testing.T) {
	origService := command_review_svc.Default()
	t.Cleanup(func() {
		command_review_svc.Register(origService)
		permission.SetReviewer(nil)
	})
	registerCommandReview()

	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAssets := asset_repo.Asset()
	asset_repo.RegisterAsset(assets)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAssets) })

	policy, err := json.Marshal(asset_entity.CommandPolicy{AllowList: []string{"ls *"}})
	require.NoError(t, err)
	assetWithMode := func(id int64, mode string) {
		assets.EXPECT().Find(gomock.Any(), id).Return(&asset_entity.Asset{
			ID: id, Type: asset_entity.AssetTypeSSH, PermissionMode: mode, CmdPolicy: string(policy),
		}, nil).AnyTimes()
	}
	assetWithMode(1, policyent.PermissionModeDefault)
	assetWithMode(2, policyent.PermissionModeAssisted)
	assetWithMode(3, policyent.PermissionModeAutopilot)
	assetWithMode(4, "") // 没设置、也没有分组可沿用：按默认

	ctx := context.Background()
	const cmd = "systemctl restart nginx" // 规则没放行也没拒绝：原本要问人

	for _, id := range []int64{1, 4} {
		def := permission.CheckPermission(ctx, asset_entity.AssetTypeSSH, id, cmd)
		assert.Equal(t, aictx.NeedConfirm, def.Decision)
		assert.Nil(t, def.Review, "default mode is not reviewed")
	}

	assisted := permission.CheckPermission(ctx, asset_entity.AssetTypeSSH, 2, cmd)
	assert.Equal(t, aictx.NeedConfirm, assisted.Decision)
	require.NotNil(t, assisted.Review)
	assert.Equal(t, aictx.ReviewFail, assisted.Review.Outcome)
	assert.Equal(t, aictx.ReviewReasonNoReviewer, assisted.Review.Reason)
	// 权限检查经注入的审核服务审核（而不是端口没注入时的兜底失败）：失败记进了服务的状态。
	assert.Equal(t, aictx.ReviewReasonNoReviewer, command_review_svc.Default().Status().LastFailReason)

	autopilot := permission.CheckPermission(ctx, asset_entity.AssetTypeSSH, 3, cmd)
	assert.Equal(t, aictx.Deny, autopilot.Decision)
	assert.Equal(t, aictx.SourceAutopilotDeny, autopilot.DecisionSource)
	require.NotNil(t, autopilot.Review)
	assert.Equal(t, aictx.ReviewReasonNoReviewer, autopilot.Review.Reason)
}
