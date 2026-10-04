package aictx

// ReviewOutcome 是一次模型审核的结果。
type ReviewOutcome string

const (
	ReviewPass   ReviewOutcome = "pass"   // 审核通过
	ReviewReject ReviewOutcome = "reject" // 审核未通过
	ReviewFail   ReviewOutcome = "fail"   // 审核失败：没能得到判断
)

// ReviewInfo 是一次模型审核的结果，随 CheckResult 传到审批界面和审计。
type ReviewInfo struct {
	Mode       string             `json:"mode"` // 触发审核的权限模式：assisted / autopilot
	Outcome    ReviewOutcome      `json:"outcome"`
	Reason     string             `json:"reason,omitempty"` // 审核失败的原因
	Failed     []string           `json:"failed,omitempty"` // 审核未通过时没满足条件的题目
	Model      string             `json:"model,omitempty"`
	Scores     map[string]float64 `json:"scores,omitempty"`    // 每道题回答"是"的概率
	Threshold  float64            `json:"threshold,omitempty"` // 判断用的阈值
	DurationMs int64              `json:"duration_ms,omitempty"`
	Cached     bool               `json:"cached,omitempty"`
	Attempts   int                `json:"attempts,omitempty"` // 调用模型的次数，超时或连接出错时会重试
}

// BatchReview 从一次批量确认里各条的审核结果中挑一个，记进这次确认的审计（审计一行只记一个
// 审核结果）：优先审核未通过的，其次审核失败的，说明为什么要问人；都没审核过时为 nil。
func BatchReview(reviews []*ReviewInfo) *ReviewInfo {
	var failed *ReviewInfo
	for _, r := range reviews {
		switch {
		case r == nil:
		case r.Outcome == ReviewReject:
			return r
		case r.Outcome == ReviewFail && failed == nil:
			failed = r
		}
	}
	return failed
}
