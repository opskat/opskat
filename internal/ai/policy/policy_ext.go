package policy

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/ai/aictx"
	"go.uber.org/zap"
)

// ExtensionPolicyRule represents the allow/deny rule lists in an extension policy
// group's Policy JSON — and a holder's own permanent rules on the same policy face,
// which share the {allow_list, deny_list} shape. Each rule is `<action>` or
// `<action>:<resource-glob>` (see MatchExtensionRule).
type ExtensionPolicyRule struct {
	AllowList []string `json:"allow_list"`
	DenyList  []string `json:"deny_list"`
}

// ExtensionCheck 是一次扩展策略判定的输入：判的是哪个策略面、动作是什么，以及规则的
// 两个来源——holder 自身那一列（opsctl policy allow/deny 写的永久规则）与它引用的
// 权限组（manifest 声明的 ext: 组、或用户自建的同类型组）。
type ExtensionCheck struct {
	// PolicyType 是扩展 manifest 声明的策略面名。引用的权限组按它筛：类型不同的组
	// （比如挂在同一资产上的 command 组）里的规则是另一套语言，拿来撞动作名只会
	// 得到似是而非的判定。
	PolicyType string
	GroupIDs   []string
	// Own 是 holder 链上各 holder 自身在这个策略面上的规则。
	Own ExtensionPolicyRule
	// Action / Resource 是 guest 的 check_policy 按这次调用的参数给出的分类。Action 由
	// 调用方先对照类型声明的动作集合核过；Resource 是 guest 的任意文本（可空）。
	Action   string
	Resource string
}

// ExtensionRuleParts 把一条扩展规则拆成动作与资源 glob。
// 在第一个 ':' 处切开：动作名不含 ':'（describe() 校验），其后整段都是 glob——glob 自己
// 可以写 ':' 去匹配含 ':' 的资源。scoped 为 false 表示规则不限资源。
func ExtensionRuleParts(rule string) (action, glob string, scoped bool) {
	return strings.Cut(rule, ":")
}

// MatchExtensionRule 判定一条扩展规则是否覆盖一次调用的 (action, resource)：动作名全等，
// 不带资源的规则匹配该动作的任意资源，带资源的规则用与命令规则相同的 glob 语义
// （matchGlobPattern / path.Match：'*' 不跨 '/'）匹配整个资源串。
//
// 资源永远只作为被匹配的一方整体参与，不会被拆开：guest 无论在资源里写什么 ':'，都
// 改变不了规则的动作段，也够不到别的策略面的规则（策略面由调用方按前缀 / 组类型选定）。
func MatchExtensionRule(rule, action, resource string) bool {
	ruleAction, glob, scoped := ExtensionRuleParts(rule)
	if ruleAction != action {
		return false
	}
	return !scoped || matchGlobPattern(glob, resource)
}

// CheckExtensionPolicy 判定一次扩展调用的 (action, resource)：Deny → Allow → NeedConfirm。
//
// 两个来源的规则先合流再判，优先序与内置命令类型（permission.checkCommandPolicyPermission）
// 一致——deny 无条件先判、再 allow，而不是按"holder 比组更近"分层。理由是同一个：
// 一条 deny 之所以写下来，是为了在任何来源的 allow 之上生效；让近处的 allow 盖住远处的
// deny，等于允许在资产上给自己扩权，绕过组级的禁令。落不到 allow / deny 的动作返回
// NeedConfirm，由调用方接 grant 匹配与审批（fail-closed）。
func CheckExtensionPolicy(ctx context.Context, in ExtensionCheck) aictx.CheckResult {
	allow := slices.Clone(in.Own.AllowList)
	deny := slices.Clone(in.Own.DenyList)

	for _, pg := range fetchPolicyGroups(ctx, in.GroupIDs) {
		if pg.PolicyType != in.PolicyType {
			continue
		}
		var rule ExtensionPolicyRule
		if err := json.Unmarshal([]byte(pg.Policy), &rule); err != nil {
			logger.Ctx(ctx).Warn("unmarshal extension policy group",
				zap.String("id", pg.BuiltinID), zap.Error(err))
			continue
		}
		allow = append(allow, rule.AllowList...)
		deny = append(deny, rule.DenyList...)
	}

	if rule, ok := firstExtensionMatch(deny, in.Action, in.Resource); ok {
		msg := "action denied by extension policy: " + in.Action
		if in.Resource != "" {
			msg += " on " + in.Resource
		}
		return aictx.CheckResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourcePolicyDeny,
			Message:        msg,
			MatchedPattern: rule,
		}
	}
	if rule, ok := firstExtensionMatch(allow, in.Action, in.Resource); ok {
		return aictx.CheckResult{
			Decision:       aictx.Allow,
			DecisionSource: aictx.SourcePolicyAllow,
			MatchedPattern: rule,
		}
	}
	return aictx.CheckResult{Decision: aictx.NeedConfirm}
}

func firstExtensionMatch(rules []string, action, resource string) (string, bool) {
	for _, rule := range rules {
		if MatchExtensionRule(rule, action, resource) {
			return rule, true
		}
	}
	return "", false
}
