package permission

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/repository/policy_group_repo"

	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"
)

// 拆不出执行单元的 shell 命令（解析失败，或解析成功却没有任何子命令）：
// 远端 shell 与本地解析器的语法边界不一致——bash 逐行读、逐行执行，本地整串拒绝的输入
// 前几行照样会跑——所以这类命令上具体 deny 规则无法判定。契约：
//   - 生效 deny 里有独立 "*" → Deny；
//   - 有独立 allow "*" 且没有任何命令 deny 规则（cp: 文件传输规则不算）→ Allow；
//   - 其余 → NeedConfirm，Message 带上解析失败原因，引导修正命令；
//   - 空白命令永不放行。
const unparseableShell = `echo "`

func TestCheckPermission_ShellUnenumerable(t *testing.T) {
	Convey("SSH：独立 * 与拆不出子命令的命令", t, func() {
		ctx, mockAsset, stubGrp := setupPolicyTest(t)
		asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeSSH}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
		setPolicy := func(p asset_entity.CommandPolicy) { asset.CmdPolicy = mustJSON(p) }

		Convey("allow * + 普通命令 → Allow，命中 *", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}})
			result := CheckPermission(ctx, "ssh", 1, "uptime")
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.MatchedPattern, ShouldEqual, "*")
		})

		Convey("allow * + 解析失败 → Allow，policy_allow，命中 *", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}})
			result := CheckPermission(ctx, "ssh", 1, unparseableShell)
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
			So(result.MatchedPattern, ShouldEqual, "*")
		})

		Convey("allow * + 空字符串 / 纯空白 → 不放行", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}})
			So(CheckPermission(ctx, "ssh", 1, "").Decision, ShouldNotEqual, aictx.Allow)
			So(CheckPermission(ctx, "ssh", 1, "   \n\t").Decision, ShouldNotEqual, aictx.Allow)
		})

		Convey("deny * + allow * → Deny 优先（解析失败与可解析都一样）", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}, DenyList: []string{"*"}})
			result := CheckPermission(ctx, "ssh", 1, unparseableShell)
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
			So(result.MatchedPattern, ShouldEqual, "*")
			So(CheckPermission(ctx, "ssh", 1, "uptime").Decision, ShouldEqual, aictx.Deny)
		})

		Convey("allow * + 具体 deny + 解析失败 → NeedConfirm，Message 带解析失败原因", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}, DenyList: []string{"reboot *"}})
			// bash 会先执行第一行 reboot 再在第二行报语法错；deny 无法判定就不能放行。
			result := CheckPermission(ctx, "ssh", 1, "reboot\n"+unparseableShell)
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
			So(result.Message, ShouldContainSubstring, "reached EOF without closing quote")
		})

		Convey("allow * + 内置 dangerous-deny 组 + 解析失败 → NeedConfirm", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}, Groups: []string{policyent.BuiltinDangerousDeny}})
			So(CheckPermission(ctx, "ssh", 1, unparseableShell).Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("deny 里只有 cp: 文件传输规则不算命令 deny → allow * 仍放行", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}, DenyList: []string{"cp:read:/etc/*"}})
			result := CheckPermission(ctx, "ssh", 1, unparseableShell)
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.MatchedPattern, ShouldEqual, "*")
		})

		Convey("没有 allow * + 解析失败 → NeedConfirm，同样带解析失败原因", func() {
			setPolicy(asset_entity.CommandPolicy{})
			result := CheckPermission(ctx, "ssh", 1, unparseableShell)
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
			So(result.Message, ShouldContainSubstring, "reached EOF without closing quote")
		})

		Convey("普通规则 ls * 不因解析失败放行", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"ls *", "curl *"}})
			So(CheckPermission(ctx, "ssh", 1, `ls "`).Decision, ShouldEqual, aictx.NeedConfirm)
			So(CheckPermission(ctx, "ssh", 1, `curl "`).Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("解析成功但没有子命令（纯赋值）与解析失败同一套判定", func() {
			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}})
			result := CheckPermission(ctx, "ssh", 1, "x=1")
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.MatchedPattern, ShouldEqual, "*")

			setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}, DenyList: []string{"reboot *"}})
			result = CheckPermission(ctx, "ssh", 1, "x=1")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
			So(result.Message, ShouldNotBeEmpty)
		})

		Convey("多层策略", func() {
			asset.CmdPolicy = ""
			asset.GroupID = 10
			pgStub := &stubPolicyGroupRepo{groups: map[int64]*policy_group_entity.PolicyGroup{}}
			origPG := policy_group_repo.PolicyGroup()
			policy_group_repo.RegisterPolicyGroup(pgStub)
			t.Cleanup(func() { policy_group_repo.RegisterPolicyGroup(origPG) })
			setGroup := func(p asset_entity.CommandPolicy) {
				stubGrp.groups[10] = &group_entity.Group{ID: 10, Name: "ops", CmdPolicy: mustJSON(p)}
			}
			setPolicyGroup := func(p asset_entity.CommandPolicy) {
				pgStub.groups[7] = &policy_group_entity.PolicyGroup{
					ID: 7, Name: "full", PolicyType: policy_group_entity.PolicyTypeCommand, Policy: mustJSON(p),
				}
			}
			setGroup(asset_entity.CommandPolicy{})

			Convey("* 来自资产组链", func() {
				setGroup(asset_entity.CommandPolicy{AllowList: []string{"*"}})
				result := CheckPermission(ctx, "ssh", 1, unparseableShell)
				So(result.Decision, ShouldEqual, aictx.Allow)
				So(result.MatchedPattern, ShouldEqual, "*")
			})

			Convey("* 来自已挂载的命令策略组", func() {
				setPolicyGroup(asset_entity.CommandPolicy{AllowList: []string{"*"}})
				setPolicy(asset_entity.CommandPolicy{Groups: []string{"7"}})
				result := CheckPermission(ctx, "ssh", 1, unparseableShell)
				So(result.Decision, ShouldEqual, aictx.Allow)
				So(result.MatchedPattern, ShouldEqual, "*")
			})

			Convey("资产 allow *，组链 deny * → Deny", func() {
				setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}})
				setGroup(asset_entity.CommandPolicy{DenyList: []string{"*"}})
				So(CheckPermission(ctx, "ssh", 1, unparseableShell).Decision, ShouldEqual, aictx.Deny)
			})

			Convey("资产 allow *，策略组 deny * → Deny", func() {
				setPolicyGroup(asset_entity.CommandPolicy{DenyList: []string{"*"}})
				setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}, Groups: []string{"7"}})
				So(CheckPermission(ctx, "ssh", 1, unparseableShell).Decision, ShouldEqual, aictx.Deny)
			})

			Convey("资产 allow *，组链里的具体 deny → NeedConfirm", func() {
				setPolicy(asset_entity.CommandPolicy{AllowList: []string{"*"}})
				setGroup(asset_entity.CommandPolicy{DenyList: []string{"reboot *"}})
				So(CheckPermission(ctx, "ssh", 1, unparseableShell).Decision, ShouldEqual, aictx.NeedConfirm)
			})
		})
	})

	Convey("Serial 与 SSH 共用命令策略", t, func() {
		ctx, mockAsset, _ := setupPolicyTest(t)
		asset := &asset_entity.Asset{
			ID: 1, Type: asset_entity.AssetTypeSerial,
			CmdPolicy: mustJSON(asset_entity.CommandPolicy{AllowList: []string{"*"}}),
		}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

		result := CheckPermission(ctx, asset_entity.AssetTypeSerial, 1, unparseableShell)
		So(result.Decision, ShouldEqual, aictx.Allow)
		So(result.MatchedPattern, ShouldEqual, "*")
	})

	Convey("K8s：组通用 CmdPolicy 与 K8s 策略两层合并判定", t, func() {
		ctx, mockAsset, stubGrp := setupPolicyTest(t)
		asset := &asset_entity.Asset{ID: 1, Type: asset_entity.AssetTypeK8s}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
		const unparseableKubectl = `kubectl get "`

		Convey("K8s allow * + 解析失败 → Allow，命中 *", func() {
			asset.CmdPolicy = mustJSON(asset_entity.K8sPolicy{AllowList: []string{"*"}})
			result := CheckPermission(ctx, asset_entity.AssetTypeK8s, 1, unparseableKubectl)
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
			So(result.MatchedPattern, ShouldEqual, "*")
		})

		Convey("K8s allow * + 具体 deny → NeedConfirm，带解析失败原因", func() {
			asset.CmdPolicy = mustJSON(asset_entity.K8sPolicy{AllowList: []string{"*"}, DenyList: []string{"kubectl delete *"}})
			result := CheckPermission(ctx, asset_entity.AssetTypeK8s, 1, unparseableKubectl)
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
			So(result.Message, ShouldContainSubstring, "reached EOF without closing quote")
		})

		Convey("组通用 deny * + K8s allow * → Deny", func() {
			asset.GroupID = 10
			stubGrp.groups[10] = &group_entity.Group{ID: 10, Name: "k8s", CmdPolicy: `{"deny_list":["*"]}`}
			asset.CmdPolicy = mustJSON(asset_entity.K8sPolicy{AllowList: []string{"*"}})
			So(CheckPermission(ctx, asset_entity.AssetTypeK8s, 1, unparseableKubectl).Decision, ShouldEqual, aictx.Deny)
		})

		Convey("组通用 allow *，但 K8s 默认策略带危险 deny → NeedConfirm", func() {
			asset.GroupID = 10
			stubGrp.groups[10] = &group_entity.Group{ID: 10, Name: "k8s", CmdPolicy: `{"allow_list":["*"]}`}
			asset.CmdPolicy = ""
			So(CheckPermission(ctx, asset_entity.AssetTypeK8s, 1, unparseableKubectl).Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
