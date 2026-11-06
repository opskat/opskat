package permission

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
)

// 权限模式 × 审核结果：只有审核通过能把"需要确认"改成放行；未通过、失败在辅助审批下问人，
// 在 Autopilot 下拒绝；默认模式不送审，结果和没有审核时一样。
func TestReviewOutcomeMatrix(t *testing.T) {
	const cmd = "systemctl restart nginx"
	type want struct {
		decision aictx.Decision
		source   string
		reviewed bool
	}
	cases := []struct {
		mode   string
		review aictx.ReviewInfo
		want   want
	}{
		{policyent.PermissionModeDefault, reviewPass, want{aictx.NeedConfirm, "", false}},
		{policyent.PermissionModeDefault, reviewReject, want{aictx.NeedConfirm, "", false}},
		{policyent.PermissionModeDefault, reviewFail, want{aictx.NeedConfirm, "", false}},
		{policyent.PermissionModeAssisted, reviewPass, want{aictx.Allow, aictx.SourceAssistedAllow, true}},
		{policyent.PermissionModeAssisted, reviewReject, want{aictx.NeedConfirm, "", true}},
		{policyent.PermissionModeAssisted, reviewFail, want{aictx.NeedConfirm, "", true}},
		{policyent.PermissionModeAutopilot, reviewPass, want{aictx.Allow, aictx.SourceAutopilotAllow, true}},
		{policyent.PermissionModeAutopilot, reviewReject, want{aictx.Deny, aictx.SourceAutopilotDeny, true}},
		{policyent.PermissionModeAutopilot, reviewFail, want{aictx.Deny, aictx.SourceAutopilotDeny, true}},
	}
	for _, c := range cases {
		Convey(c.mode+" × "+string(c.review.Outcome), t, func() {
			ctx, mockRepo, _ := setupPolicyTest(t)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(c.mode, 0), nil).AnyTimes()
			f := registerFakeReviewer(t, c.review)

			r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
			So(r.Decision, ShouldEqual, c.want.decision)
			if c.want.source != "" {
				So(r.DecisionSource, ShouldEqual, c.want.source)
			}
			if !c.want.reviewed {
				So(f.calls, ShouldBeEmpty)
				So(r.Review, ShouldBeNil)
				return
			}
			So(f.calls, ShouldHaveLength, 1)
			So(r.Review, ShouldNotBeNil)
			So(r.Review.Outcome, ShouldEqual, c.review.Outcome)
			So(r.Review.Mode, ShouldEqual, c.mode)
		})
	}
}

// 没有注入审核端口（例如没经过启动代码的进程）：需要审核的命令按没有审核器失败，
// 辅助审批问人、Autopilot 拒绝，绝不因此放行；默认模式和原来一样。
func TestNoReviewerPortFailsClosed(t *testing.T) {
	Convey("没有注入审核端口", t, func() {
		ctx, mockRepo, _ := setupPolicyTest(t)
		orig := reviewer
		SetReviewer(nil)
		t.Cleanup(func() { SetReviewer(orig) })
		const cmd = "systemctl restart nginx"

		Convey("辅助审批：问人，带上审核失败", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeAssisted, 0), nil).AnyTimes()
			r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
			So(r.Review, ShouldNotBeNil)
			So(r.Review.Outcome, ShouldEqual, aictx.ReviewFail)
			So(r.Review.Reason, ShouldEqual, aictx.ReviewReasonNoReviewer)
			So(r.Review.Mode, ShouldEqual, policyent.PermissionModeAssisted)
		})

		Convey("Autopilot：拒绝", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeAutopilot, 0), nil).AnyTimes()
			r := CheckPermission(aictx.WithPolicyLang(ctx, "zh-CN"), asset_entity.AssetTypeSSH, 1, cmd)
			So(r.Decision, ShouldEqual, aictx.Deny)
			So(r.DecisionSource, ShouldEqual, aictx.SourceAutopilotDeny)
			So(r.Message, ShouldContainSubstring, "没有可用的命令审核扩展")
			So(r.Review, ShouldNotBeNil)
			So(r.Review.Reason, ShouldEqual, aictx.ReviewReasonNoReviewer)
		})

		Convey("默认模式：不审核", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeDefault, 0), nil).AnyTimes()
			r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
			So(r.Review, ShouldBeNil)
		})
	})
}

// 内置类型按注册时声明的写法送审；扩展资产类型（注册了策略检查的和没注册的）按纯文本。
func TestReviewSyntaxDeclaredAtRegistration(t *testing.T) {
	const extType = "review-syntax-ext-type"
	if err := RegisterPolicyCheck(extType, func(context.Context, int64, string) aictx.CheckResult { return aictx.CheckResult{} }, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { UnregisterPolicyCheck(extType) })

	for _, c := range []struct {
		assetType string
		want      Syntax
	}{
		{asset_entity.AssetTypeSSH, SyntaxShell},
		{asset_entity.AssetTypeRedis, SyntaxRedis},
		{asset_entity.AssetTypeDatabase, SyntaxText},
		{extType, SyntaxText},
		{"review-syntax-unregistered", SyntaxText},
	} {
		if _, got, _ := reviewTypeFor(c.assetType); got != c.want {
			t.Errorf("%s: syntax = %q, want %q", c.assetType, got, c.want)
		}
	}
}
