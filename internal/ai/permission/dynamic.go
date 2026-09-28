package permission

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
)

// MatchGrant 报告该资产上是否已有一条批准过的常驻授权覆盖这条命令。
//
// 导出的理由与 RegisterPolicyCheck 一样：运行期注册进来的类型（扩展提供的资产类型）
// 的判定函数住在本包之外，而 grant 匹配是**每个** PolicyCheckFunc 在返回 NeedConfirm
// 之前必须走的最后一步。内置类型的检查函数在本包内直接调 matchGrantForAsset；包外的
// 检查函数漏掉这一步，用户点过"始终允许"后下一条同样的命令还是会弹框。
func MatchGrant(ctx context.Context, assetID int64, command, approvalType string) (aictx.CheckResult, bool) {
	result := matchGrantForAsset(ctx, assetID, command, approvalType)
	if result == nil {
		return aictx.CheckResult{}, false
	}
	return *result, true
}

// ExtensionPolicyForAsset 收集一个扩展策略面在资产 holder 链（资产 → 组 → 父组）
// 上的两样东西：引用的权限组 ID，以及 holder 自己在这个策略面上的永久规则。两者一趟
// 走完——每条命令都要问一次，而组链要读库。
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
