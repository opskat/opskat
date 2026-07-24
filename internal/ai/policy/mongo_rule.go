package policy

import (
	"path"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// MatchMongoRule 匹配一条 MongoDB 策略/grant 规则与一条 mongo 命令。
//
// 规则语法：`<operation> [collection-glob]`，与命令语法
// （`<op> [collection] [--db=…] [--query=…]`，见 internal/ai/skills/mongodb/SKILL.md）
// 的前两个位置对齐。规则里的 flag 不参与匹配：收窄只有 collection 这一维。
//
// 为什么需要它（三条约束同时成立，缺一条就是静默 fail-open）：
//
//  1. 统一 exec 送进来的是**富命令串**（`deleteMany users --db=prod --query=…`），
//     不再是裸 op。裸 op 全等比较（policyValueMatches）在富命令串上永远不命中，
//     `dropCollection` 这条内置 deny 会从 Deny 掉到 NeedConfirm——deny 丢失。
//  2. 反过来也不能直接复用 MatchCommandRule（shell glob）：它要求规则未声明的 flag
//     一个都不能多出来，`MatchCommandRule("deleteMany", "deleteMany users --db=prod")`
//     = false，写成裸 op 的组通用/grant 规则同样会静默失效。
//  3. **op 必须大小写不敏感**：被替换掉的 policyValueMatches 用 EqualFold 比较，
//     而 MatchCommandRule 的程序名比较不折叠大小写。照搬后者会让一条
//     `DenyTypes: ["DropDatabase"]` 悄悄停止拦截 `dropDatabase`。
//     collection 一侧则保持大小写敏感——MongoDB 的集合名本身区分大小写。
//
// 与 MatchRedisRule / MatchKafkaRule 同形：切词只发生一次（splitRulePair），
// 规则与命令一起切、要退回一起退回。
func MatchMongoRule(rule, command string) bool {
	ruleTokens, cmdTokens := splitRulePair(rule, command)

	cmdOp, cmdCollection := mongoRuleParts(cmdTokens)
	if cmdOp == "" {
		return false
	}
	if isWildcardAll(rule) {
		return true
	}

	ruleOp, ruleCollection := mongoRuleParts(ruleTokens)
	if ruleOp == "" || !strings.EqualFold(ruleOp, cmdOp) {
		return false
	}
	// 只写了 op 的规则（内置组与存量 grant 的形状）匹配该 op 的任意命令；
	// 显式的 `*` collection 同理，且与"命令没带 collection"也匹配
	// （listDatabases 一类操作本就没有 collection）。
	if ruleCollection == "" || ruleCollection == "*" {
		return true
	}
	if cmdCollection == "" {
		return false
	}
	matched, err := path.Match(ruleCollection, cmdCollection)
	if err != nil {
		logger.Default().Warn("mongo policy collection match",
			zap.String("pattern", ruleCollection), zap.Error(err))
	}
	return matched
}

// mongoRuleParts 从已切词的 token 里取出 operation 与 collection：第一个词是
// operation，其后**第一个非 flag 词**是 collection（mongo 命令至多一个 positional，
// 见 ParseMongoCommand）。
//
// flag 被跳过而不是当成 collection，是为了让 grant 直接可用：审批弹窗批下来的
// pattern 是规范化后的完整命令串（`listCollections --db=analytics`、
// `deleteMany logs --db=prod --query=…`）。把 `--db=analytics` 当成 collection glob
// 会让这类 grant 永远匹配不上自己批准过的那条命令——"全部允许"批完等于没批。
// 代价是 grant/规则不按 --db 与 --query 收窄（与 Kafka 只按 action+resource 收窄同理）；
// 要收窄到具体集合就写 collection。
func mongoRuleParts(tokens []string) (op, collection string) {
	if len(tokens) == 0 {
		return "", ""
	}
	op = tokens[0]
	for _, t := range tokens[1:] {
		if strings.HasPrefix(t, "--") {
			continue
		}
		return op, t
	}
	return op, ""
}
