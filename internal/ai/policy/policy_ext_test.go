package policy

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCheckExtensionPolicy(t *testing.T) {
	Convey("CheckExtensionPolicy", t, func() {
		ctx := context.Background()

		// Register test extension policy groups
		policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
			BuiltinID:  "ext:oss:readonly",
			Name:       "OSS Read-Only",
			PolicyType: "oss",
			Policy:     `{"allow_list":["list","read"],"deny_list":["delete","admin"]}`,
		})
		policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
			BuiltinID:  "ext:oss:dangerous-deny",
			Name:       "OSS Dangerous aictx.Deny",
			PolicyType: "oss",
			Policy:     `{"deny_list":["delete","admin"]}`,
		})

		Reset(func() {
			policy_group_entity.UnregisterExtensionGroups("oss")
		})

		check := func(groups []string, action string) aictx.CheckResult {
			return CheckExtensionPolicy(ctx, ExtensionCheck{PolicyType: "oss", GroupIDs: groups, Action: action})
		}

		Convey("aictx.Allow when action is in allow_list", func() {
			result := check([]string{"ext:oss:readonly"}, "read")
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
		})

		Convey("aictx.Deny when action is in deny_list", func() {
			result := check([]string{"ext:oss:readonly"}, "delete")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		})

		Convey("aictx.NeedConfirm when action not in any list", func() {
			result := check([]string{"ext:oss:readonly"}, "upload")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("Merging multiple groups: deny takes precedence", func() {
			// "ext:oss:readonly" has allow_list with "read", but also deny_list with "delete"
			// "ext:oss:dangerous-deny" has deny_list with "delete"
			// Even if one group allows "read", if another group denies it, deny wins.
			// Here test that "delete" is denied even across groups.
			result := check([]string{"ext:oss:readonly", "ext:oss:dangerous-deny"}, "delete")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)

			// "read" is only in allow_list, not in any deny_list → aictx.Allow
			result = check([]string{"ext:oss:readonly", "ext:oss:dangerous-deny"}, "read")
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
		})

		Convey("aictx.NeedConfirm when no groups configured", func() {
			result := check(nil, "read")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		// 一条 holder 自己的永久规则（opsctl policy allow 写的，前缀已由调用方还原）
		// 与权限组的规则同一层判定。
		Convey("The holder's own rules join the same decision", func() {
			own := func(rule ExtensionPolicyRule, action string) aictx.CheckResult {
				return CheckExtensionPolicy(ctx, ExtensionCheck{PolicyType: "oss", Own: rule, Action: action})
			}
			Convey("an own allow decides without any policy group", func() {
				result := own(ExtensionPolicyRule{AllowList: []string{"upload"}}, "upload")
				So(result.Decision, ShouldEqual, aictx.Allow)
			})
			Convey("an own deny beats a group allow", func() {
				result := CheckExtensionPolicy(ctx, ExtensionCheck{
					PolicyType: "oss",
					GroupIDs:   []string{"ext:oss:readonly"},
					Own:        ExtensionPolicyRule{DenyList: []string{"read"}},
					Action:     "read",
				})
				So(result.Decision, ShouldEqual, aictx.Deny)
			})
			Convey("a group deny beats an own allow", func() {
				result := CheckExtensionPolicy(ctx, ExtensionCheck{
					PolicyType: "oss",
					GroupIDs:   []string{"ext:oss:readonly"},
					Own:        ExtensionPolicyRule{AllowList: []string{"delete"}},
					Action:     "delete",
				})
				So(result.Decision, ShouldEqual, aictx.Deny)
			})
		})

		// 一条挂在同一资产上的 command 权限组说的是命令模式，不是动作名。
		Convey("A policy group of another type is not read as actions", func() {
			policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
				BuiltinID:  "ext:other:wide",
				Name:       "another face",
				PolicyType: "other",
				Policy:     `{"allow_list":["read"]}`,
			})
			Reset(func() { policy_group_entity.UnregisterExtensionGroups("other") })

			result := check([]string{"ext:other:wide"}, "read")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
