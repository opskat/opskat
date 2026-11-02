package command_review_svc

import (
	"context"
	"errors"
	"fmt"
)

// Question 是审核器声明的一道题。结果里只回 id，宿主按 Names（语言 → 名称）显示。
type Question struct {
	ID    string            `json:"id"`
	Names map[string]string `json:"names"`
}

// 审核器对一条命令的回答。error 是"没能得到判断"，按审核失败处理。
const (
	AnswerPass   = "pass"
	AnswerReject = "reject"
	AnswerError  = "error"
)

// Answer 是审核器对一条命令的回答。Failed 是 reject 时没满足的题目 id，Scores 是它们的
// 评分（题目 id → 0~1）；Reason 是 error 时的原因 id。
type Answer struct {
	Outcome string             `json:"outcome"`
	Failed  []string           `json:"failed,omitempty"`
	Scores  map[string]float64 `json:"scores,omitempty"`
	Reason  string             `json:"reason,omitempty"`
}

// Reviewer 是要用的审核器。审核服务对每条要审的命令调用一次 Review。
type Reviewer interface {
	// Identity 是扩展名、版本和设置哈希，都参与缓存键。
	Identity() (name, version, settingsHash string)
	// Questions 是审核器声明的题目，回答里只回题目 id，宿主按这里的名称显示。
	Questions() []Question
	// MaxCommandLen 是审核器声明的单条送审文本（原命令加追加的解码脚本）最大长度，更长的不送审。
	MaxCommandLen() int
	// Review 审一条命令：assetType 是规范资产类型，command 是送审文本（原命令原样开头，
	// 解码出的脚本追加在后，见 permission.ReviewRequest.Text），syntax 是命令写法
	// （shell / redis / text）。ctx 带着这一次调用的超时。
	Review(ctx context.Context, assetType, command, syntax string) (Answer, error)
}

// ReviewerSource 给出这次要用的审核器。没有可用的返回 ErrNoReviewer，
// 要用的这次不可用返回 ErrReviewerUnavailable。
type ReviewerSource interface {
	Current(ctx context.Context) (Reviewer, error)
}

// 审核器在这里用不了的原因（ReviewerInfo.Reason），设置页按它们显示；详细的错误只进日志。
const (
	UnusableNotRunning      = "not_running"       // 扩展没在这个进程里运行
	UnusableDigestMismatch  = "digest_mismatch"   // 包与安装时记录的摘要不一致
	UnusableStoreRequired   = "store_required"    // 正式版只认商店安装的审核器
	UnusableNoInstallRecord = "no_install_record" // 没有安装来源记录
	UnusableCheckFailed     = "check_failed"      // 资格检查本身没能完成（读不了记录或包等）
)

// IneligibleError 是审核器资格检查没过的原因：Code 是 Unusable* 之一。
type IneligibleError struct {
	Code string
	Err  error
}

func (e *IneligibleError) Error() string { return e.Err.Error() }
func (e *IneligibleError) Unwrap() error { return e.Err }

// UnusableReason 是资格检查错误 err 对应的 Unusable* 原因；不是 IneligibleError 的按检查失败。
func UnusableReason(err error) string {
	var ie *IneligibleError
	if errors.As(err, &ie) {
		return ie.Code
	}
	return UnusableCheckFailed
}

// ReviewerInfo 是设置页列出的一个审核器。Usable 为假时 Reason 是 Unusable* 之一；
// Signed 表示从官方商店安装、签名已校验；Questions 是它声明的题目，扩展详情据此列出审核项。
type ReviewerInfo struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName"`
	Version     string     `json:"version"`
	Usable      bool       `json:"usable"`
	Reason      string     `json:"reason,omitempty"`
	Signed      bool       `json:"signed"`
	Questions   []Question `json:"questions"`
}

// State 是设置页和权限模式卡片看到的审核状态。Selected 是设置里选的、仍可选的审核器；
// Active 是实际在用的，没有可用的时为空，Problem 说明原因（与审核失败原因同名：
// no_reviewer / reviewer_unavailable）。
type State struct {
	Reviewers []ReviewerInfo `json:"reviewers"`
	Selected  string         `json:"selected"`
	Active    string         `json:"active"`
	Problem   string         `json:"problem,omitempty"`
}

// validateAnswer 校验审核器对一条命令的回答：结果合规、题目 id 都已声明、分数在 0~1。
// 不合规的回答按无效结果失败。
func validateAnswer(questions []Question, r Answer) error {
	known := make(map[string]struct{}, len(questions))
	for _, q := range questions {
		known[q.ID] = struct{}{}
	}
	switch r.Outcome {
	case AnswerPass:
		if len(r.Failed) > 0 || r.Reason != "" || len(r.Scores) > 0 {
			return fmt.Errorf("pass carries a failure")
		}
	case AnswerReject:
		if len(r.Failed) == 0 {
			return fmt.Errorf("reject requires failed question ids")
		}
		if r.Reason != "" {
			return fmt.Errorf("reject carries a reason; reasons belong on error")
		}
	case AnswerError:
		if r.Reason == "" {
			return fmt.Errorf("error requires a reason")
		}
		if len(r.Failed) > 0 || len(r.Scores) > 0 {
			return fmt.Errorf("error carries question ids or scores; those belong on reject")
		}
	default:
		return fmt.Errorf("outcome %q is not pass, reject or error", r.Outcome)
	}
	seen := make(map[string]struct{}, len(r.Failed))
	for _, id := range r.Failed {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("question id %q is not declared", id)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("question id %q is repeated", id)
		}
		seen[id] = struct{}{}
	}
	for id, score := range r.Scores {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("score for question %q, which is not in failed", id)
		}
		if score < 0 || score > 1 {
			return fmt.Errorf("score for %q is %v, outside [0,1]", id, score)
		}
	}
	return nil
}
