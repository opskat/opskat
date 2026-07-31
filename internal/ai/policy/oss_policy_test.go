package policy

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// TestMatchOSSRule walks the rule-semantics table from spec §3.4 row by row.
func TestMatchOSSRule(t *testing.T) {
	Convey("MatchOSSRule", t, func() {
		Convey("* 匹配任何形状合法的策略串", func() {
			So(MatchOSSRule("*", "object.read mybucket/logs/app.log"), ShouldBeTrue)
			So(MatchOSSRule("*", "bucket.list *"), ShouldBeTrue)
			So(MatchOSSRule("*", "object.presign.write mybucket/key"), ShouldBeTrue)
		})

		Convey("* 不匹配形状不合法的命令", func() {
			So(MatchOSSRule("*", ""), ShouldBeFalse)
			So(MatchOSSRule("*", "object.read"), ShouldBeFalse)
		})

		Convey("bare 桶名 = 整桶（任意深度）", func() {
			So(MatchOSSRule("object.read mybucket", "object.read mybucket/logs/2024/jan.log"), ShouldBeTrue)
			So(MatchOSSRule("object.read mybucket", "object.read mybucket/app.log"), ShouldBeTrue)
			So(MatchOSSRule("object.read mybucket", "object.write mybucket/app.log"), ShouldBeFalse)   // action 不匹配
			So(MatchOSSRule("object.read mybucket", "object.read otherbucket/app.log"), ShouldBeFalse) // bucket 不匹配
		})

		Convey("bare 桶名与尾随 / 等价", func() {
			So(MatchOSSRule("object.read mybucket/", "object.read mybucket/logs/2024/jan.log"), ShouldBeTrue)
			So(MatchOSSRule("object.read mybucket/", "object.read otherbucket/x"), ShouldBeFalse)
		})

		Convey("尾随 / 的前缀 = 该前缀下任意深度", func() {
			So(MatchOSSRule("object.read mybucket/logs/", "object.read mybucket/logs/app.log"), ShouldBeTrue)
			So(MatchOSSRule("object.read mybucket/logs/", "object.read mybucket/logs/2024/jan.log"), ShouldBeTrue)
			So(MatchOSSRule("object.read mybucket/logs/", "object.read mybucket/other/app.log"), ShouldBeFalse) // 不同前缀
			// "logs" 前缀不能越界匹配到同名开头的兄弟前缀 "logsx/"
			So(MatchOSSRule("object.read mybucket/logs/", "object.read mybucket/logsx/app.log"), ShouldBeFalse)
		})

		Convey("非尾随 / 的 pattern：* 不跨 /，只匹配这一层", func() {
			So(MatchOSSRule("object.read mybucket/logs/*.gz", "object.read mybucket/logs/app.gz"), ShouldBeTrue)
			So(MatchOSSRule("object.read mybucket/logs/*.gz", "object.read mybucket/logs/2024/app.gz"), ShouldBeFalse) // * 不能跨越 /
			So(MatchOSSRule("object.read mybucket/logs/*.gz", "object.read mybucket/logs/app.txt"), ShouldBeFalse)
		})

		Convey("object.* 匹配任意 object 子动作，尾随 / 覆盖整桶", func() {
			So(MatchOSSRule("object.* mybucket/", "object.read mybucket/a"), ShouldBeTrue)
			So(MatchOSSRule("object.* mybucket/", "object.write mybucket/b"), ShouldBeTrue)
			So(MatchOSSRule("object.* mybucket/", "object.presign.read mybucket/c"), ShouldBeTrue) // action 内无 /，* 能跨 presign.read 的点号
			So(MatchOSSRule("object.* mybucket/", "bucket.list *"), ShouldBeFalse)                 // action 命名空间不同
		})

		Convey("object.presign.* 通配桶 = 任意桶上的任意预签名", func() {
			So(MatchOSSRule("object.presign.* *", "object.presign.read anybucket/key"), ShouldBeTrue)
			So(MatchOSSRule("object.presign.* *", "object.presign.write otherbucket/key2"), ShouldBeTrue)
			So(MatchOSSRule("object.presign.* *", "object.read anybucket/key"), ShouldBeFalse) // action 前缀不同
		})

		Convey("bucket.list 的资源恒为字面 *", func() {
			So(MatchOSSRule("bucket.list *", "bucket.list *"), ShouldBeTrue)
			// 一条按桶名收紧的规则打不中 bucket.list 命令恒为 "*" 的资源半区，
			// 这不是 bug：ListBuckets 天然是全局操作，无法按单桶限定。
			So(MatchOSSRule("bucket.list mybucket", "bucket.list *"), ShouldBeFalse)
		})

		Convey("D5 动机场景：bare 桶名 deny 规则不会 fail-open 全失配", func() {
			So(MatchOSSRule("object.delete mybucket", "object.delete mybucket/secret.txt"), ShouldBeTrue)
		})

		Convey("含空白的 key（决策 D4）：按第一段空白切分，key 内部空白保留", func() {
			So(MatchOSSRule("object.read mybucket", `object.read mybucket/My Report.pdf`), ShouldBeTrue)
			So(MatchOSSRule(`object.read mybucket/My Report.pdf`, `object.read mybucket/My Report.pdf`), ShouldBeTrue)
			So(MatchOSSRule(`object.read mybucket/My Report.pdf`, `object.read mybucket/Other Report.pdf`), ShouldBeFalse)
		})

		Convey("边界：空规则 / 空命令 / 单 token 一律不匹配", func() {
			So(MatchOSSRule("", "object.read mybucket/key"), ShouldBeFalse)
			So(MatchOSSRule("object.read mybucket/key", ""), ShouldBeFalse)
			So(MatchOSSRule("object.read", "object.read mybucket/key"), ShouldBeFalse) // 规则缺 resource 半区
			So(MatchOSSRule("object.read mybucket/key", "object.read"), ShouldBeFalse) // 命令缺 resource 半区
		})

		Convey("非法 glob pattern 不 panic、不放行", func() {
			So(func() { MatchOSSRule("object.read mybucket/[unterminated", "object.read mybucket/x") }, ShouldNotPanic)
			So(MatchOSSRule("object.read mybucket/[unterminated", "object.read mybucket/x"), ShouldBeFalse)
			So(MatchOSSRule("object.[read mybucket", "object.read mybucket/x"), ShouldBeFalse) // action 半区非法 pattern
		})

		Convey("大小写：action 不敏感，bucket/key 敏感", func() {
			So(MatchOSSRule("OBJECT.READ mybucket", "object.read mybucket/x"), ShouldBeTrue)
			So(MatchOSSRule("object.read MyBucket", "object.read mybucket/x"), ShouldBeFalse)
		})
	})
}
