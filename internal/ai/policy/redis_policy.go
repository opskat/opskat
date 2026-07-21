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

// splitRedisTokens 对命令/规则串做引号感知切词（cmdline.Words，与 etcd 的
// FormatCommand/ParseCommand 用同一套词法），失败时退回按空白分词。
//
// 规则串是用户手写文本，未必是合法 shell 语法；cmd 串在 Redis 侧也是模型/用户直接
// 拼出来的自由文本，同样可能不合法。退回 Fields 而不是把解析失败当"零 token"处理，
// 是因为后者会让一条切不了词的 deny 规则从"总能命中"悄悄变成"永远不命中"而失效
// ——这在允许清单里表现为误放行，在拒绝清单里表现为放过本该拒绝的命令，两者都比
// "退回到切词引入前的匹配结果"更危险。
func splitRedisTokens(s string) []string {
	if words, err := cmdline.Words(s); err == nil {
		return words
	}
	return strings.Fields(s)
}

// ExtractRedisCommand 提取 Redis 命令名（含子命令）和参数
func ExtractRedisCommand(cmd string) (fullCmd string, args string) {
	parts := splitRedisTokens(cmd)
	if len(parts) == 0 {
		return "", ""
	}
	name := strings.ToUpper(parts[0])
	if len(parts) > 1 && redisMultiWordCmds[name] {
		fullCmd = name + " " + strings.ToUpper(parts[1])
		if len(parts) > 2 {
			args = strings.Join(parts[2:], " ")
		}
	} else {
		fullCmd = name
		if len(parts) > 1 {
			args = strings.Join(parts[1:], " ")
		}
	}
	return
}

// MatchRedisRule 检查 Redis 命令是否匹配规则
// 规则格式: "FLUSHDB", "CONFIG SET *", "DEL user:*"
func MatchRedisRule(rule, cmd string) bool {
	if isWildcardAll(rule) {
		cmdCmd, _ := ExtractRedisCommand(cmd)
		return cmdCmd != ""
	}

	ruleParts := splitRedisTokens(rule)
	cmdParts := splitRedisTokens(cmd)
	if len(ruleParts) == 0 || len(cmdParts) == 0 {
		return false
	}
	if redisMultiWordCmds[strings.ToUpper(ruleParts[0])] &&
		len(ruleParts) == 2 &&
		isWildcardAll(ruleParts[1]) &&
		len(cmdParts) >= 2 &&
		strings.EqualFold(ruleParts[0], cmdParts[0]) {
		return true
	}

	ruleCmd, ruleArgs := ExtractRedisCommand(rule)
	cmdCmd, cmdArgs := ExtractRedisCommand(cmd)

	if ruleCmd != cmdCmd {
		return false
	}
	// 无参数规则或 * 通配 → 匹配
	if ruleArgs == "" || ruleArgs == "*" {
		return true
	}
	if cmdArgs == "" {
		return false
	}
	// 按首个参数做 glob 匹配（key pattern）。必须从 ruleParts/cmdParts 按下标取，
	// 不能对 ruleArgs/cmdArgs 再 strings.Fields 一次——它们是 ExtractRedisCommand
	// 用空格 Join 过的字符串，一个带内部空格的 key（引号切词后空格是字面值的一
	// 部分，例如 "/prod/my key"）会被这第二次 Fields 错误地拆成两段参数。
	// ruleCmd 只可能是 1 个词或 "A B" 两个词（redisMultiWordCmds），len(Fields(ruleCmd))
	// 就是命令名占用的 token 数；ruleCmd == cmdCmd 已在上面保证，两边词数相同。
	skip := len(strings.Fields(ruleCmd))
	ruleFirstArg := ruleParts[skip]
	cmdFirstArg := cmdParts[skip]
	matched, err := path.Match(ruleFirstArg, cmdFirstArg)
	if err != nil {
		logger.Default().Warn("redis policy path match", zap.String("pattern", ruleFirstArg), zap.Error(err))
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
