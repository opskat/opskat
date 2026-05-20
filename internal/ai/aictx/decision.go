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
