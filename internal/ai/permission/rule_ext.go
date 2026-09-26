package permission

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/opskat/opskat/internal/ai/policy"
)

// 扩展提供的资产类型的永久规则落点（opsctl policy allow / deny / rm / show）。
//
// 落点是**共用的 CommandPolicy 列**，规则形状 `ext:<policyType>:<action>` 或
// `ext:<policyType>:<action>:<resource-glob>`——不新增
// 数据库列、不写 migration，与 cp 面把方向前缀写进同一列（rule_persist.go 的 cpLand）
// 是同一个做法。policyType 段不能省：一个资产组可以同时挂着多个扩展的资产，而
// CommandPolicy 只有一列，不带类型段两个扩展的同名动作会串。
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

// extRulePrefix 是一个扩展策略面在共用 CommandPolicy 列里的命名空间前缀。
func extRulePrefix(policyType string) string {
	return policy.ExtRulePrefix + policyType + ":"
}

// RegisterExtensionRuleSink 为一个扩展提供的资产类型注册永久规则落点。
// actions 是该扩展声明的全部策略动作，落点只接受以其中之一为动作段的规则。
func RegisterExtensionRuleSink(canonicalType, policyType string, actions []string) error {
	if canonicalType == "" || policyType == "" {
		return fmt.Errorf("permission: invalid extension rule sink registration %q", canonicalType)
	}
	prefix := extRulePrefix(policyType)
	land := extLand(prefix, actions)
	return addRuleSink(canonicalType, &ruleLanding{
		shape:         commandShape,
		refPolicyType: policyType,
		// 扩展的权限组 Policy JSON 就是 {allow_list, deny_list}，与 CommandPolicy 的
		// 两侧同形，因此用同一个形状解码；它的 kind 是 manifest 声明的策略面名，
		// 不是宿主的策略列，所以 refShape 只能由这里给出。
		refShape:     commandShape,
		land:         land,
		grantRequest: land,
		match:        extRuleShadows(prefix),
		ownFilter:    func(rule string) bool { return strings.HasPrefix(rule, prefix) },
	})
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

// extensionGrantFor resolves a grant request — request_permission, opsctl grant,
// or the user's edit of one — for assetType (spec 参数级策略 › 授权请求).
//
// An extension asset only matches grants shaped like its permanent rules
// (MatchExtensionGrant), so its grant request is written the way a rule is —
// `<action>` or `<action>:<resource-glob>`, one per line — and validated by the very
// codec a permanent rule lands through (extLand: declared action, well-formed glob).
// A command-shaped or undeclared pattern is an error: persisting it would store a
// grant nothing ever consults while telling the caller it was approved.
//
// isExt is false for every type whose grant requests are command-shaped (all built-in
// types); the caller keeps its grant pattern unchanged.
func extensionGrantFor(assetType, patterns string) (grant ExtensionGrant, isExt bool, err error) {
	landing, ok := ruleLandingFor(assetType)
	if !ok || landing.grantRequest == nil {
		return ExtensionGrant{}, false, nil
	}
	grant.Type = assetType
	for _, line := range strings.Split(patterns, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		landed, err := landing.grantRequest(line)
		if err != nil {
			return ExtensionGrant{}, true, fmt.Errorf("invalid grant pattern %q for an extension asset: %w", line, err)
		}
		for _, r := range landed {
			grant.Rules = append(grant.Rules, r.Rule)
		}
	}
	if len(grant.Rules) == 0 {
		return ExtensionGrant{}, true, errors.New("empty grant request: extension grants are <action> or <action>:<resource-glob>")
	}
	return grant, true, nil
}

// ExtensionGrantForAsset is extensionGrantFor for an asset id — the opsctl grant
// entry point, which receives asset ids rather than assets. An asset that cannot be
// resolved is not an extension asset as far as this is concerned (isExt false): its
// grant request keeps the command-shaped path it always had.
func ExtensionGrantForAsset(ctx context.Context, assetID int64, patterns string) (ExtensionGrant, bool, error) {
	asset := resolveAssetForPolicy(ctx, assetID)
	if asset == nil {
		return ExtensionGrant{}, false, nil
	}
	return extensionGrantFor(asset.Type, patterns)
}

// UnregisterRuleSink 移除一个运行期注册的永久规则落点（扩展禁用/卸载）。
func UnregisterRuleSink(canonicalType string) {
	landingMu.Lock()
	defer landingMu.Unlock()
	delete(ruleLandings, canonicalType)
}

// extLand 把 `<action>` 或 `<action>:<resource-glob>` 落成 `ext:<policyType>:` 前缀的规则。
func extLand(prefix string, actions []string) func(pattern string) ([]LandedRule, error) {
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
		return []LandedRule{{Rule: prefix + rule}}, nil
	}
}

// extRuleShadows 判定一条 deny 是否遮蔽一条落点。
//
// 两边形态不同是有原因的：holder 自己那一列的规则带命名空间前缀（同一列还住着别的
// 类型），而权限组里的规则没有（一个扩展权限组整体就属于这个策略面，
// policy.CheckExtensionPolicy 也是拿去掉前缀的规则比的）。还原掉前缀，两个来源才用
// 同一把尺子。
//
// 遮蔽要求 deny 覆盖落点能匹配的每一个调用：不限资源的落点只被不限资源的 deny 遮蔽；
// 限资源的落点把它的 glob 当作资源文本交给 deny 匹配——与命令形状拿 deny 模式去撞
// allow 规则原文是同一个近似。
func extRuleShadows(prefix string) func(denyRule, rule string) bool {
	strip := func(s string) string {
		return strings.TrimPrefix(strings.TrimSpace(s), prefix)
	}
	return func(denyRule, rule string) bool {
		deny := strip(denyRule)
		action, glob, scoped := policy.ExtensionRuleParts(strip(rule))
		if !scoped {
			denyAction, _, denyScoped := policy.ExtensionRuleParts(deny)
			return !denyScoped && denyAction == action
		}
		return policy.MatchExtensionRule(deny, action, glob)
	}
}
