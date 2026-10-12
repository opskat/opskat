package permission

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"

	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"
)

// 读不出来的策略不等于"没有策略"。把它当成空策略跳过，里面的规则会一起消失：allow `*`
// 没了，用户明明配了全放行却每条命令都要审批；deny 没了，组上一条 `*` 就能放行资产本该
// 拒绝的命令。两种都没有任何提示，界面还照常显示着那些规则。
func TestCheckPermission_UnreadablePolicy(t *testing.T) {
	// groups 里的数字是 v1.1.1 及更早版本写下的内置权限组 ID，现行结构只认字符串。
	const unreadable = `{"allow_list":["*"],"deny_list":["rm *"],"groups":[-1,-4]}`

	Convey("判定要读的策略列解析失败时拒绝执行，并指出是哪个 holder", t, func() {
		ctx, mockAsset, stubGroup := setupPolicyTest(t)
		stubGroup.groups[5] = &group_entity.Group{ID: 5, Name: "prod"}

		Convey("资产自己的策略解析失败", func() {
			asset := &asset_entity.Asset{ID: 31, Name: "k3s-worker-1", Type: asset_entity.AssetTypeSSH, CmdPolicy: unreadable}
			mockAsset.EXPECT().Find(gomock.Any(), int64(31)).Return(asset, nil).AnyTimes()

			result := CheckPermission(ctx, asset_entity.AssetTypeSSH, 31, "uptime")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
			So(result.Message, ShouldContainSubstring, "k3s-worker-1")
		})

		Convey("资产策略解析失败时，组上的 allow `*` 不能替它放行", func() {
			stubGroup.groups[5].CmdPolicy = mustJSON(asset_entity.CommandPolicy{AllowList: []string{"*"}})
			asset := &asset_entity.Asset{ID: 31, Name: "k3s-worker-1", Type: asset_entity.AssetTypeSSH, GroupID: 5, CmdPolicy: unreadable}
			mockAsset.EXPECT().Find(gomock.Any(), int64(31)).Return(asset, nil).AnyTimes()

			result := CheckPermission(ctx, asset_entity.AssetTypeSSH, 31, "rm -rf /data")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.Message, ShouldContainSubstring, "k3s-worker-1")
		})

		Convey("组通用策略解析失败时，类型专用策略不能绕过它放行", func() {
			stubGroup.groups[5].CmdPolicy = unreadable
			asset := &asset_entity.Asset{
				ID: 31, Name: "cache", Type: asset_entity.AssetTypeRedis, GroupID: 5,
				CmdPolicy: mustJSON(asset_entity.RedisPolicy{AllowList: []string{"GET *"}}),
			}
			mockAsset.EXPECT().Find(gomock.Any(), int64(31)).Return(asset, nil).AnyTimes()

			result := CheckPermission(ctx, asset_entity.AssetTypeRedis, 31, "GET user:1")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.Message, ShouldContainSubstring, "prod")
		})

		Convey("组上别的类型的策略列坏了，不影响本类型的判定", func() {
			stubGroup.groups[5].QryPolicy = unreadable
			asset := &asset_entity.Asset{
				ID: 31, Name: "k3s-worker-1", Type: asset_entity.AssetTypeSSH, GroupID: 5,
				CmdPolicy: mustJSON(asset_entity.CommandPolicy{AllowList: []string{"uptime"}}),
			}
			mockAsset.EXPECT().Find(gomock.Any(), int64(31)).Return(asset, nil).AnyTimes()

			result := CheckPermission(ctx, asset_entity.AssetTypeSSH, 31, "uptime")
			So(result.Decision, ShouldEqual, aictx.Allow)
		})
	})
}
