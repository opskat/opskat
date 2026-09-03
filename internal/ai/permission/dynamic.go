package permission

import (
	"context"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
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

// ExtensionPolicyForAsset 收集一个扩展策略面在资产 holder 链（资产 → 组 → 父组）
// 上的两样东西：引用的权限组 ID，以及 holder 自己那一列里属于这个策略面的永久规则
// （已还原成裸动作名）。两者一趟走完——每条命令都要问一次，而组链要读库。
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
		own.AllowList = append(own.AllowList, extActionsOf(prefix, p.AllowList)...)
		own.DenyList = append(own.DenyList, extActionsOf(prefix, p.DenyList)...)
	}
	return groups, own
}

// extActionsOf 从一列共用的命令规则里挑出属于该策略面的，并去掉命名空间前缀。
func extActionsOf(prefix string, rules []string) []string {
	var actions []string
	for _, r := range rules {
		if action, ok := strings.CutPrefix(r, prefix); ok {
			actions = append(actions, action)
		}
	}
	return actions
}
