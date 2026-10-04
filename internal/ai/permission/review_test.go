package permission

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/command_review_svc"
)

// fakeReviewer 返回预设结果，并记录收到的输入和批量调用次数。
type fakeReviewer struct {
	result  command_review_svc.Result
	calls   []command_review_svc.Input
	batches int
}

func (f *fakeReviewer) Review(ctx context.Context, in command_review_svc.Input) command_review_svc.Result {
	return f.ReviewBatch(ctx, []command_review_svc.Input{in})[0]
}

func (f *fakeReviewer) ReviewBatch(_ context.Context, ins []command_review_svc.Input) []command_review_svc.Result {
	f.batches++
	out := make([]command_review_svc.Result, len(ins))
	for i, in := range ins {
		f.calls = append(f.calls, in)
		out[i] = f.result
	}
	return out
}

func (f *fakeReviewer) TestModel(context.Context, command_review_svc.Config) (string, error) {
	return "", nil
}
func (f *fakeReviewer) Status() command_review_svc.Status          { return command_review_svc.Status{} }
func (f *fakeReviewer) SetConfigErrorListener(func(reason string)) {}

func registerFakeReviewer(t *testing.T, r command_review_svc.Result) *fakeReviewer {
	f := &fakeReviewer{result: r}
	orig := command_review_svc.Default()
	command_review_svc.Register(f)
	t.Cleanup(func() { command_review_svc.Register(orig) })
	return f
}

var (
	reviewPass   = command_review_svc.Result{Outcome: command_review_svc.OutcomePass, Model: "jev-1.13.0"}
	reviewReject = command_review_svc.Result{Outcome: command_review_svc.OutcomeReject, Failed: []string{"disruptive"}, Model: "jev-1.13.0"}
	reviewFail   = command_review_svc.Result{Outcome: command_review_svc.OutcomeFail, Reason: command_review_svc.ReasonTimeout}
)

// sshAsset 的策略只放行 ls，所以 "systemctl restart nginx" 会落到"需要人确认"。
func sshAsset(mode string, groupID int64) *asset_entity.Asset {
	return &asset_entity.Asset{
		ID:             1,
		Type:           asset_entity.AssetTypeSSH,
		GroupID:        groupID,
		PermissionMode: mode,
		CmdPolicy:      mustJSON(asset_entity.CommandPolicy{AllowList: []string{"ls *", "systemctl status *"}}),
	}
}

