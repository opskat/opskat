package policy

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
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

// TestCheckOSSPolicy 锁住 spec §4.1 步骤 3 的判定顺序：先 deny（任一策略串命中即拒），
// 再 allow（allow 非空时必须全部命中才放行）。一条 OSS 命令派生 1~3 条策略串（§3.3），
// 所以判定的输入是切片而不是单串。
func TestCheckOSSPolicy(t *testing.T) {
	ctx := context.Background()

	Convey("默认策略下三种结果各自成立(spec §4.4)", t, func() {
		Convey("object.read → allow（内置只读组）", func() {
			res := CheckOSSPolicy(ctx, nil, []string{"object.read mybucket/app.log"})
			So(res.Decision, ShouldEqual, aictx.Allow)
			So(res.MatchedPattern, ShouldEqual, "object.read *")
		})

		Convey("object.delete → needconfirm（既不在 allow 名单也不在地板）", func() {
			res := CheckOSSPolicy(ctx, nil, []string{"object.delete mybucket/app.log"})
			So(res.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("object.presign.write → deny（默认地板）", func() {
			res := CheckOSSPolicy(ctx, nil, []string{"object.presign.write mybucket/app.log"})
			So(res.Decision, ShouldEqual, aictx.Deny)
			So(res.MatchedPattern, ShouldEqual, "object.presign.write *")
		})

		Convey("presign GET 留在审批，没有被 PUT 那条地板一起带走", func() {
			res := CheckOSSPolicy(ctx, nil, []string{"object.presign.read mybucket/app.log"})
			So(res.Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})

	Convey("多策略串的判定(spec §3.3：copy 两条、move 三条)", t, func() {
		Convey("deny 命中任意一条即整条拒绝，并报出被拒的那一条", func() {
			pol := &asset_entity.OSSPolicy{
				AllowList: []string{"object.* *"},
				DenyList:  []string{"object.write locked/"},
			}
			res := CheckOSSPolicy(ctx, pol, []string{"object.read src/a.txt", "object.write locked/a.txt"})
			So(res.Decision, ShouldEqual, aictx.Deny)
			So(res.MatchedPattern, ShouldEqual, "object.write locked/")
			So(res.Message, ShouldContainSubstring, "object.write locked/a.txt")
		})

		Convey("allow 非空时必须每条都命中", func() {
			pol := &asset_entity.OSSPolicy{AllowList: []string{"object.read mybucket/", "object.write mybucket/"}}
			all := CheckOSSPolicy(ctx, pol, []string{"object.read mybucket/a", "object.write mybucket/b"})
			So(all.Decision, ShouldEqual, aictx.Allow)
			// 目的地换个桶：只有 read 命中，整条命令回落到审批
			part := CheckOSSPolicy(ctx, pol, []string{"object.read mybucket/a", "object.write other/b"})
			So(part.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		// 上面两例失败的都是**最后**一条策略串，只看目的地的实现照样能通过。
		// 这一例把失败点放在**源**上，锁住 spec §3.3 点名的核心安全点：
		// 只检查目的地就等于放行"把受限对象复制到可读位置再读"。
		Convey("受限的源不能被可写的目的地带过去(copy 的目的地旁路)", func() {
			denySrc := &asset_entity.OSSPolicy{
				AllowList: []string{"object.* *"},
				DenyList:  []string{"object.read locked/"},
			}
			res := CheckOSSPolicy(ctx, denySrc, []string{"object.read locked/a.txt", "object.write dst/a.txt"})
			So(res.Decision, ShouldEqual, aictx.Deny)
			So(res.MatchedPattern, ShouldEqual, "object.read locked/")
			So(res.Message, ShouldContainSubstring, "object.read locked/a.txt")

			// 源不在 allow 名单里，只有目的地命中 → 整条回落到审批
			writeOnly := &asset_entity.OSSPolicy{AllowList: []string{"object.write dst/"}}
			part := CheckOSSPolicy(ctx, writeOnly, []string{"object.read locked/a.txt", "object.write dst/a.txt"})
			So(part.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("空策略串不得凭空放行(与 CheckGroupGenericPolicy 同姿态)", func() {
			So(CheckOSSPolicy(ctx, nil, nil).Decision, ShouldEqual, aictx.NeedConfirm)
		})
	})

	Convey("显式策略不会继承未选择的默认组", t, func() {
		allowPresign := &asset_entity.OSSPolicy{AllowList: []string{"object.presign.write *"}}
		res := CheckOSSPolicy(ctx, allowPresign, []string{"object.presign.write mybucket/a"})
		So(res.Decision, ShouldEqual, aictx.Allow)

		writeOnly := &asset_entity.OSSPolicy{AllowList: []string{"object.write mybucket/"}}
		So(CheckOSSPolicy(ctx, writeOnly, []string{"object.read mybucket/a"}).Decision, ShouldEqual, aictx.NeedConfirm)
	})
}
