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
		So(policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
			BuiltinID:  "ext:oss:readonly",
			Name:       "OSS Read-Only",
			PolicyType: "oss",
			Policy:     `{"allow_list":["list","read"],"deny_list":["delete","admin"]}`,
		}), ShouldBeNil)
		So(policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
			BuiltinID:  "ext:oss:dangerous-deny",
			Name:       "OSS Dangerous aictx.Deny",
			PolicyType: "oss",
			Policy:     `{"deny_list":["delete","admin"]}`,
		}), ShouldBeNil)

		Reset(func() {
			policy_group_entity.UnregisterExtensionGroups("oss")
		})

		check := func(groups []string, action string) aictx.CheckResult {
			return CheckExtensionPolicy(ctx, ExtensionCheck{PolicyType: "oss", GroupIDs: groups, Action: action}).CheckResult
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
				return CheckExtensionPolicy(ctx, ExtensionCheck{PolicyType: "oss", Own: rule, Action: action}).CheckResult
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
			So(policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
				BuiltinID:  "ext:other:wide",
				Name:       "another face",
				PolicyType: "other",
				Policy:     `{"allow_list":["read"]}`,
			}), ShouldBeNil)
			Reset(func() { policy_group_entity.UnregisterExtensionGroups("other") })

			result := check([]string{"ext:other:wide"}, "read")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}

