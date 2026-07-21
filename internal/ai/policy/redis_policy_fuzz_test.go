package policy

import "testing"

// fuzzRules 是喂给 FuzzMatchRedisRule 的规则种子：两个内置组的全部规则（Redis 的
// redis-readonly / redis-dangerous-deny，etcd 的 etcd-readonly / etcd-dangerous-deny），
// 加上历史上崩过或误判过的引号/空词形状。直接抄一份而不是从 policy_group_entity
// 读，是为了避免 policy → policy_group_entity 的测试期依赖；这份清单只在种子里用，
// 漂了也只是少几个起点，不影响 fuzz 的不变式。
var fuzzRules = []string{
	// builtin:redis-readonly
	"GET", "MGET", "STRLEN", "HGETALL", "TYPE", "TTL", "EXISTS", "KEYS", "SCAN", "INFO", "PING",
	// builtin:redis-dangerous-deny
	"FLUSHDB", "FLUSHALL", "CONFIG SET *", "CONFIG RESETSTAT", "DEBUG *", "SHUTDOWN *",
	"SLAVEOF *", "REPLICAOF *", "ACL DELUSER *", "ACL SETUSER *", "SCRIPT FLUSH", "CLUSTER RESET *",
	// builtin:etcd-readonly
	"get *", "endpoint *", "member list", "lease list",
	// builtin:etcd-dangerous-deny
	"auth enable", "user add *", "role grant-permission *", "member remove *",
	"move-leader *", "defrag", "compact *", "alarm disarm *", "snapshot save *",
	// 通配、空、引号与未闭合引号
	"*", "", "   ", "del /prod/*", "DEL 'user:*", `DEL "/prod/*`, "del '/prod/*",
	"'CONFIG SET' maxmemory", "CONFIG 'SET X' foo", "'GET KEY' a", "get ''", `put "" ""`,
	"get '' --prefix", "DEL user:[a-", "GET \\", "GET a b c", "|", "a && b", "$(x)", "a\x00b",
}

// fuzzCommands 是命令侧种子：既有模型/用户可能直接写出的 Redis 串，也有
// etcd_svc.FormatCommand 会产出的规范形状（key 走 cmdline.QuoteIfNeeded，
// 非 ASCII / 空格 / 空串都会被加上引号）。
var fuzzCommands = []string{
	"GET mykey", "SET a b", "DEL user:123", "CONFIG SET maxmemory 128mb", "DEBUG STATS",
	"INFO", "PING", "", "   ", "FLUSHALL",
	"del '/prod/x'", `DEL "/prod/x"`, "DEL 'user:1'", "del '/prod/my key'", "del '/prod/配置'",
	"get '' --prefix", "get '/app/' --prefix --limit=10", "put '/k' 'v'", "lease grant --ttl=3600",
	"'CONFIG SET' maxmemory", "CONFIG 'SET X' foo", "'GET KEY' a",
	"member list", "endpoint status", "user add alice", "snapshot save /tmp/x",
	"GET 'unterminated", "GET \\", "a | b", "a\x00b", "GET a\nb",
}

// FuzzMatchRedisRule 锁住 MatchRedisRule 的唯一硬不变式：对任意 (rule, cmd) 字符串对
// 都不能 panic。
//
// 这条不变式看着弱，但它正是这个函数反复出问题的地方：它同时是 Redis 与 etcd 的策略
// 匹配器，调用链 helper.HandleExecRedis → permission.CheckForAsset 与 IPC 的
// TestPolicyRule（策略测试面板的自由文本输入框）都能把任意字符串喂进来，而
// internal/ai 与 internal/app 全链路没有 recover()——一次 index out of range 就是
// 整个桌面应用崩溃，而不是一次 fail-closed。三轮基于举例的回归测试各自漏掉了下一个
// 形状（引号切词后带空格的命令名 token、单边退回分词），所以改用属性测试。
func FuzzMatchRedisRule(f *testing.F) {
	for _, rule := range fuzzRules {
		for _, cmd := range fuzzCommands {
			f.Add(rule, cmd)
		}
	}
	f.Fuzz(func(_ *testing.T, rule, cmd string) {
		// 不断言返回值：唯一的性质就是"不 panic"。
		_ = MatchRedisRule(rule, cmd)
	})
}
