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
	// Action / Resources 是 guest 的 check_policy 按这次调用的参数给出的分类。Action 由
	// 调用方先对照类型声明的动作集合核过；Resources 是这次调用触及的资源，每个都是
	// path.Match glob（pkg/extension 解码时把字面资源整体转义、把 PolicyResources 的
	// '*' / '?' 留作通配，见 ext_resource.go）。零个资源按空资源判，与资源还是单个串时一致。
	Action    string
	Resources []string
}

// ExtensionResources 是一次分类实际参与判定的资源：没有资源的调用按一个空资源判——
// 资源还是单个串时，"无资源"就是空串，规则、grant key、记住预填都按它写。
func ExtensionResources(resources []string) []string {
	if len(resources) == 0 {
		return []string{""}
	}
	return resources
}

// ExtensionRuleParts 把一条扩展规则拆成动作与资源 glob。
// 在第一个 ':' 处切开：动作名不含 ':'（describe() 校验），其后整段都是 glob——glob 自己
// 可以写 ':' 去匹配含 ':' 的资源。scoped 为 false 表示规则不限资源。
func ExtensionRuleParts(rule string) (action, glob string, scoped bool) {
	return strings.Cut(rule, ":")
}

// MatchExtensionRule 判定一条扩展规则是否覆盖一次调用的某个资源 (action, resource)：动作名
// 全等，不带资源的规则覆盖该动作的任意资源，带资源的规则要求它的 glob 匹配 resource 代表的
// 每一个名字（globCovers；字面资源即 path.Match 匹配这个名字，'*' 不跨 '/'）。这是 allow
// 规则与 grant 的语义；deny 规则用 extensionDenyHits（可能重叠即命中）。
//
// 资源永远只作为被匹配的一方整体参与，不会被拆开：guest 无论在资源里写什么 ':'，都
// 改变不了规则的动作段，也够不到别的策略面的规则（策略面由调用方按前缀 / 组类型选定）。
func MatchExtensionRule(rule, action, resource string) bool {
	ruleAction, glob, scoped := ExtensionRuleParts(rule)
	if ruleAction != action {
		return false
	}
	return !scoped || globCovers(glob, resource)
}

// extensionDenyHits 判定一条 deny 规则是否命中某个资源：动作名全等，不带资源的 deny
// 命中该动作的任意资源，带资源的 deny 只要它的 glob 可能与 resource 代表的某个名字相同
// 即命中（globMayMatch，判不出按命中）。
func extensionDenyHits(rule, action, resource string) bool {
	ruleAction, glob, scoped := ExtensionRuleParts(rule)
	if ruleAction != action {
		return false
	}
	return !scoped || globMayMatch(glob, resource)
}

// ExtensionPolicyResult 是 CheckExtensionPolicy 的判定。Deny / Allow 已是终局；NeedConfirm
// 时 Uncovered 是没有 allow 规则覆盖的那些资源——由调用方拿 grant 逐个覆盖
// （AllowedByGrants），覆盖不全才问用户。
type ExtensionPolicyResult struct {
	aictx.CheckResult
	Uncovered []string
	// covering 是覆盖了其余资源的 allow 规则，放行时与 grant 一起记进 MatchedPattern。
	covering []string
}

// AllowedByGrants 是 grant 覆盖了全部 Uncovered 资源之后的放行结果：MatchedPattern 列出
// 参与判定的全部 allow 规则与 grant（审计沿用这一列，不另开列）。
func (r ExtensionPolicyResult) AllowedByGrants(grants []string) aictx.CheckResult {
	return aictx.CheckResult{
		Decision:       aictx.Allow,
		DecisionSource: aictx.SourceGrantAllow,
		MatchedPattern: joinPatterns(append(slices.Clone(r.covering), grants...)),
	}
}

