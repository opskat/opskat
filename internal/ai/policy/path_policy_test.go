package policy

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestMatchPathRule(t *testing.T) {
	Convey("路径 pattern 匹配", t, func() {
		Convey("同目录下的通配", func() {
			So(MatchPathRule("/opt/app/*", "/opt/app/deploy.sh"), ShouldBeTrue)
			So(MatchPathRule("/opt/app/*", "/opt/app/"), ShouldBeTrue)
		})

		Convey("* 不跨 /", func() {
			So(MatchPathRule("/opt/app/*", "/opt/app/sub/deploy.sh"), ShouldBeFalse)
		})

		Convey("精确路径", func() {
			So(MatchPathRule("/etc/passwd", "/etc/passwd"), ShouldBeTrue)
			So(MatchPathRule("/etc/passwd", "/etc/passwd.bak"), ShouldBeFalse)
		})

		Convey("单独 * 放行任意非空路径", func() {
			So(MatchPathRule("*", "/etc/cron.d/backup"), ShouldBeTrue)
			So(MatchPathRule("*", ""), ShouldBeFalse)
		})

		Convey("非法 pattern 不放行", func() {
			So(MatchPathRule("/opt/[", "/opt/x"), ShouldBeFalse)
		})

		Convey("空 pattern 不放行", func() {
			So(MatchPathRule("", "/etc/passwd"), ShouldBeFalse)
		})

		Convey("尾随斜杠表示整棵目录树，但不覆盖相邻目录", func() {
			So(MatchPathRule("/opt/app/", "/opt/app/deploy.sh"), ShouldBeTrue)
			So(MatchPathRule("/opt/app/", "/opt/app/releases/v2/bin"), ShouldBeTrue)
			So(MatchPathRule("/opt/app/", "/opt/application/secret"), ShouldBeFalse)
			So(MatchPathRule("/opt/app/", "/opt/app"), ShouldBeFalse)
		})
	})
}
