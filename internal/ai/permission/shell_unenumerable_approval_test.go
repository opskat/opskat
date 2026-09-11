package permission

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"
)

// 拆不出子命令的 shell 命令没有任何可匹配的 pattern：落成规则或 grant 后，下一次检查
// 仍会先拆不出子命令，永远匹配不上。所以归一化必须交出空列表（而不是原文），审批只能
// 本次允许，不能给出"始终允许"。
func TestNormalizeGrantPatterns_ShellUnenumerable(t *testing.T) {
	Convey("shell 归一化与 CheckPermission 按同一整串拆分", t, func() {
		Convey("解析失败 / 没有子命令 → 空", func() {
			So(NormalizeGrantPatterns("exec", unparseableShell, GrantOriginSystem), ShouldBeEmpty)
			So(NormalizeGrantPatterns("exec", "x=1", GrantOriginSystem), ShouldBeEmpty)
			So(NormalizeGrantPatterns("k8s", `kubectl get "`, GrantOriginUser), ShouldBeEmpty)
		})

		Convey("后面某行解析失败，整条命令都拆不出——不留下前面行的 pattern", func() {
			So(NormalizeGrantPatterns("exec", "reboot\n"+unparseableShell, GrantOriginSystem), ShouldBeEmpty)
		})

		Convey("跨行的语法结构按整体拆，不逐行切出语法碎片", func() {
			patterns := NormalizeGrantPatterns("exec", "for i in 1 2; do\n  echo $i\ndone", GrantOriginSystem)
			So(patterns, ShouldResemble, []string{"echo $i"})
		})
	})
}

func TestHandleConfirm_ShellUnenumerableIsOnce(t *testing.T) {
	Convey("AI 审批：拆不出子命令的命令只给一次性审批", t, func() {
		ctx, mockAsset, _ := setupPolicyTest(t)
		asset := &asset_entity.Asset{ID: 1, Name: "web", Type: asset_entity.AssetTypeSSH}
		mockAsset.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()

		var kinds []string
		checker := NewCommandPolicyChecker(func(_ context.Context, kind string, _ []ApprovalItem) ApprovalResponse {
			kinds = append(kinds, kind)
			return ApprovalResponse{Decision: "allow"}
		})

		result := checker.CheckForAsset(ctx, 1, asset_entity.AssetTypeSSH, unparseableShell)
		So(result.Decision, ShouldEqual, aictx.Allow)
		So(result.DecisionSource, ShouldEqual, aictx.SourceUserAllow)

		checker.CheckForAsset(ctx, 1, asset_entity.AssetTypeSSH, "uptime")
		So(kinds, ShouldResemble, []string{ApprovalKindOnce, ApprovalKindSingle})
	})
}
