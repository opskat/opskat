package permission

import (
	"context"

	"github.com/opskat/opskat/internal/ai/aictx"
)

// MatchGrant 报告该资产上是否已有一条批准过的常驻授权覆盖这条命令。
//
// 导出的理由与 RegisterPolicyCheck 一样：运行期注册进来的类型（扩展提供的资产类型）
// 的判定函数住在本包之外，而 grant 匹配是**每个** PolicyCheckFunc 在返回 NeedConfirm
// 之前必须走的最后一步。内置类型的检查函数在本包内直接调 matchGrantForAsset；包外的
// 检查函数漏掉这一步的后果，正是扩展路径以前的样子：用户点过"始终允许"，下一条同样的
// 命令还是弹框。
func MatchGrant(ctx context.Context, assetID int64, command, approvalType string) (aictx.CheckResult, bool) {
	result := matchGrantForAsset(ctx, assetID, command, approvalType)
	if result == nil {
		return aictx.CheckResult{}, false
	}
	return *result, true
}

// PolicyGroupsForAsset 返回资产自身与其组链上 CommandPolicy 引用的权限组 ID，按
// 资产 → 组 → 父组的顺序去重。
//
// 扩展的策略语言是 action 名，规则住在 manifest 声明的 `ext:` 权限组里，因此扩展类型的
// 检查函数需要的正是"这个资产实际引用了哪些组"。此前 Bridge.GetExtensionPolicyGroups
// 忽略资产、无条件返回 manifest 的默认组——用户在资产上改过权限组也没用。
func PolicyGroupsForAsset(ctx context.Context, assetID int64) []string {
	asset := resolveAssetForPolicy(ctx, assetID)
	if asset == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
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
			out = append(out, id)
		}
	}
	return out
}
