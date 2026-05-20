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

		Convey("aictx.Allow when action is in allow_list", func() {
			result := CheckExtensionPolicy(ctx, []string{"ext:oss:readonly"}, "read")
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
		})

		Convey("aictx.Deny when action is in deny_list", func() {
			result := CheckExtensionPolicy(ctx, []string{"ext:oss:readonly"}, "delete")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		})

		Convey("aictx.NeedConfirm when action not in any list", func() {
			result := CheckExtensionPolicy(ctx, []string{"ext:oss:readonly"}, "upload")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("Merging multiple groups: deny takes precedence", func() {
			// "ext:oss:readonly" has allow_list with "read", but also deny_list with "delete"
			// "ext:oss:dangerous-deny" has deny_list with "delete"
			// Even if one group allows "read", if another group denies it, deny wins.
			// Here test that "delete" is denied even across groups.
			result := CheckExtensionPolicy(ctx, []string{"ext:oss:readonly", "ext:oss:dangerous-deny"}, "delete")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)

			// "read" is only in allow_list, not in any deny_list → aictx.Allow
			result = CheckExtensionPolicy(ctx, []string{"ext:oss:readonly", "ext:oss:dangerous-deny"}, "read")
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
		})

		Convey("aictx.NeedConfirm when no groups configured", func() {
			result := CheckExtensionPolicy(ctx, nil, "read")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