// 规则形如 <action> 或 <action>:<resource-glob>（holder 自身那一列的前缀已由调用方还原）。
func TestCheckExtensionPolicyResourceGlobs(t *testing.T) {
	Convey("CheckExtensionPolicy matches a rule's resource glob against the call's resource", t, func() {
		ctx := context.Background()
		check := func(own ExtensionPolicyRule, action, resource string) aictx.CheckResult {
			return CheckExtensionPolicy(ctx, ExtensionCheck{PolicyType: "oss", Own: own, Action: action, Resources: []string{resource}}).CheckResult
		}

		Convey("a rule without a resource matches the action on any resource", func() {
			So(check(ExtensionPolicyRule{AllowList: []string{"read"}}, "read", "bucket/a").Decision, ShouldEqual, aictx.Allow)
			So(check(ExtensionPolicyRule{AllowList: []string{"read"}}, "read", "").Decision, ShouldEqual, aictx.Allow)
		})

		Convey("a glob uses the command rules' path.Match semantics", func() {
			own := ExtensionPolicyRule{AllowList: []string{"read:logs/*"}}
			So(check(own, "read", "logs/app.log").Decision, ShouldEqual, aictx.Allow)
			So(check(own, "read", "logs/2026/app.log").Decision, ShouldEqual, aictx.NeedConfirm)
			So(check(own, "read", "secrets/key").Decision, ShouldEqual, aictx.NeedConfirm)
			So(check(own, "write", "logs/app.log").Decision, ShouldEqual, aictx.NeedConfirm)
			So(check(own, "read", "").Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("a matching deny glob beats an allow, and reports the rule that decided", func() {
			own := ExtensionPolicyRule{AllowList: []string{"read"}, DenyList: []string{"read:secrets/*"}}
			got := check(own, "read", "secrets/key")
			So(got.Decision, ShouldEqual, aictx.Deny)
			So(got.MatchedPattern, ShouldEqual, "read:secrets/*")
			So(got.Message, ShouldContainSubstring, "secrets/key")
			So(check(own, "read", "logs/a").Decision, ShouldEqual, aictx.Allow)
		})

		Convey("the rule splits at the first ':' — everything after it is the glob", func() {
			So(check(ExtensionPolicyRule{AllowList: []string{"read:a"}}, "read", "a:b").Decision, ShouldEqual, aictx.NeedConfirm)
			So(check(ExtensionPolicyRule{AllowList: []string{"read:a:*"}}, "read", "a:b").Decision, ShouldEqual, aictx.Allow)
			So(check(ExtensionPolicyRule{AllowList: []string{"read:a"}}, "read:a", "").Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("policy group rules carry globs too", func() {
			So(policy_group_entity.RegisterExtensionGroup(&policy_group_entity.PolicyGroup{
				BuiltinID:  "ext:oss:logs",
				Name:       "logs",
				PolicyType: "oss",
				Policy:     `{"allow_list":["read:logs/*"],"deny_list":["read:logs/private*"]}`,
			}), ShouldBeNil)
			Reset(func() { policy_group_entity.UnregisterExtensionGroups("oss") })

			in := ExtensionCheck{PolicyType: "oss", GroupIDs: []string{"ext:oss:logs"}, Action: "read"}
			in.Resources = []string{"logs/app"}
			So(CheckExtensionPolicy(ctx, in).Decision, ShouldEqual, aictx.Allow)
			in.Resources = []string{"logs/private.key"}
			So(CheckExtensionPolicy(ctx, in).Decision, ShouldEqual, aictx.Deny)
		})
	})
}

// A call may touch several resources (resources are globs: the host quotes a
// literal resource, a PolicyResources resource keeps '*' / '?' as wildcards).
// Deny if any resource hits a deny; allow only if every resource is covered.
func TestCheckExtensionPolicyMultiResource(t *testing.T) {
	Convey("CheckExtensionPolicy judges each resource of the call", t, func() {
		ctx := context.Background()
		check := func(own ExtensionPolicyRule, action string, resources ...string) ExtensionPolicyResult {
			return CheckExtensionPolicy(ctx, ExtensionCheck{PolicyType: "es", Own: own, Action: action, Resources: resources})
		}

		Convey("one resource hitting a deny denies the call under a broad allow", func() {
			own := ExtensionPolicyRule{AllowList: []string{"delete"}, DenyList: []string{"delete:prod-*"}}
			got := check(own, "delete", "keep", "prod-1", "prod-2")
			So(got.Decision, ShouldEqual, aictx.Deny)
			So(got.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
			So(got.MatchedPattern, ShouldEqual, "delete:prod-* (prod-1, prod-2)")
			So(got.Message, ShouldContainSubstring, "prod-1, prod-2")
			So(got.Message, ShouldNotContainSubstring, "keep")

			So(check(own, "delete", "keep", "y").Decision, ShouldEqual, aictx.Allow)
		})

		Convey("each deny rule lists the resources it denied", func() {
			own := ExtensionPolicyRule{DenyList: []string{"delete:prod-*", "delete:secret"}}
			got := check(own, "delete", "secret", "prod-1", "ok")
			So(got.Decision, ShouldEqual, aictx.Deny)
			So(got.MatchedPattern, ShouldEqual, "delete:secret (secret); delete:prod-* (prod-1)")
		})

		Convey("an allow must cover every resource; different rules may cover different ones", func() {
			own := ExtensionPolicyRule{AllowList: []string{"write:logs-*", "write:a"}}
			got := check(own, "write", "logs-1", "a", "logs-2")
			So(got.Decision, ShouldEqual, aictx.Allow)
			So(got.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
			So(got.MatchedPattern, ShouldEqual, "write:logs-*; write:a")

			got = check(own, "write", "logs-a", "secret")
			So(got.Decision, ShouldEqual, aictx.NeedConfirm)
			So(got.Uncovered, ShouldResemble, []string{"secret"})
		})

		Convey("grants cover what the rules left uncovered and join the matched pattern", func() {
			own := ExtensionPolicyRule{AllowList: []string{"write:logs-*"}}
			got := check(own, "write", "logs-a", "secret").AllowedByGrants([]string{"ext:es:write:secret"})
			So(got.Decision, ShouldEqual, aictx.Allow)
			So(got.DecisionSource, ShouldEqual, aictx.SourceGrantAllow)
			So(got.MatchedPattern, ShouldEqual, "write:logs-*; ext:es:write:secret")
		})

		Convey("a wildcard resource hits a deny whose glob may match one of its names", func() {
			own := ExtensionPolicyRule{AllowList: []string{"delete"}, DenyList: []string{"delete:prod-*"}}
			for _, r := range []string{"*", "p*", "?rod-1", "prod-*", "*-1"} {
				So(check(own, "delete", r).Decision, ShouldEqual, aictx.Deny)
			}
			for _, r := range []string{"logs-*", "prod", "?", "x*y"} {
				So(check(own, "delete", r).Decision, ShouldEqual, aictx.Allow)
			}
			own.DenyList = []string{"delete:prod-[0-9]"}
			So(check(own, "delete", "prod-*").Decision, ShouldEqual, aictx.Deny)
		})

		Convey("an undecidable deny overlap counts as a hit", func() {
			own := ExtensionPolicyRule{AllowList: []string{"delete"}, DenyList: []string{"delete:[a-"}}
			So(check(own, "delete", "logs-*").Decision, ShouldEqual, aictx.Deny)
		})

		Convey("a wildcard resource is allowed only by a glob covering every name it stands for", func() {
			own := ExtensionPolicyRule{AllowList: []string{"read:logs-*"}}
			So(check(own, "read", "logs-2026-*").Decision, ShouldEqual, aictx.Allow)
			So(check(own, "read", "logs-?").Decision, ShouldEqual, aictx.Allow)
			So(check(own, "read", "logs*").Decision, ShouldEqual, aictx.NeedConfirm)
			So(check(own, "read", "*").Decision, ShouldEqual, aictx.NeedConfirm)
			So(check(ExtensionPolicyRule{AllowList: []string{"read"}}, "read", "*").Decision, ShouldEqual, aictx.Allow)
			So(check(ExtensionPolicyRule{AllowList: []string{"read:*"}}, "read", "logs-*").Decision, ShouldEqual, aictx.Allow)
			So(check(ExtensionPolicyRule{AllowList: []string{"read:logs-\\*"}}, "read", "logs-*").Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("an undecidable allow cover counts as not covered", func() {
			So(check(ExtensionPolicyRule{AllowList: []string{"read:[a-"}}, "read", "logs-*").Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("a quoted literal resource is judged exactly as the name it quotes", func() {
			So(check(ExtensionPolicyRule{AllowList: []string{"read:a*"}}, "read", `a\*b`).Decision, ShouldEqual, aictx.Allow)
			So(check(ExtensionPolicyRule{AllowList: []string{`read:a\*b`}}, "read", `a\*b`).Decision, ShouldEqual, aictx.Allow)
			So(check(ExtensionPolicyRule{AllowList: []string{`read:a\*b`}}, "read", `a*b`).Decision, ShouldEqual, aictx.NeedConfirm)
			got := check(ExtensionPolicyRule{DenyList: []string{"read:a?b"}}, "read", `a\*b`)
			So(got.Decision, ShouldEqual, aictx.Deny)
			So(got.MatchedPattern, ShouldEqual, "read:a?b")
			So(got.Message, ShouldEndWith, "on a*b")
		})

		Convey("no resource is judged as the empty resource", func() {
			So(check(ExtensionPolicyRule{AllowList: []string{"read:*"}}, "read").Decision, ShouldEqual, aictx.Allow)
			So(check(ExtensionPolicyRule{AllowList: []string{"read:logs-*"}}, "read").Decision, ShouldEqual, aictx.NeedConfirm)
			got := check(ExtensionPolicyRule{DenyList: []string{"read"}}, "read")
			So(got.Decision, ShouldEqual, aictx.Deny)
			So(got.MatchedPattern, ShouldEqual, "read")
			So(got.Message, ShouldEqual, "action denied by extension policy: read")
		})
	})
}
