package policy

import (
	"context"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
)

// ShellCommandRules 去掉命令策略列表里的文件传输规则（cp:*、cp:read:<路径>、cp:write:<路径>）。
// 它们与 shell 规则同存于 CommandPolicy 的 allow/deny 列表，匹配的却是远端路径，
// 不参与 shell 命令判定——尤其不能让一条 cp deny 被当成"存在具体命令 deny"。
func ShellCommandRules(rules []string) []string {
	filtered := make([]string, 0, len(rules))
	for _, rule := range rules {
		if !strings.HasPrefix(rule, "cp:") {
			filtered = append(filtered, rule)
		}
	}
	return filtered
}

// DecideUnenumerableShell 判定一条拆不出执行单元的 shell 命令：ExtractSubCommands 返回了
// parseErr，或解析成功却没有任何子命令（仅赋值、函数声明、注释等）。
//
// 这类命令上具体 deny 规则无法判定。远端 shell 与本地解析器的语法边界并不一致：bash 逐行
// 读、逐行执行，本地整串拒绝的输入里，语法错误之前的行照样会跑；`$(( 1 + ))` 这种本地判为
// 语法错误的写法，bash 只当运行时错误，后面的行继续执行。所以只有两条规则能下结论：
//   - 独立 deny "*" 拒绝一切；
//   - 独立 allow "*" 在没有任何具体 deny 规则时放行一切——配置它就是显式选择全权限。
//
// 其余一律 NeedConfirm，并把原因写进 Message，引导调用方修正命令。空白命令永不放行。
//
// allowRules / denyRules 是资产、组链与策略组合并后的生效 shell 规则（已经过 ShellCommandRules）。
func DecideUnenumerableShell(ctx context.Context, command string, parseErr error, allowRules, denyRules []string) aictx.CheckResult {
	if strings.TrimSpace(command) == "" {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}
	if i := slices.IndexFunc(denyRules, isWildcardAll); i >= 0 {
		return aictx.CheckResult{
			Decision:       aictx.Deny,
			Message:        PolicyMsg(ctx, "command blocked by policy", "命令被策略禁止执行"),
			DecisionSource: aictx.SourcePolicyDeny,
			MatchedPattern: denyRules[i],
		}
	}
	if i := slices.IndexFunc(allowRules, isWildcardAll); i >= 0 && len(denyRules) == 0 {
		return aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow, MatchedPattern: allowRules[i]}
	}
	return aictx.CheckResult{Decision: aictx.NeedConfirm, Message: unenumerableShellMessage(ctx, parseErr)}
}

func unenumerableShellMessage(ctx context.Context, parseErr error) string {
	if parseErr != nil {
		return PolicyFmt(ctx,
			"%v; the policy cannot check its sub-commands one by one, so no allow rule can match it. Fix the command syntax and retry",
			"%v；策略无法逐条校验其中的子命令，allow 规则无法匹配它。请修正命令语法后重试",
			parseErr)
	}
	return PolicyMsg(ctx,
		"no executable command was recognized, so no allow rule can match it. Fix the command and retry",
		"未识别到可执行的命令，allow 规则无法匹配它。请修正命令后重试")
}