func TestApplyReview(t *testing.T) {
	Convey("模型审核只处理原本要问人的命令，并按权限模式处理结果", t, func() {
		ctx, mockRepo, groups := setupPolicyTest(t)
		const cmd = "systemctl restart nginx"

		Convey("默认模式不审核，结果和原来一样", func() {
			f := registerFakeReviewer(t, reviewPass)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset("", 0), nil).AnyTimes()

			r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
			So(r.Review, ShouldBeNil)
			So(f.calls, ShouldBeEmpty)
		})

		Convey("规则已经放行或拒绝的命令不审核", func() {
			f := registerFakeReviewer(t, reviewReject)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeAutopilot, 0), nil).AnyTimes()

			r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, "ls -la")
			So(r.Decision, ShouldEqual, aictx.Allow)
			So(r.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
			So(f.calls, ShouldBeEmpty)
		})

		Convey("辅助审批", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeAssisted, 0), nil).AnyTimes()

			Convey("审核通过：自动放行", func() {
				f := registerFakeReviewer(t, reviewPass)
				r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
				So(r.Decision, ShouldEqual, aictx.Allow)
				So(r.DecisionSource, ShouldEqual, aictx.SourceAssistedAllow)
				So(r.Review.Outcome, ShouldEqual, aictx.ReviewPass)
				// 按资产类型注册时声明的写法替换敏感信息
				So(f.calls, ShouldResemble, []command_review_svc.Input{{AssetType: asset_entity.AssetTypeSSH, Command: cmd, Syntax: command_review_svc.SyntaxShell}})
			})

			Convey("按别名检查（opsctl exec 传审批类型 exec）时，交给模型和缓存的是规范资产类型", func() {
				f := registerFakeReviewer(t, reviewPass)
				r := CheckPermission(ctx, "exec", 1, cmd)
				So(r.DecisionSource, ShouldEqual, aictx.SourceAssistedAllow)
				So(f.calls, ShouldResemble, []command_review_svc.Input{{AssetType: asset_entity.AssetTypeSSH, Command: cmd, Syntax: command_review_svc.SyntaxShell}})
			})

			Convey("还会从管道读入内容：模型看不到那部分，不送审，照常问人", func() {
				f := registerFakeReviewer(t, reviewPass)
				r := CheckPermissions(ctx, []PermissionRequest{{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: cmd, PipedInput: true}})[0]
				So(r.Decision, ShouldEqual, aictx.NeedConfirm)
				So(r.Review, ShouldBeNil)
				So(f.calls, ShouldBeEmpty)
			})

			Convey("审核未通过：仍然问人，保留规则提示并带上审核结果", func() {
				registerFakeReviewer(t, reviewReject)
				r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
				So(r.Decision, ShouldEqual, aictx.NeedConfirm)
				So(r.HintRules, ShouldContain, "systemctl status *")
				So(r.Review.Outcome, ShouldEqual, aictx.ReviewReject)
				So(r.Review.Failed, ShouldResemble, []string{"disruptive"})
				So(r.Review.Mode, ShouldEqual, policyent.PermissionModeAssisted)
			})

			Convey("审核失败：仍然问人", func() {
				registerFakeReviewer(t, reviewFail)
				r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
				So(r.Decision, ShouldEqual, aictx.NeedConfirm)
				So(r.Review.Outcome, ShouldEqual, aictx.ReviewFail)
				So(r.Review.Reason, ShouldEqual, command_review_svc.ReasonTimeout)
			})
		})

		Convey("Autopilot", func() {
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeAutopilot, 0), nil).AnyTimes()

			Convey("审核通过：自动放行", func() {
				registerFakeReviewer(t, reviewPass)
				r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
				So(r.Decision, ShouldEqual, aictx.Allow)
				So(r.DecisionSource, ShouldEqual, aictx.SourceAutopilotAllow)
			})

			Convey("还会从管道读入内容：不送审，直接拒绝，告诉调用方怎么改", func() {
				f := registerFakeReviewer(t, reviewPass)
				r := CheckPermissions(aictx.WithPolicyLang(ctx, "zh-CN"), []PermissionRequest{{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: cmd, PipedInput: true}})[0]
				So(r.Decision, ShouldEqual, aictx.Deny)
				So(r.DecisionSource, ShouldEqual, aictx.SourceAutopilotDeny)
				So(r.Message, ShouldContainSubstring, "管道")
				So(r.Message, ShouldContainSubstring, "/dev/null")
				So(f.calls, ShouldBeEmpty)
			})

			Convey("还会从管道读入内容，但规则已经放行：和原来一样放行", func() {
				f := registerFakeReviewer(t, reviewReject)
				r := CheckPermissions(ctx, []PermissionRequest{{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: "ls -la", PipedInput: true}})[0]
				So(r.Decision, ShouldEqual, aictx.Allow)
				So(r.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
				So(f.calls, ShouldBeEmpty)
			})

			Convey("审核未通过：直接拒绝，告诉调用方不要原样重试", func() {
				registerFakeReviewer(t, reviewReject)
				r := CheckPermission(aictx.WithPolicyLang(ctx, "zh-CN"), asset_entity.AssetTypeSSH, 1, cmd)
				So(r.Decision, ShouldEqual, aictx.Deny)
				So(r.DecisionSource, ShouldEqual, aictx.SourceAutopilotDeny)
				So(r.Message, ShouldContainSubstring, "模型审核未通过")
				So(r.Message, ShouldContainSubstring, "不要原样重试")
				So(r.Review.Outcome, ShouldEqual, aictx.ReviewReject)
				So(r.Review.Mode, ShouldEqual, policyent.PermissionModeAutopilot)
			})

			Convey("审核失败：直接拒绝，写明原因；只有临时性的失败才让调用方稍后重试", func() {
				cases := []struct {
					reason, want string
					retryLater   bool
				}{
					{command_review_svc.ReasonTimeout, "超时", true},
					{command_review_svc.ReasonUnavailable, "服务不可用", true},
					// 原样重试结果一样，告诉调用方该怎么改
					{command_review_svc.ReasonTooLong, "缩短或拆成几条", false},
					{command_review_svc.ReasonUnparseable, "修正命令语法", false},
					{command_review_svc.ReasonUndecodable, "直接写出来", false},
					// 配置问题只有用户能修
					{command_review_svc.ReasonNotConfigured, "留给用户", false},
					{command_review_svc.ReasonAPIKeyUnreadable, "API key 无法读取", false},
					{command_review_svc.ReasonInvalidAPIKey, "留给用户", false},
				}
				for _, c := range cases {
					registerFakeReviewer(t, command_review_svc.Result{Outcome: command_review_svc.OutcomeFail, Reason: c.reason})
					r := CheckPermission(aictx.WithPolicyLang(ctx, "zh-CN"), asset_entity.AssetTypeSSH, 1, cmd)
					So(r.Decision, ShouldEqual, aictx.Deny)
					So(r.DecisionSource, ShouldEqual, aictx.SourceAutopilotDeny)
					So(r.Message, ShouldContainSubstring, "模型审核失败")
					So(r.Message, ShouldContainSubstring, c.want)
					if c.retryLater {
						So(r.Message, ShouldContainSubstring, "稍后重试")
					} else {
						So(r.Message, ShouldNotContainSubstring, "稍后重试")
					}
				}
			})
		})

		// 拆不开的 shell 命令，禁止规则没法逐条检查，只能由人判断：不交给模型，
		// 否则 Autopilot 会让模型放行一条本该被禁止规则拦下的命令。
		Convey("拆不开的 shell 命令不交给模型审核", func() {
			withDeny := func(mode string) *asset_entity.Asset {
				a := sshAsset(mode, 0)
				a.CmdPolicy = mustJSON(asset_entity.CommandPolicy{AllowList: []string{"ls *"}, DenyList: []string{"docker compose down"}})
				return a
			}
			for _, unenumerable := range []string{"docker compose down 'unterminated", "FOO=bar"} {
				Convey("Autopilot 直接拒绝："+unenumerable, func() {
					f := registerFakeReviewer(t, reviewPass)
					mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(withDeny(policyent.PermissionModeAutopilot), nil).AnyTimes()
					r := CheckPermission(aictx.WithPolicyLang(ctx, "zh-CN"), asset_entity.AssetTypeSSH, 1, unenumerable)
					So(f.calls, ShouldBeEmpty)
					So(r.Decision, ShouldEqual, aictx.Deny)
					So(r.DecisionSource, ShouldEqual, aictx.SourceAutopilotDeny)
					So(r.Message, ShouldContainSubstring, "不会把无法逐条检查的命令交给模型审核")
					So(r.Review, ShouldBeNil)
				})
				Convey("辅助审批仍然问人："+unenumerable, func() {
					f := registerFakeReviewer(t, reviewPass)
					mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(withDeny(policyent.PermissionModeAssisted), nil).AnyTimes()
					r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, unenumerable)
					So(f.calls, ShouldBeEmpty)
					So(r.Decision, ShouldEqual, aictx.NeedConfirm)
					So(r.Review, ShouldBeNil)
				})
			}
		})

		Convey("资产没有设置时沿用分组链上最近的设置", func() {
			groups.groups[10] = &group_entity.Group{ID: 10, ParentID: 20}
			groups.groups[20] = &group_entity.Group{ID: 20, PermissionMode: policyent.PermissionModeAutopilot}
			registerFakeReviewer(t, reviewReject)

			Convey("资产为空：用上级分组的 Autopilot", func() {
				mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset("", 10), nil).AnyTimes()
				r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
				So(r.DecisionSource, ShouldEqual, aictx.SourceAutopilotDeny)
			})

			Convey("资产显式设为默认：不受分组影响", func() {
				mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeDefault, 10), nil).AnyTimes()
				r := CheckPermission(ctx, asset_entity.AssetTypeSSH, 1, cmd)
				So(r.Decision, ShouldEqual, aictx.NeedConfirm)
				So(r.Review, ShouldBeNil)
			})
		})

		Convey("辅助审批审核未通过时，审批项带上审核结果，人批准后结果里也保留", func() {
			registerFakeReviewer(t, reviewReject)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeAssisted, 0), nil).AnyTimes()

			var shown []ApprovalItem
			checker := NewCommandPolicyChecker(func(_ context.Context, _ string, items []ApprovalItem) ApprovalResponse {
				shown = items
				return ApprovalResponse{Decision: "allow"}
			})
			r := checker.CheckForAsset(ctx, 1, asset_entity.AssetTypeSSH, cmd)

			So(shown, ShouldHaveLength, 1)
			So(shown[0].Review, ShouldNotBeNil)
			So(shown[0].Review.Outcome, ShouldEqual, aictx.ReviewReject)
			So(r.Decision, ShouldEqual, aictx.Allow)
			So(r.DecisionSource, ShouldEqual, aictx.SourceUserAllow)
			So(r.Review.Outcome, ShouldEqual, aictx.ReviewReject)
			// 人批准的记录里只看得到 user_allow，模式要靠审核结果自己带着，审计才看得出是辅助审批转过来的
			So(r.Review.Mode, ShouldEqual, policyent.PermissionModeAssisted)
		})

		Convey("批量检查：规则放行的原样返回，需要审核的一次交给 ReviewBatch，结果顺序不变", func() {
			f := registerFakeReviewer(t, reviewPass)
			mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(sshAsset(policyent.PermissionModeAssisted, 0), nil).AnyTimes()

			rs := CheckPermissions(ctx, []PermissionRequest{
				{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: "ls -la"},
				{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: cmd},
				{AssetType: asset_entity.AssetTypeSSH, AssetID: 1, Command: "systemctl stop nginx"},
			})
			So(rs, ShouldHaveLength, 3)
			So(rs[0].DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
			So(rs[1].DecisionSource, ShouldEqual, aictx.SourceAssistedAllow)
			So(rs[2].DecisionSource, ShouldEqual, aictx.SourceAssistedAllow)
			So(f.batches, ShouldEqual, 1)
			So(f.calls, ShouldHaveLength, 2)
			So(f.calls[0].Command, ShouldEqual, cmd)
			So(f.calls[1].Command, ShouldEqual, "systemctl stop nginx")
		})
	})
}

