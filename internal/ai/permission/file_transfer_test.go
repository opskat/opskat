package permission

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"

	. "github.com/smartystreets/goconvey/convey"
)

func TestCheckFileTransferPermission(t *testing.T) {
	Convey("cp 类型的权限检查", t, func() {
		Convey("没有 grant 时需要确认", func() {
			ctx := withStubGrant(t)
			r := CheckPermission(ctx, GrantToolCp, 1, "/etc/cron.d/backup")
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("命中 cp grant 的路径 pattern 时放行", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "/opt/app/*")

			r := CheckPermission(ctx, GrantToolCp, 1, "/opt/app/deploy.sh")
			So(r.Decision, ShouldEqual, aictx.Allow)
			So(r.MatchedPattern, ShouldEqual, "/opt/app/*")
		})

		Convey("cp grant 的 * 不跨目录", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "/opt/app/*")

			r := CheckPermission(ctx, GrantToolCp, 1, "/opt/app/sub/deploy.sh")
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("路径为空时需要确认，不能被 * 整串放行", func() {
			ctx := withStubGrant(t)
			SaveGrantPattern(ctx, "sess-cp", 1, "web-01", GrantToolCp, "*")

			r := CheckPermission(ctx, GrantToolCp, 1, "")
			So(r.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})
}
