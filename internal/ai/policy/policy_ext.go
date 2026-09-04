package policy

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/ai/aictx"
	"go.uber.org/zap"
)

// ExtensionPolicyRule represents the allow/deny action lists in an extension policy
// group's Policy JSON — and, with the namespace prefix stripped, a holder's own
// permanent extension rules, which are stored in the shared CommandPolicy column
// under the same {allow_list, deny_list} shape.
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
	// Own 是 holder 链自身那一列的规则，动作名已还原（去掉 ext:<policyType>: 前缀）。
	Own    ExtensionPolicyRule
	Action string
}

// CheckExtensionPolicy 判定一个扩展动作：Deny → Allow → NeedConfirm。
//
// 两个来源的规则先合流再判，优先序与内置命令类型（permission.checkCommandPolicyPermission）
// 一致——deny 无条件先判、再 allow，而不是按"holder 比组更近"分层。理由是同一个：
// 一条 deny 之所以写下来，是为了在任何来源的 allow 之上生效；让近处的 allow 盖住远处的
// deny，等于允许在资产上给自己扩权，绕过组级的禁令。落不到 allow / deny 的动作返回
// NeedConfirm，由调用方接 grant 匹配与审批（fail-closed）。
func CheckExtensionPolicy(ctx context.Context, in ExtensionCheck) aictx.CheckResult {
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

	if slices.Contains(deny, in.Action) {
		return aictx.CheckResult{
			Decision:       aictx.Deny,
			DecisionSource: aictx.SourcePolicyDeny,
			Message:        "action denied by extension policy: " + in.Action,
			MatchedPattern: in.Action,
		}
	}
	if slices.Contains(allow, in.Action) {
		return aictx.CheckResult{
			Decision:       aictx.Allow,
			DecisionSource: aictx.SourcePolicyAllow,
			MatchedPattern: in.Action,
		}
	}
	return aictx.CheckResult{Decision: aictx.NeedConfirm}
}
