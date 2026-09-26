package permission

import (
	"context"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
)

// MatchExtensionGrant is the grant lookup of a classify-registered extension type's
// policy check (a PolicyCheckFunc lives outside this package, so the lookup is
// exported): it builds the current call's grant key from its live (policyType, action,
// resource) classification — never from the raw command text — so a later call that
// spells the same request differently (different flag order, an equivalent literal)
// still hits the grant, and a grant for one resource never covers another. See
// extGrantMatch for how a stored pattern is compared against it.
func MatchExtensionGrant(ctx context.Context, assetID int64, approvalType, policyType, action, resource string) (aictx.CheckResult, bool) {
	key := extGrantKey(policyType, action, resource)
	result := matchGrantForAssetWith(ctx, assetID, key, approvalType, extGrantMatch)
	if result == nil {
		return aictx.CheckResult{}, false
	}
	return *result, true
}

// ExtensionPolicyForAsset 收集一个扩展策略面在资产 holder 链（资产 → 组 → 父组）
// 上的两样东西：引用的权限组 ID，以及 holder 自己那一列里属于这个策略面的永久规则
// （已去掉命名空间前缀，还原成 `<action>[:<resource-glob>]`）。两者一趟走完——每条命令都要问一次，而组链要读库。
//
// 之所以由本包给出：holder 链的走法（policyHoldersForAsset）与永久规则的落点形状
// （rule_ext.go 的命名空间前缀）都是本包的知识，而扩展的判定函数住在包外。
// 此前只有权限组这一半，因此 opsctl policy allow 写下的规则没有任何读它的地方。
func ExtensionPolicyForAsset(ctx context.Context, assetID int64, policyType string) (groups []string, own policy.ExtensionPolicyRule) {
	asset := resolveAssetForPolicy(ctx, assetID)
	if asset == nil {
		return nil, own
	}
	prefix := extRulePrefix(policyType)
	seen := make(map[string]struct{})
	for _, holder := range policyHoldersForAsset(ctx, asset) {
		p, err := holder.GetCommandPolicy()
		if err != nil || p == nil {
			continue
		}
		for _, id := range p.Groups {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			groups = append(groups, id)
		}
		own.AllowList = append(own.AllowList, extRulesOf(prefix, p.AllowList)...)
		own.DenyList = append(own.DenyList, extRulesOf(prefix, p.DenyList)...)
	}
	return groups, own
}

// extRulesOf 从一列共用的命令规则里挑出属于该策略面的，并去掉命名空间前缀。
func extRulesOf(prefix string, rules []string) []string {
	var own []string
	for _, r := range rules {
		if rule, ok := strings.CutPrefix(r, prefix); ok {
			own = append(own, rule)
		}
	}
	return own
}
