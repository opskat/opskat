package aictx

// Decision 权限判定结果
type Decision int

const (
	Allow       Decision = iota // 直接放行
	Deny                        // 拒绝
	NeedConfirm                 // 需要用户确认
)

// 决策来源常量
const (
	SourcePolicyAllow = "policy_allow" // 命令策略白名单放行
	SourcePolicyDeny  = "policy_deny"  // 命令策略黑名单拒绝
	SourceUserAllow   = "user_allow"   // 用户手动允许
	SourceUserDeny    = "user_deny"    // 用户手动拒绝
	SourceGrantAllow  = "grant_allow"  // Grant 预批准匹配放行
	SourceGrantDeny   = "grant_deny"   // Grant 权限申请被拒绝
)

// 统一 exec 的短路来源：命令在触达权限检查之前就已经确定不会执行。
//
// 这四条路径都不经过策略判定，本来不会写 Decision/DecisionSource，审计行于是落成
// decision 为空——而 decision 为空在既有语义里表示"这次工具调用压根不涉及权限检查"
// （list_assets 之类）。门禁短路那条更糟：它返回引导文本而不是 error，success 也是 1，
// 于是一条命令从未执行的行看起来跟执行成功的行一模一样。
//
// 复用 Decision/DecisionSource 这套既有字段（而不是加一列）把它们标成 Deny：语义上
// 与策略拒绝一致——都是"被挡下、没有执行"，靠 DecisionSource 区分是被谁挡下的。
const (
	SourceExecUnsupportedType   = "exec_unsupported_type"   // 该资产类型没有注册执行器
	SourceExecGateBlocked       = "exec_gate_blocked"       // 该类型用法文档本会话未到过模型面前
	SourceExecPrecheckFailed    = "exec_precheck_failed"    // 类型前置条件不满足（如串口无活跃会话）
	SourceExecCanonicalizeError = "exec_canonicalize_error" // 命令没通过该类型的规范化（语法/配置错误）
	SourceExecTypeMismatch      = "exec_type_mismatch"      // exec/batch_exec 的可选 type 断言与资产真实类型不符，命令在权限检查之前就被挡下
	SourceExecResolveFailed     = "exec_resolve_failed"     // 资产引用不存在或歧义
	SourceExecInvalidInput      = "exec_invalid_input"      // 命令等边界参数为空或形状错误
	SourceBatchComplete         = "batch_complete"          // batch_exec 所有 item 均成功
	SourceBatchPartialFailure   = "batch_partial_failure"   // 至少一项成功、至少一项拒绝/失败
	SourceBatchFailed           = "batch_failed"            // batch_exec 没有任何成功 item
)

// CheckResult 权限检查结果
type CheckResult struct {
	Decision       Decision
	Message        string   // 返回给 AI 的消息
	HintRules      []string // 拒绝时的允许规则提示
	DecisionSource string   // 决策来源（SourcePolicyAllow 等常量）
	MatchedPattern string   // 匹配的命令模式
}

// DecisionString 返回决策的字符串表示（用于审计日志存储）
func (r CheckResult) DecisionString() string {
	switch r.Decision {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	default:
		return ""
	}
}