// CheckExtensionPolicy 判定一次扩展调用的 (action, resources)：Deny → Allow → NeedConfirm，
// 逐个资源判：任一资源命中 deny 即拒；每个资源都被某条 allow 覆盖（不同资源可由不同规则
// 覆盖）才放行；其余返回 NeedConfirm 并交出未覆盖的资源。
//
// 两个来源的规则先合流再判，优先序与内置命令类型（permission.checkCommandPolicyPermission）
// 一致——deny 无条件先判、再 allow，而不是按"holder 比组更近"分层。理由是同一个：
// 一条 deny 之所以写下来，是为了在任何来源的 allow 之上生效；让近处的 allow 盖住远处的
// deny，等于允许在资产上给自己扩权，绕过组级的禁令。落不到 allow / deny 的调用返回
// NeedConfirm，由调用方接 grant 匹配与审批（fail-closed）。
//
// 拒绝时 MatchedPattern 是命中的 deny 规则；多资源调用在每条规则后列出它拒绝的资源
// （"<rule> (<r1>, <r2>)"，多条以 "; " 相连）。资源按扩展返回的原样展示。
func CheckExtensionPolicy(ctx context.Context, in ExtensionCheck) ExtensionPolicyResult {
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

	resources := ExtensionResources(in.Resources)
	if denied := extensionDenials(deny, in.Action, resources); len(denied) > 0 {
		return ExtensionPolicyResult{CheckResult: denyResult(in.Action, denied, len(resources) > 1)}
	}

	var result ExtensionPolicyResult
	for _, resource := range resources {
		i := slices.IndexFunc(allow, func(rule string) bool { return MatchExtensionRule(rule, in.Action, resource) })
		if i < 0 {
			result.Uncovered = append(result.Uncovered, resource)
			continue
		}
		result.covering = append(result.covering, allow[i])
	}
	if len(result.Uncovered) > 0 {
		result.Decision = aictx.NeedConfirm
		return result
	}
	result.CheckResult = aictx.CheckResult{
		Decision:       aictx.Allow,
		DecisionSource: aictx.SourcePolicyAllow,
		MatchedPattern: joinPatterns(result.covering),
	}
	return result
}

// extensionDenial 是一条 deny 规则与它拒绝的资源（按规则首次命中的顺序）。
type extensionDenial struct {
	rule      string
	resources []string
}

// extensionDenials 给每个资源找第一条命中它的 deny 规则，按规则归组。
func extensionDenials(deny []string, action string, resources []string) []extensionDenial {
	var denied []extensionDenial
	for _, resource := range resources {
		i := slices.IndexFunc(deny, func(rule string) bool { return extensionDenyHits(rule, action, resource) })
		if i < 0 {
			continue
		}
		j := slices.IndexFunc(denied, func(d extensionDenial) bool { return d.rule == deny[i] })
		if j < 0 {
			denied = append(denied, extensionDenial{rule: deny[i]})
			j = len(denied) - 1
		}
		denied[j].resources = append(denied[j].resources, DisplayExtensionResource(resource))
	}
	return denied
}

func denyResult(action string, denied []extensionDenial, multiResource bool) aictx.CheckResult {
	var names []string
	patterns := make([]string, 0, len(denied))
	for _, d := range denied {
		for _, name := range d.resources {
			if name != "" {
				names = append(names, name)
			}
		}
		pattern := d.rule
		if multiResource {
			pattern += " (" + strings.Join(d.resources, ", ") + ")"
		}
		patterns = append(patterns, pattern)
	}
	msg := "action denied by extension policy: " + action
	if len(names) > 0 {
		msg += " on " + strings.Join(names, ", ")
	}
	return aictx.CheckResult{
		Decision:       aictx.Deny,
		DecisionSource: aictx.SourcePolicyDeny,
		Message:        msg,
		MatchedPattern: strings.Join(patterns, "; "),
	}
}

// joinPatterns lists the rules / grants that took part in an allow, each once.
func joinPatterns(patterns []string) string {
	var unique []string
	for _, p := range patterns {
		if !slices.Contains(unique, p) {
			unique = append(unique, p)
		}
	}
	return strings.Join(unique, "; ")
}
