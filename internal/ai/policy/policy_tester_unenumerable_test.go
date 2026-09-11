package policy

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	. "github.com/smartystreets/goconvey/convey"
)

// 策略测试面板必须与真实执行路径（permission.checkCommandPolicyPermission /
// checkK8sPermission）对拆不出子命令的命令给出同一结论，否则面板说"需确认"而执行放行。
func TestPolicyTester_ShellUnenumerable(t *testing.T) {
	ctx := context.Background()

	Convey("testSSHPolicy：拆不出子命令", t, func() {
		Convey("allow * → Allow，命中 *", func() {
			out := testSSHPolicy(ctx, &asset_entity.CommandPolicy{AllowList: []string{"*"}}, nil, `echo "`)
			So(out.Decision, ShouldEqual, aictx.Allow)
			So(out.MatchedPattern, ShouldEqual, "*")
		})

		Convey("allow * + 内置危险 deny 组 → NeedConfirm，带解析失败原因", func() {
			out := testSSHPolicy(ctx, &asset_entity.CommandPolicy{
				AllowList: []string{"*"}, Groups: []string{policy.BuiltinDangerousDeny},
			}, nil, `echo "`)
			So(out.Decision, ShouldEqual, aictx.NeedConfirm)
			So(out.Message, ShouldContainSubstring, "reached EOF without closing quote")
		})

		Convey("组链 deny * + 资产 allow * → Deny", func() {
			groups := []*group_entity.Group{makeGroup("ops", `{"deny_list":["*"]}`)}
			out := testSSHPolicy(ctx, &asset_entity.CommandPolicy{AllowList: []string{"*"}}, groups, `echo "`)
			So(out.Decision, ShouldEqual, aictx.Deny)
			So(out.MatchedPattern, ShouldEqual, "*")
		})
	})

	Convey("testK8sPolicy：拆不出子命令", t, func() {
		Convey("K8s allow * → Allow，命中 *", func() {
			out := testK8sPolicy(ctx, &asset_entity.K8sPolicy{AllowList: []string{"*"}}, nil, `kubectl get "`)
			So(out.Decision, ShouldEqual, aictx.Allow)
			So(out.MatchedPattern, ShouldEqual, "*")
		})

		Convey("组通用 allow * + K8s 默认策略的危险 deny → NeedConfirm", func() {
			groups := []*group_entity.Group{makeGroup("k8s", `{"allow_list":["*"]}`)}
			out := testK8sPolicy(ctx, nil, groups, `kubectl get "`)
			So(out.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
