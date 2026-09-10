package permission

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// 扩展提供的资产类型的永久规则落点（opsctl policy allow / deny / rm / show）。
//
// 落点是**共用的 CommandPolicy 列**，规则形状 `ext:<policyType>:<action>`——不新增
// 数据库列、不写 migration，与 cp 面把方向前缀写进同一列（rule_persist.go 的 cpLand）
// 是同一个做法。policyType 段不能省：一个资产组可以同时挂着多个扩展的资产，而
// CommandPolicy 只有一列，不带类型段两个扩展的同名动作会串。
//
// 与内置类型的两点差别，都来自"扩展的策略语言是动作名"：
//   - 落点校验动作名。扩展声明的动作集是封闭的（manifest 的 policies.actions 由每个
//     工具的 policyAction 派生），一条写不进这个集合的规则永远匹配不上任何调用——
//     与其落一条永远不生效的规则，不如在写库前点名可用动作。
//   - 匹配是动作名全等，不支持 `*` 通配。运行期判定
//     （policy.CheckExtensionPolicy）本来就是动作名精确包含，落点这边多认一种通配
//     语法只会让 `policy show` 标出运行期并不存在的遮蔽；要"放行全部"就把动作列全。
//
// 注册是运行期的：扩展随用户启用/禁用来去，因此重复注册返回错误而不是 panic
// （与 RegisterDynamicExecutor / RegisterPolicyCheck 一致）。

// extRulePrefix 是一个扩展策略面在共用 CommandPolicy 列里的命名空间前缀。
func extRulePrefix(policyType string) string {
	return "ext:" + policyType + ":"
}

// RegisterExtensionRuleSink 为一个扩展提供的资产类型注册永久规则落点。
// actions 是该扩展声明的全部策略动作，落点只接受其中之一。
func RegisterExtensionRuleSink(canonicalType, policyType string, actions []string) error {
	if canonicalType == "" || policyType == "" {
		return fmt.Errorf("permission: invalid extension rule sink registration %q", canonicalType)
	}
	prefix := extRulePrefix(policyType)
	return addRuleSink(canonicalType, &ruleLanding{
		shape:         commandShape,
		refPolicyType: policyType,
		// 扩展的权限组 Policy JSON 就是 {allow_list, deny_list}，与 CommandPolicy 的
		// 两侧同形，因此用同一个形状解码；它的 kind 是 manifest 声明的策略面名，
		// 不是宿主的策略列，所以 refShape 只能由这里给出。
		refShape:  commandShape,
		land:      extLand(prefix, actions),
		match:     extActionMatch(prefix),
		ownFilter: func(rule string) bool { return strings.HasPrefix(rule, prefix) },
	})
}

// UnregisterRuleSink 移除一个运行期注册的永久规则落点（扩展禁用/卸载）。
func UnregisterRuleSink(canonicalType string) {
	landingMu.Lock()
	defer landingMu.Unlock()
	delete(ruleLandings, canonicalType)
}

// extLand 把一个动作名落成 `ext:<policyType>:<action>`。
func extLand(prefix string, actions []string) func(pattern string) ([]LandedRule, error) {
	known := slices.Clone(actions)
	return func(pattern string) ([]LandedRule, error) {
		action := strings.TrimSpace(pattern)
		if action == "" {
			return nil, errors.New("empty pattern")
		}
		if !slices.Contains(known, action) {
			return nil, fmt.Errorf(
				"%q is not a policy action of this extension: extension rules are written per action, not per command (known actions: %s)",
				pattern, strings.Join(known, ", "))
		}
		return []LandedRule{{Rule: prefix + action}}, nil
	}
}

// extActionMatch 判定一条 deny 是否遮蔽一条落点：都还原成动作名后全等。
//
// 两边形态不同是有原因的：holder 自己那一列的规则带命名空间前缀（同一列还住着别的
// 类型），而权限组里的规则是裸动作名（一个扩展权限组整体就属于这个策略面，
// policy.CheckExtensionPolicy 也是拿裸动作名比的）。还原掉前缀，两个来源才用同一
// 把尺子。
func extActionMatch(prefix string) func(denyRule, rule string) bool {
	action := func(s string) string {
		return strings.TrimPrefix(strings.TrimSpace(s), prefix)
	}
	return func(denyRule, rule string) bool {
		return action(denyRule) == action(rule)
	}
}
