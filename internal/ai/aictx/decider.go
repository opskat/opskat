package aictx

// 决策方分组：把审计行的 DecisionSource 归成用户能理解的「谁做的决定」。审计页与
// opsctl list audit 都按分组筛选，归组定义只此一处（spec 2026-11-23-audit-log-filters 决策 4）。
const (
	DeciderPolicy    = "policy"    // 命令策略
	DeciderUser      = "user"      // 用户手动审批
	DeciderGrant     = "grant"     // 授权（grant）
	DeciderAssisted  = "assisted"  // 辅助审批
	DeciderAutopilot = "autopilot" // Autopilot
	DeciderExecGate  = "exec_gate" // 执行前拦截：命令在权限检查之前被挡下（全部 exec_*）
	DeciderBatch     = "batch"     // 批量执行汇总行（全部 batch_*）
	DeciderNone      = "none"      // 无需审批：DecisionSource 为空
)

// DeciderGroups 是分组的展示顺序。
var DeciderGroups = []string{
	DeciderPolicy, DeciderUser, DeciderGrant, DeciderAssisted, DeciderAutopilot,
	DeciderExecGate, DeciderBatch, DeciderNone,
}

var deciderSources = map[string][]string{
	DeciderPolicy:    {SourcePolicyAllow, SourcePolicyDeny},
	DeciderUser:      {SourceUserAllow, SourceUserDeny},
	DeciderGrant:     {SourceGrantAllow, SourceGrantDeny},
	DeciderAssisted:  {SourceAssistedAllow},
	DeciderAutopilot: {SourceAutopilotAllow, SourceAutopilotDeny},
	DeciderExecGate: {
		SourceExecUnsupportedType, SourceExecGateBlocked, SourceExecPrecheckFailed,
		SourceExecCanonicalizeError, SourceExecTypeMismatch, SourceExecResolveFailed,
		SourceExecInvalidInput,
	},
	DeciderBatch: {SourceBatchComplete, SourceBatchPartialFailure, SourceBatchFailed},
	DeciderNone:  {""},
}

var sourceDecider = func() map[string]string {
	m := make(map[string]string)
	for group, sources := range deciderSources {
		for _, s := range sources {
			m[s] = group
		}
	}
	return m
}()

// DeciderOf 返回决策来源所属的分组；未归组的来源（未来新增尚未登记）返回 false。
func DeciderOf(source string) (string, bool) {
	group, ok := sourceDecider[source]
	return group, ok
}

// DeciderSources 返回分组包含的全部决策来源；未知分组返回 false。
func DeciderSources(group string) ([]string, bool) {
	sources, ok := deciderSources[group]
	return sources, ok
}
