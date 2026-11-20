package permission

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
)

// issue #384：没送去模型审核的命令也要写明原因。辅助审批问人时弹窗、审计里看得到「未送模型审核（…）」，
// Autopilot 拒绝时审计同样记下，返回给调用方的说明里有下一步怎么做。
func TestSkippedReviewRecordsWhy(t *testing.T) {
	cases := []struct {
		name    string
		req     PermissionRequest
		generic bool
		reason  string
		next    string // Autopilot 拒绝时返回给调用方的说明里应有的内容
	}{
		{name: "还要从管道读入内容", reason: aictx.ReviewReasonPipedInput, next: "-n",
			req: PermissionRequest{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: "systemctl restart nginx", PipedInput: true}},
		{name: "规则拆不开", reason: aictx.ReviewReasonPolicyUnparseable, next: "修正命令语法",
			req: PermissionRequest{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: `echo "`}},
		{name: "没有可执行的命令", reason: aictx.ReviewReasonNoCommand, next: "修正命令",
			req: PermissionRequest{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: "x=1"}},
		{name: "读取密文", reason: aictx.ReviewReasonSecretValue, next: "留给用户", generic: true,
			req: PermissionRequest{AssetType: asset_entity.AssetTypeGeneric, AssetID: 1, Command: "secret:token"}},
	}
	for _, c := range cases {
		for _, mode := range []string{policyent.PermissionModeAssisted, policyent.PermissionModeAutopilot} {
			t.Run(c.name+"/"+mode, func(t *testing.T) {
				var ctx context.Context
				if c.generic {
					ctx = reviewedGenericAsset(t, mode)
				} else {
					base, mockRepo, _ := setupPolicyTest(t)
					mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(mode, 0), nil).AnyTimes()
					ctx = base
				}
				ctx = aictx.WithPolicyLang(ctx, "zh-CN")
				f := registerFakeReviewer(t, reviewPass)

				r := CheckPermissions(ctx, []PermissionRequest{c.req})[0]

				assert.Empty(t, f.calls, "not sent to the reviewer")
				require.NotNil(t, r.Review, "the reason is recorded for the dialog and the audit")
				assert.Equal(t, aictx.ReviewInfo{Mode: mode, Outcome: aictx.ReviewSkipped, Reason: c.reason}, *r.Review)
				if mode == policyent.PermissionModeAssisted {
					assert.Equal(t, aictx.NeedConfirm, r.Decision)
					return
				}
				assert.Equal(t, aictx.Deny, r.Decision)
				assert.Equal(t, aictx.SourceAutopilotDeny, r.DecisionSource)
				assert.Contains(t, r.Message, "未送模型审核")
				assert.Contains(t, r.Message, c.next)
				assert.Contains(t, r.Message, "命令没有执行")
			})
		}
	}
}

// 规则拆不开时，Autopilot 返回给调用方的说明保留具体的语法错误，调用方据此改命令。
func TestSkippedReviewKeepsPolicyDetail(t *testing.T) {
	base, mockRepo, _ := setupPolicyTest(t)
	mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeAutopilot, 0), nil).AnyTimes()
	registerFakeReviewer(t, reviewPass)

	r := CheckPermission(aictx.WithPolicyLang(base, "zh-CN"), asset_entity.AssetTypeSSH, 1, `echo "`)

	assert.Equal(t, aictx.Deny, r.Decision)
	assert.Contains(t, r.Message, "策略无法逐条校验其中的子命令")
	assert.Contains(t, r.Message, "未送模型审核")
}
