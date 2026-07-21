package policy

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/etcd_svc"
)

// extractForTest 走与 MatchRedisRule 相同的单次切词管线（splitRulePair →
// parseRedisCommand），只是把同一个串同时当规则和命令喂进去。
func extractForTest(s string) redisCommand {
	tokens, _ := splitRulePair(s, s)
	return parseRedisCommand(tokens)
}

func TestParseRedisCommand(t *testing.T) {
	Convey("parseRedisCommand", t, func() {
		Convey("简单命令 GET", func() {
			c := extractForTest("GET mykey")
			So(c.Name, ShouldEqual, "GET")
			So(c.Args, ShouldResemble, []string{"mykey"})
		})

		Convey("多词命令 CONFIG SET", func() {
			c := extractForTest("CONFIG SET maxmemory 128mb")
			So(c.Name, ShouldEqual, "CONFIG SET")
			So(c.Args, ShouldResemble, []string{"maxmemory", "128mb"})
		})

		Convey("空字符串", func() {
			c := extractForTest("")
			So(c.Name, ShouldBeEmpty)
			So(c.Args, ShouldBeEmpty)
		})

		Convey("单命令无参数 PING", func() {
			c := extractForTest("PING")
			So(c.Name, ShouldEqual, "PING")
			So(c.Args, ShouldBeEmpty)
		})

		Convey("非多词命令带参数 DEL", func() {
			c := extractForTest("DEL key1 key2")
			So(c.Name, ShouldEqual, "DEL")
			So(c.Args, ShouldResemble, []string{"key1", "key2"})
		})

		Convey("多词命令 XGROUP CREATE", func() {
			c := extractForTest("XGROUP CREATE mystream grpname $")
			So(c.Name, ShouldEqual, "XGROUP CREATE")
			So(c.Args, ShouldResemble, []string{"mystream", "grpname", "$"})
		})

		Convey("引号内的空格是参数的一部分，不再被二次切开", func() {
			c := extractForTest("DEL '/prod/my key'")
			So(c.Name, ShouldEqual, "DEL")
			So(c.Args, ShouldResemble, []string{"/prod/my key"})
		})

		Convey("引号让命令名 token 自带空格时，参数下标不受影响", func() {
			c := extractForTest("'CONFIG SET' maxmemory")
			So(c.Name, ShouldEqual, "CONFIG SET")
			So(c.Args, ShouldResemble, []string{"maxmemory"})
		})
	})
}