func TestAutopilotGrantRequests(t *testing.T) {
	Convey("Autopilot 是无人值守：它的资产不走授权申请，不弹窗问人", t, func() {
		ctx, mockRepo, _ := setupPolicyTest(t)
		ctx = aictx.WithPolicyLang(ctx, "zh-CN")
		asset := func(id int64, name, mode string) *asset_entity.Asset {
			return &asset_entity.Asset{ID: id, Name: name, Type: asset_entity.AssetTypeSSH, PermissionMode: mode}
		}
		mockRepo.EXPECT().Find(gomock.Any(), int64(1)).Return(asset(1, "web-auto", policyent.PermissionModeAutopilot), nil).AnyTimes()
		mockRepo.EXPECT().Find(gomock.Any(), int64(2)).Return(asset(2, "web-default", policyent.PermissionModeDefault), nil).AnyTimes()
		mockRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(asset(3, "web-assisted", policyent.PermissionModeAssisted), nil).AnyTimes()

		var asked []ApprovalItem
		checker := NewCommandPolicyChecker(nil)
		checker.SetGrantRequestFunc(func(_ context.Context, items []ApprovalItem, _ string) (bool, []string) {
			asked = append(asked, items...)
			patterns := make([]string, 0, len(items))
			for _, it := range items {
				patterns = append(patterns, it.Command)
			}
			return true, patterns
		})

		Convey("全是 Autopilot 资产：不弹审批，告诉调用方直接执行、由模型逐条审核", func() {
			r := checker.SubmitGrantMulti(ctx, []GrantItem{{AssetID: 1, Patterns: []string{"ufw --force delete *"}}}, "加固")
			So(asked, ShouldBeEmpty)
			So(r.Decision, ShouldEqual, aictx.Deny)
			So(r.DecisionSource, ShouldEqual, aictx.SourceAutopilotDeny)
			So(r.Message, ShouldContainSubstring, "web-auto")
			So(r.Message, ShouldContainSubstring, "直接执行")
			// 不是用户拒绝，不能让调用方停掉整个任务
			So(r.Message, ShouldNotContainSubstring, "停止当前任务")
		})

		Convey("混有其他资产：只把非 Autopilot 的部分交给人，并说明跳过了哪些", func() {
			r := checker.SubmitGrantMulti(ctx, []GrantItem{
				{AssetID: 1, Patterns: []string{"ufw status*"}},
				{AssetID: 2, Patterns: []string{"systemctl * nginx"}},
			}, "加固")
			So(asked, ShouldHaveLength, 1)
			So(asked[0].AssetID, ShouldEqual, 2)
			So(r.Decision, ShouldEqual, aictx.Allow)
			So(r.MatchedPattern, ShouldEqual, "systemctl * nginx")
			So(r.Message, ShouldContainSubstring, "web-auto")
		})

		Convey("辅助审批有人在场，照常申请授权", func() {
			r := checker.SubmitGrantMulti(ctx, []GrantItem{{AssetID: 3, Patterns: []string{"systemctl * nginx"}}}, "加固")
			So(asked, ShouldHaveLength, 1)
			So(r.Decision, ShouldEqual, aictx.Allow)
		})
	})
}
