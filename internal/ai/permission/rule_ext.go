package permission

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/policy"
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
// 与内置类型的两点差别，都来自"扩展的策略语言是动作名 + 资源 glob"：
//   - 落点校验动作名。扩展声明的动作集是封闭的（manifest 的 policies.actions 由每个
//     工具声明的动作派生），一条写不进这个集合的规则永远匹配不上任何调用——
//     与其落一条永远不生效的规则，不如在写库前点名可用动作。资源 glob 也在落库前
//     校验语法。
//   - 动作段全等、不支持 `*` 通配；只有资源段是 glob（policy.MatchExtensionRule，与
//     运行期判定同一个函数），要"放行全部动作"就把动作列全。
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
// actions 是该扩展声明的全部策略动作，落点只接受以其中之一为动作段的规则。
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
		refShape:       shape,
		runtimeShape:   policyType,
		land:           extLand(actions),
		grantsAreRules: true,
		match:          extRuleShadows,
	})
	if err != nil {
		removeRuleShape(policyType)
	}
	return err
}

// ExtensionGrant is a validated grant request for an extension asset.
type ExtensionGrant struct {
	// Type is the extension asset type. A grant approval item for the request carries
	// it instead of the generic "grant", so an edit made in the dialog is held to the
	// same syntax (ParseApprovalResponse) and persisted through the same codec
	// (SaveGrantPatternsForApproval).
	Type string
	// Rules are the persisted ext:<policyType>:<action>[:<resource-glob>] grants, one
	// per requested line.
	Rules []string
}

// extensionGrantFor resolves a grant request — request_permission, the opsctl
// approval channel, or the user's edit of one — for assetType (spec 参数级策略 › 授权请求).
//
// An extension asset only matches grants keyed by its classification
// (MatchExtensionGrant), so its grant request is written the way a rule is —
// `<action>` or `<action>:<resource-glob>`, one per line — and validated by the very
// codec a permanent rule lands through (extLand: declared action, well-formed glob),
// then namespaced with the policy type (extGrantRule): grants share one table with
// every other type, holder columns do not.
// A command-shaped or undeclared pattern is an error: persisting it would store a
// grant nothing ever consults while telling the caller it was approved.
//
// isExt is false for every type whose grant requests are command-shaped (all built-in
// types); the caller keeps its grant pattern unchanged.
func extensionGrantFor(assetType, patterns string) (grant ExtensionGrant, isExt bool, err error) {
	landing, ok := ruleLandingFor(assetType)
	if !ok || !landing.grantsAreRules {
		return ExtensionGrant{}, false, nil
	}
	grant.Type = assetType
	for _, line := range strings.Split(patterns, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		landed, err := landing.land(line)
		if err != nil {
			return ExtensionGrant{}, true, fmt.Errorf("invalid grant pattern %q for an extension asset: %w", line, err)
		}
		for _, r := range landed {
			grant.Rules = append(grant.Rules, extGrantRule(landing.refPolicyType, r.Rule))
		}
	}
	if len(grant.Rules) == 0 {
		return ExtensionGrant{}, true, errors.New("empty grant request: extension grants are <action> or <action>:<resource-glob>")
	}
	return grant, true, nil
}

// ExtensionGrantForAsset is extensionGrantFor for an asset id — the opsctl
// approval-channel entry point, which receives asset ids rather than assets. An
// asset that cannot be resolved is not an extension asset as far as this is concerned
// (isExt false): its grant request keeps the command-shaped path it always had.
func ExtensionGrantForAsset(ctx context.Context, assetID int64, patterns string) (ExtensionGrant, bool, error) {
	asset := resolveAssetForPolicy(ctx, assetID)
	if asset == nil {
		return ExtensionGrant{}, false, nil
	}
	return extensionGrantFor(asset.Type, patterns)
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

// extLand 校验 `<action>` 或 `<action>:<resource-glob>`（动作属于该扩展、glob 合法）后原样落库。
func extLand(actions []string) func(pattern string) ([]LandedRule, error) {
	known := slices.Clone(actions)
	return func(pattern string) ([]LandedRule, error) {
		rule := strings.TrimSpace(pattern)
		if rule == "" {
			return nil, errors.New("empty pattern")
		}
		action, glob, scoped := policy.ExtensionRuleParts(rule)
		if !slices.Contains(known, action) {
			return nil, fmt.Errorf(
				"%q is not a policy action of this extension: extension rules are <action> or <action>:<resource-glob>, not commands (known actions: %s)",
				action, strings.Join(known, ", "))
		}
		if scoped {
			if glob == "" {
				return nil, fmt.Errorf("%q has an empty resource glob: leave out the ':' to match every resource", pattern)
			}
			if _, err := path.Match(glob, ""); err != nil {
				return nil, fmt.Errorf("%q has an invalid resource glob: %w", pattern, err)
			}
		}
		return []LandedRule{{Rule: rule}}, nil
	}
}

// extRuleShadows 判定一条 deny 是否遮蔽一条落点。
//
// 遮蔽要求 deny 覆盖落点能匹配的每一个调用：不限资源的落点只被不限资源的 deny 遮蔽；
// 限资源的落点把它的 glob 当作资源文本交给 deny 匹配——与命令形状拿 deny 模式去撞
// allow 规则原文是同一个近似。
func extRuleShadows(denyRule, rule string) bool {
	deny := strings.TrimSpace(denyRule)
	action, glob, scoped := policy.ExtensionRuleParts(strings.TrimSpace(rule))
	if !scoped {
		denyAction, _, denyScoped := policy.ExtensionRuleParts(deny)
		return !denyScoped && denyAction == action
	}
	return policy.MatchExtensionRule(deny, action, glob)
}