func TestMatchRedisRule(t *testing.T) {
	Convey("MatchRedisRule", t, func() {
		Convey("精确匹配", func() {
			So(MatchRedisRule("GET mykey", "GET mykey"), ShouldBeTrue)
		})

		Convey("规则无参数匹配任意参数", func() {
			So(MatchRedisRule("GET", "GET mykey"), ShouldBeTrue)
			So(MatchRedisRule("GET", "GET"), ShouldBeTrue)
		})

		Convey("通配符 * 匹配任意参数", func() {
			So(MatchRedisRule("GET *", "GET mykey"), ShouldBeTrue)
			So(MatchRedisRule("GET *", "GET"), ShouldBeTrue)
		})

		Convey("单独 * 匹配任意 Redis 命令", func() {
			So(MatchRedisRule("*", "INFO"), ShouldBeTrue)
			So(MatchRedisRule("*", "SET a b"), ShouldBeTrue)
		})

		Convey("多词命令的子命令通配符保持语义", func() {
			So(MatchRedisRule("DEBUG *", "DEBUG STATS"), ShouldBeTrue)
			So(MatchRedisRule("DEBUG *", "CONFIG SET maxmemory 128mb"), ShouldBeFalse)
		})

		Convey("key pattern glob 匹配", func() {
			So(MatchRedisRule("DEL user:*", "DEL user:123"), ShouldBeTrue)
			So(MatchRedisRule("DEL user:*", "DEL order:123"), ShouldBeFalse)
			So(MatchRedisRule("DEL cache/*", "DEL cache/a/b"), ShouldBeFalse)
		})

		Convey("key pattern glob 匹配 - 引号切词后的 key（空格/非 ASCII）不再被误拆", func() {
			// 回归用例：FormatCommand 对 etcd key/value 加了 cmdline.QuoteIfNeeded 引号后，
			// 旧版 strings.Fields 会把引号字符原样粘在第一个 token 上，导致 path.Match 必然
			// 失配——一条本该命中的 deny 规则因此静默失效（fail-open）。
			So(MatchRedisRule("DEL /prod/*", "DEL '/prod/my key'"), ShouldBeTrue)
			So(MatchRedisRule("DEL /prod/*", "DEL '/prod/配置'"), ShouldBeTrue)
			So(MatchRedisRule("GET /prod/*", "GET '/prod/配置'"), ShouldBeTrue)
			// 未加引号但含非 ASCII 字符的 key（这是一个中文产品，绝大多数 key 都会命中这条）
			// 同样要能正常匹配。
			So(MatchRedisRule("DEL /prod/*", "DEL /prod/配置"), ShouldBeTrue)
		})

		Convey("规则或命令不是合法 shell 语法时，切词退回按空白分词，不静默失配", func() {
			// 未闭合引号：cmdline.Words 报错，切词必须退回 strings.Fields
			// 而不是把它当"零 token"处理——否则一条切不了词的 deny 规则会从"总能命中"
			// 悄悄变成"永远不命中"。
			So(MatchRedisRule("del 'unterminated", "del 'unterminated"), ShouldBeTrue)
		})

		Convey("只有一侧切词失败时，两侧一起退回分词——不能在两套词法里比较", func() {
			// 规则是用户手写文本，可能带未闭合引号（切词失败）；命令侧却是
			// FormatCommand 产出的合法带引号串（切词成功）。若只退回失败的那一侧，
			// 规则里保留着引号字符、命令里已被剥掉，path.Match 必然失配——一条本该
			// 命中的 deny 规则静默失效（fail-open）。
			So(MatchRedisRule("del '/prod/*", "del '/prod/x'"), ShouldBeTrue)
			So(MatchRedisRule(`DEL "/prod/*`, `DEL "/prod/x"`), ShouldBeTrue)
			So(MatchRedisRule("DEL 'user:*", "DEL 'user:1'"), ShouldBeTrue)
		})

		Convey("命令名 token 含空格（引号切词的结果）不再让下标错位", func() {
			// 回归：命令名曾按 strings.Fields(joined) 数词数、再回原 token 切片取下标，
			// 一个 'CONFIG SET' 这样的带空格 token 会让词数多于 token 数，直接
			// index out of range —— 全链路无 recover()，等于整个应用崩溃。
			// 引号包起来的命令名规范化后与两个词的写法同名，于是仍然命中：deny 规则
			// 保持有效（fail-closed），而不是被一对引号绕过。
			So(MatchRedisRule("CONFIG SET maxmemory", "'CONFIG SET' maxmemory"), ShouldBeTrue)
			So(MatchRedisRule("'GET KEY' a", "'GET KEY' a"), ShouldBeTrue)
			So(MatchRedisRule("CONFIG 'SET X' foo", "CONFIG 'SET X' foo"), ShouldBeTrue)
		})

		Convey("显式空参数是一个真实参数，不等于「无参数」", func() {
			// `get ''` 的参数是一个空串 key，只应匹配同样是空 key 的命令；曾经因为
			// 参数被 Join 成字符串后空参数塌缩成 ""（与"没有参数"无法区分）而匹配一切，
			// 在 allow list 里就是一条过度放行的规则。
			So(MatchRedisRule("get ''", "get /prod/secret"), ShouldBeFalse)
			So(MatchRedisRule("get ''", "get ''"), ShouldBeTrue)
			// etcd 的"列出整个 key 空间"就渲染成空 key，通配规则仍要能覆盖它。
			So(MatchRedisRule("get *", "get '' --prefix"), ShouldBeTrue)
		})

		Convey("不同命令不匹配", func() {
			So(MatchRedisRule("GET mykey", "SET mykey"), ShouldBeFalse)
		})

		Convey("规则有参数但命令无参数", func() {
			So(MatchRedisRule("GET mykey", "GET"), ShouldBeFalse)
		})

		Convey("多词命令大小写不敏感", func() {
			So(MatchRedisRule("config set", "CONFIG SET maxmemory 128mb"), ShouldBeTrue)
		})
	})
}

