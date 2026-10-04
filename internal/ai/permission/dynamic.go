package permission

import (
	"context"
	"slices"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/policy"
)

// MatchExtensionGrant is the grant lookup of a classify-registered extension type's
// policy check (a PolicyCheckFunc lives outside this package, so the lookup is
// exported): it builds one grant key per resource from the call's live (policyType,
// action, resource) classification — never from the raw command text — so a later
// call that spells the same request differently (different flag order, an
// equivalent literal) still hits the grant, and a grant for one resource never
// covers another. Every resource must be covered by some grant (different
// resources may be covered by different grants); patterns lists each grant that
// took part, once. See extGrantMatch for how a stored pattern is compared.
func MatchExtensionGrant(ctx context.Context, assetID int64, approvalType, policyType, action string, resources []string) (patterns []string, ok bool) {
	resources = policy.ExtensionResources(resources)
	keys := make([]string, len(resources))
	for i, resource := range resources {
		keys[i] = extGrantKey(policyType, action, resource)
	}
	matched := grantPatternsForAsset(ctx, assetID, keys, approvalType, extGrantMatch)
	if matched == nil {
		return nil, false
	}
	for _, p := range matched {
		if !slices.Contains(patterns, p) {
			patterns = append(patterns, p)
		}
	}
	return patterns, true
}

// ExtensionPolicyForAsset 收集一个扩展策略面在资产 holder 链（资产 → 组 → 父组）
// 上的两样东西：引用的权限组 ID，以及 holder 自己在这个策略面上的永久规则
// （`<action>[:<resource-glob>]`）。两者一趟走完——每条命令都要问一次，而组链要读库。
//
// 之所以由本包给出：holder 链的走法（policyHoldersForAsset）与策略面的读法
// （rule_ext.go 注册的落点）都是本包的知识，而扩展的判定函数住在包外。
func ExtensionPolicyForAsset(ctx context.Context, assetID int64, policyType string) (groups []string, own policy.ExtensionPolicyRule) {
	asset := resolveAssetForPolicy(ctx, assetID)
	if asset == nil {
		return nil, own
	}
	shape, ok := ruleShapeFor(policyType)
	if !ok {
		return nil, own
	}
	seen := make(map[string]struct{})
	for _, holder := range policyHoldersForAsset(ctx, asset) {
		allow, deny, refs, err := shape.ownSides(holder)
		if err != nil {
			logger.Ctx(ctx).Warn("read extension policy of holder",
				zap.String("policyType", policyType), zap.Error(err))
			continue
		}
		for _, id := range refs {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			groups = append(groups, id)
		}
		own.AllowList = append(own.AllowList, allow...)
		own.DenyList = append(own.DenyList, deny...)
	}
	return groups, own
}
