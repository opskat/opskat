package permission

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// withStubGrant 注册 stubGrantRepo 与一个只认 assetID=1 的 mock asset repo，
// 返回带 sessionID 的 ctx。两件事都是必须的：grant 匹配依赖 aictx.GetSessionID
// （不注入会直接返回空串，测试假绿），而按资产的匹配链会解析资产拿组链
// （asset_repo 未注册时是空指针崩溃，不是失败）。
func withStubGrant(t *testing.T) context.Context {
	stub := newStubGrantRepo()
	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(stub)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().Find(gomock.Any(), int64(1)).
		Return(&asset_entity.Asset{ID: 1, Name: "web-01", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)

	t.Cleanup(func() {
		if origGrant != nil {
			grant_repo.RegisterGrant(origGrant)
		}
		if origAsset != nil {
			asset_repo.RegisterAsset(origAsset)
		}
	})
	return aictx.WithSessionID(context.Background(), "sess-cp")
}

func TestGrantIsolation(t *testing.T) {
	Convey("grant 池按工具面隔离", t, func() {
		// 用户在 cp 审批里点"本次会话允许"并把 pattern 编辑成 `*`（很常见）时，
		// 这条路径授权一旦被命令面看见，MatchCommandRule 的 `*` 会放行任意命令。
		Convey("cp 的路径授权不能放行命令", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "*")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"rm -rf /var/log"}, "exec", policy.MatchCommandRule)
			So(got, ShouldEqual, "")
		})

		Convey("命令授权不能放行文件传输", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", "exec", "*")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"/etc/cron.d/backup"}, GrantToolCp, policy.MatchPathRule)
			So(got, ShouldEqual, "")
		})

		Convey("存量 tool_name=exec 的行仍然为非 cp 检查生效", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", "exec", "redis-cli get *")

			got := matchGrantPatternsWith(ctx, 1, nil, []string{"redis-cli get foo"}, "redis", policy.MatchCommandRule)
			So(got, ShouldEqual, "redis-cli get *")
		})
	})
}