func TestCheckRedisPolicy(t *testing.T) {
	ctx := context.Background()

	Convey("CheckRedisPolicy", t, func() {
		Convey("拒绝列表命中 → aictx.Deny，DecisionSource=aictx.SourcePolicyDeny", func() {
			p := &asset_entity.RedisPolicy{
				DenyList: []string{"FLUSHALL"},
			}
			result := CheckRedisPolicy(ctx, p, "FLUSHALL")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		})

		Convey("允许列表命中 → aictx.Allow，DecisionSource=aictx.SourcePolicyAllow", func() {
			p := &asset_entity.RedisPolicy{
				AllowList: []string{"GET", "SET"},
			}
			result := CheckRedisPolicy(ctx, p, "GET mykey")
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
		})

		Convey("有允许列表但未命中 → aictx.NeedConfirm", func() {
			p := &asset_entity.RedisPolicy{
				AllowList: []string{"GET"},
			}
			result := CheckRedisPolicy(ctx, p, "DEL mykey")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("空策略使用默认只读 allow", func() {
			p := &asset_entity.RedisPolicy{}
			result := CheckRedisPolicy(ctx, p, "INFO")
			So(result.Decision, ShouldEqual, aictx.Allow)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
		})

		Convey("空策略下写命令需要确认", func() {
			p := &asset_entity.RedisPolicy{}
			result := CheckRedisPolicy(ctx, p, "SET mykey value")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)
		})

		Convey("空策略叠加默认 dangerous deny", func() {
			p := &asset_entity.RedisPolicy{}
			result := CheckRedisPolicy(ctx, p, "DEBUG STATS")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
			So(result.MatchedPattern, ShouldEqual, "DEBUG *")
		})

		Convey("拒绝列表优先于允许列表", func() {
			p := &asset_entity.RedisPolicy{
				AllowList: []string{"FLUSHALL"},
				DenyList:  []string{"FLUSHALL"},
			}
			result := CheckRedisPolicy(ctx, p, "FLUSHALL")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		})

		Convey("nil policy 使用默认策略", func() {
			result := CheckRedisPolicy(ctx, nil, "GET mykey")
			So(result.Decision, ShouldEqual, aictx.Allow)

			result = CheckRedisPolicy(ctx, nil, "SET mykey value")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)

			result = CheckRedisPolicy(ctx, nil, "DEBUG STATS")
			So(result.Decision, ShouldEqual, aictx.Deny)
		})

		Convey("allow_list wildcard allows any non-dangerous Redis command", func() {
			p := &asset_entity.RedisPolicy{AllowList: []string{"*"}}
			result := CheckRedisPolicy(ctx, p, "SET mykey value")
			So(result.Decision, ShouldEqual, aictx.Allow)

			result = CheckRedisPolicy(ctx, p, "DEBUG STATS")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		})

		Convey("deny_list wildcard denies every Redis command", func() {
			p := &asset_entity.RedisPolicy{DenyList: []string{"*"}}
			result := CheckRedisPolicy(ctx, p, "INFO")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
			So(result.MatchedPattern, ShouldEqual, "*")
		})

		Convey("explicit allow list replaces default read-only allow", func() {
			p := &asset_entity.RedisPolicy{AllowList: []string{"GET *"}}
			result := CheckRedisPolicy(ctx, p, "INFO")
			So(result.Decision, ShouldEqual, aictx.NeedConfirm)

			result = CheckRedisPolicy(ctx, p, "DEBUG STATS")
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		})
	})
}

func TestCheckEtcdPolicy_UserDenyRuleMatchesQuotedNonASCIIKey(t *testing.T) {
	ctx := context.Background()

	Convey("CheckEtcdPolicy 拒绝清单命中带引号/非 ASCII 字符的 key", t, func() {
		// 走真实生产路径：etcd_svc.FormatCommand 把 ExecRequest 渲染成规范命令串
		// （对 key/value 加 cmdline.QuoteIfNeeded 引号），再交给 CheckEtcdPolicy 匹配
		// 用户自定义的 deny 规则。这是 CRITICAL-1 报告的确切场景：引号一旦破坏
		// MatchRedisRule 的切词，deny 规则就会静默失效（fail-open）。
		Convey("非 ASCII key 触发引号", func() {
			p := &asset_entity.EtcdPolicy{DenyList: []string{"del /prod/*"}}
			cmd := etcd_svc.FormatCommand(&etcd_svc.ExecRequest{Op: "del", Key: "/prod/配置"})
			So(cmd, ShouldEqual, "del '/prod/配置'")
			result := CheckEtcdPolicy(ctx, p, cmd)
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
			So(result.MatchedPattern, ShouldEqual, "del /prod/*")
		})

		Convey("含空格的 key 触发引号", func() {
			p := &asset_entity.EtcdPolicy{DenyList: []string{"del /prod/*"}}
			cmd := etcd_svc.FormatCommand(&etcd_svc.ExecRequest{Op: "del", Key: "/prod/my key"})
			So(cmd, ShouldEqual, "del '/prod/my key'")
			result := CheckEtcdPolicy(ctx, p, cmd)
			So(result.Decision, ShouldEqual, aictx.Deny)
			So(result.DecisionSource, ShouldEqual, aictx.SourcePolicyDeny)
		})
	})
}
