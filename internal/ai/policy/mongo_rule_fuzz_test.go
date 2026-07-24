package policy

import "testing"

// fuzzMongoRules 是规则侧种子：两个内置组的全部规则（builtin:mongo-readonly /
// mongo-readwrite / mongo-dangerous-deny），加上大小写变体、collection 收窄、
// 引号与未闭合引号等历史上崩过或误判过的形状。直接抄一份而不是从
// policy_group_entity 读，理由与 fuzzRules（redis_policy_fuzz_test.go）相同：
// 避免 policy → policy_group_entity 的测试期依赖。
var fuzzMongoRules = []string{
	// builtin:mongo-readonly / mongo-readwrite
	"find", "findOne", "aggregate", "countDocuments",
	"insertOne", "insertMany", "updateOne", "updateMany", "deleteOne", "deleteMany",
	// builtin:mongo-dangerous-deny
	"dropDatabase", "dropCollection",
	// 大小写变体（EqualFold 折叠的那一维）
	"DropDatabase", "DROPCOLLECTION", "FIND",
	// collection 收窄与通配
	"deleteMany users", "deleteMany log*", "deleteMany *", "find public_*", "*",
	// grant 会存进来的完整命令串形状
	"listCollections --db=analytics", `deleteMany logs --db=prod --query='{"filter":{"a":1}}'`,
	// 空、引号、未闭合引号、shell 元字符
	"", "   ", "deleteMany 'my coll'", "deleteMany 'users", `find "users`, "find users extra",
	"find [a-", "find \\", "|", "a && b", "$(x)", "a\x00b", "--db=prod", "-- ", "find --",
}

// fuzzMongoCommands 是命令侧种子：CanonicalizeMongoCommand 会产出的规范形状
// （cmdline.Render：collection 走 QuoteIfNeeded，flag 按名排序），存量的裸 op 规则
// 的裸 op，以及模型可能直接写错的形状。
var fuzzMongoCommands = []string{
	"find", "findOne", "insertOne", "deleteMany", "listDatabases", "listCollections",
	"dropDatabase", "dropCollection", "DropCollection",
	"find users", `find users --query='{"filter":{"a":1}}'`,
	`deleteMany logs --db=prod --query='{"filter":{"level":"debug"}}'`,
	"deleteMany logs --db=prod", "listCollections --db=analytics",
	`aggregate events --db=analytics --query='{"pipeline":[]}'`,
	"deleteMany 'my coll'", "deleteMany '配置'", "deleteMany ''",
	"", "   ", "deleteMany 'users", `find "users`, "find users extra",
	"find --db=prod", "--db=prod", "-- ", "a | b", "a\x00b", "find a\nb", "find \\",
}

// FuzzMatchMongoRule 锁住 MatchMongoRule 的唯一硬不变式：对任意 (rule, command)
// 字符串对都不能 panic。
//
// 这条不变式看着弱，但它正是这一族匹配器反复出问题的地方（MatchRedisRule 因为
// 带空格的命令名 token 下标越界崩过整个应用）。MatchMongoRule 的输入同样来自
// 三条能塞进任意字符串的路径：统一 exec 的模型输入、策略测试面板
// （IPC TestPolicyRule）的自由文本规则框、以及 DB 里存量的 grant pattern；
// internal/ai 与 internal/app 全链路没有 recover()，一次越界就是桌面应用崩溃，
// 而不是一次 fail-closed。
func FuzzMatchMongoRule(f *testing.F) {
	for _, rule := range fuzzMongoRules {
		for _, cmd := range fuzzMongoCommands {
			f.Add(rule, cmd)
		}
	}
	f.Fuzz(func(_ *testing.T, rule, command string) {
		// 不断言返回值：唯一的性质就是"不 panic"。
		_ = MatchMongoRule(rule, command)
	})
}
