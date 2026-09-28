package permission

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	policyent "github.com/opskat/opskat/internal/model/entity/policy"
)

// 扩展提供的资产类型的永久规则落点（opsctl policy allow / deny / rm / show）。
//
// 每个扩展策略面（manifest 的 policies.type）在运行期注册成一个自己的策略面，与内置
// 类型"一种策略一列"同构：
//   - 资产只有一种类型，它的 CommandPolicy 列就是这个类型的策略，规则是裸动作名——
//     与桌面端详情页写下的形状相同；
//   - 资产组同时挂着多种类型，扩展的策略落在组的 ext_policy 列里、按策略面名分开，
//     从不进 shell 的 CommandPolicy，因此 shell 判定不必认识扩展规则。
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

// extensionShape 是一个扩展策略面在 holder 上的读写落点。
func extensionShape(policyType string) *shapeLanding {
	return newRuleShape(shapeSides[policyent.ExtensionPolicy]{
		get: func(h policyent.Holder) (*policyent.ExtensionPolicy, error) { return h.GetExtensionPolicy(policyType) },
		set: func(h policyRWHolder, p *policyent.ExtensionPolicy) error { return h.SetExtensionPolicy(policyType, p) },
		sides: func(p *policyent.ExtensionPolicy) (*[]string, *[]string, *[]string) {
			return &p.AllowList, &p.DenyList, &p.Groups
		},
		newOne: func() *policyent.ExtensionPolicy { return &policyent.ExtensionPolicy{} },
	})
}

// RegisterExtensionRuleSink 为一个扩展提供的资产类型注册永久规则落点及其策略面。
// actions 是该扩展声明的全部策略动作，落点只接受其中之一。
func RegisterExtensionRuleSink(canonicalType, policyType string, actions []string) error {
	if canonicalType == "" || policyType == "" {
		return fmt.Errorf("permission: invalid extension rule sink registration %q", canonicalType)
	}
	shape := extensionShape(policyType)
	if err := addRuleShape(policyType, shape); err != nil {
		return err
	}
	err := addRuleSink(canonicalType, &ruleLanding{
		shape:         shape,
		refPolicyType: policyType,
		// 扩展权限组的 Policy JSON 就是 {allow_list, deny_list}，与策略面同形。
		refShape:     shape,
		runtimeShape: policyType,
		land:         extLand(actions),
		match:        func(denyRule, rule string) bool { return strings.TrimSpace(denyRule) == strings.TrimSpace(rule) },
	})
	if err != nil {
		removeRuleShape(policyType)
	}
	return err
}

// UnregisterRuleSink 移除一个运行期注册的永久规则落点（扩展禁用/卸载），连同随它
// 注册的策略面。
func UnregisterRuleSink(canonicalType string) {
	landingMu.Lock()
	landing, ok := ruleLandings[canonicalType]
	delete(ruleLandings, canonicalType)
	landingMu.Unlock()
	if ok && landing.runtimeShape != "" {
		removeRuleShape(landing.runtimeShape)
	}
}

// extLand 校验一个动作名属于该扩展后原样落库。
func extLand(actions []string) func(pattern string) ([]LandedRule, error) {
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
		return []LandedRule{{Rule: action}}, nil
	}
}
