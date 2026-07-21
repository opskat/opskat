package policy

import (
	"context"
	"path"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// redisMultiWordCmds 多词 Redis 命令的前缀
var redisMultiWordCmds = map[string]bool{
	"CONFIG":  true,
	"ACL":     true,
	"CLUSTER": true,
	"CLIENT":  true,
	"DEBUG":   true,
	"MEMORY":  true,
	"MODULE":  true,
	"SCRIPT":  true,
	"SLOWLOG": true,
	"OBJECT":  true,
	"XGROUP":  true,
	"XINFO":   true,
}

// splitRedisPair 把规则串和命令串切成词，供一次比较使用。引号感知切词走
// cmdline.Words（与 etcd 的 FormatCommand/ParseCommand 同一套词法）。
//
// 两个串必须一起切、要退回一起退回：规则串是用户手写文本，未必是合法 shell 语法；
// cmd 串在 Redis 侧也是模型/用户直接拼出来的自由文本，同样可能不合法。
//   - 退回 strings.Fields 而不是把解析失败当"零 token"，是因为后者会让一条切不了词的
//     deny 规则从"总能命中"悄悄变成"永远不命中"而失效。
//   - 只退回失败的那一侧则更糟：两个串会被放进不同的词法空间比较——一边还带着引号
//     字符，另一边已经剥掉——本该命中的规则同样会静默失配。任一侧失败就双双退回，
//     这次比较整体落回引号感知引入前的行为，至少是自洽的。
func splitRedisPair(rule, cmd string) (ruleTokens, cmdTokens []string) {
	ruleTokens, ruleErr := cmdline.Words(rule)
	cmdTokens, cmdErr := cmdline.Words(cmd)
	if ruleErr != nil || cmdErr != nil {
		return strings.Fields(rule), strings.Fields(cmd)
	}
	return ruleTokens, cmdTokens
}

// redisCommand 是一条切好词的 Redis/etcd 命令（或规则）。
//
// 切词只发生一次，结果以 []string 往下流：Name 是大写规范化后的命令名（多词命令为
// "A B"），Args 是命令名之后的参数 token。历史上这里返回的是用空格 Join 过的字符串，
// 调用方再 Fields/数词一次，切词建立的 token 边界因此被摧毁又被不一致地重建——带空格
// 的 key 被拆成两个参数、带空格的命令名让下标越界 panic、显式空参数塌缩成"没有参数"
// 而匹配一切，三轮修复各出一个新缺陷都来自这一个根因。
type redisCommand struct {
	Name string
	Args []string
}

// parseRedisCommand 从已切词的 token 中分出命令名（含 redisMultiWordCmds 的子命令）与参数。
func parseRedisCommand(tokens []string) redisCommand {
	if len(tokens) == 0 {
		return redisCommand{}
	}
	name := strings.ToUpper(tokens[0])
	rest := tokens[1:]
	if len(rest) > 0 && redisMultiWordCmds[name] {
		name += " " + strings.ToUpper(rest[0])
		rest = rest[1:]
	}
	return redisCommand{Name: name, Args: rest}
}

// MatchRedisRule 检查 Redis 命令是否匹配规则
// 规则格式: "FLUSHDB", "CONFIG SET *", "DEL user:*"
func MatchRedisRule(rule, cmd string) bool {
	ruleTokens, cmdTokens := splitRedisPair(rule, cmd)
	command := parseRedisCommand(cmdTokens)
	if command.Name == "" {
		return false
	}
	if isWildcardAll(rule) {
		return true
	}
	if len(ruleTokens) == 0 {
		return false
	}
	// "CONFIG *" 这类多词命令的子命令通配：匹配该命令的任意子命令。
	if redisMultiWordCmds[strings.ToUpper(ruleTokens[0])] &&
		len(ruleTokens) == 2 &&
		isWildcardAll(ruleTokens[1]) &&
		len(cmdTokens) >= 2 &&
		strings.EqualFold(ruleTokens[0], cmdTokens[0]) {
		return true
	}

	r := parseRedisCommand(ruleTokens)
	if r.Name != command.Name {
		return false
	}
	// 无参数规则或单个 * 通配 → 匹配任意参数
	if len(r.Args) == 0 || (len(r.Args) == 1 && r.Args[0] == "*") {
		return true
	}
	if len(command.Args) == 0 {
		return false
	}
	// 按首个参数做 glob 匹配（key pattern）
	matched, err := path.Match(r.Args[0], command.Args[0])
	if err != nil {
		logger.Default().Warn("redis policy path match", zap.String("pattern", r.Args[0]), zap.Error(err))
	}
	return matched
}

// CheckRedisPolicy 检查 Redis 命令是否符合策略（合并默认策略后检查）
func CheckRedisPolicy(ctx context.Context, policy *asset_entity.RedisPolicy, cmd string) aictx.CheckResult {
	merged := EffectiveRedisPolicy(ctx, policy)
	return checkRedisPolicyRules(ctx, merged, cmd)
}

// CheckEtcdPolicy 检查 etcd 命令是否符合策略（EtcdPolicy 是 RedisPolicy 的类型别名，
// 命令格式 "op [key] [value]" 与 Redis "cmd [args]" 同构，复用 MatchRedisRule 匹配）。
func CheckEtcdPolicy(ctx context.Context, policy *asset_entity.EtcdPolicy, cmd string) aictx.CheckResult {
	merged := EffectiveEtcdPolicy(ctx, policy)
	return checkRedisPolicyRules(ctx, merged, cmd)
}

// checkRedisPolicyRules 检查 Redis 命令是否符合给定策略（不合并默认策略）
func checkRedisPolicyRules(ctx context.Context, policy *asset_entity.RedisPolicy, cmd string) aictx.CheckResult {
	if policy == nil {
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
	}
	// deny list 检查
	for _, rule := range policy.DenyList {
		if MatchRedisRule(rule, cmd) {
			return aictx.CheckResult{
				Decision:       aictx.Deny,
				Message:        PolicyFmt(ctx, "Redis command denied by policy: %s", "Redis 命令被策略禁止: %s", cmd),
				DecisionSource: aictx.SourcePolicyDeny,
				MatchedPattern: rule,
			}
		}
	}
	// allow list 白名单
	if len(policy.AllowList) > 0 {
		for _, rule := range policy.AllowList {
			if MatchRedisRule(rule, cmd) {
				return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
			}
		}
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
}
