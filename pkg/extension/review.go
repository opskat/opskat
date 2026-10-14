package extension

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// 审核结果的取值。与 command_review_svc 的 Outcome 同名，但属于扩展边界：
// 宿主在校验通过后才把它译成自己的结果。
const (
	ReviewOutcomePass   = "pass"
	ReviewOutcomeReject = "reject"
	ReviewOutcomeError  = "error"
)

// ReasonInvalidResult 是整批结果不合规时的失败原因。条数不对、问题 id 未声明、
// reject 不带问题、JSON 解析失败、调用出错或 panic，都归到这一条。
const ReasonInvalidResult = "invalid_result"

// questionIDRe 限制问题 id：审计按它统计，所以不能含空白或冒号。
var questionIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// I18nNames 是各语言的显示名。key 是语言代码（zh-CN / en），value 是该语言的名称。
type I18nNames struct {
	Name map[string]string `json:"name"`
}

// ReviewQuestion 是审核器声明的一道题。结果里只回 id，宿主按这里的名称显示。
type ReviewQuestion struct {
	ID   string    `json:"id"`
	I18n I18nNames `json:"i18n"`
}

// ReviewerDef 是 describe() 的 reviewer 段。
type ReviewerDef struct {
	Questions     []ReviewQuestion `json:"questions"`
	MaxItems      int              `json:"maxItems"`
	MaxCommandLen int              `json:"maxCommandLen"`
}

func (r *ReviewerDef) validate() error {
	if r == nil {
		return fmt.Errorf("reviewer is required")
	}
	if len(r.Questions) == 0 {
		return fmt.Errorf("reviewer.questions must declare at least one question")
	}
	seen := make(map[string]struct{}, len(r.Questions))
	for i, q := range r.Questions {
		if !questionIDRe.MatchString(q.ID) {
			return fmt.Errorf("reviewer.questions[%d].id must match %s (got %q)", i, questionIDRe.String(), q.ID)
		}
		if _, dup := seen[q.ID]; dup {
			return fmt.Errorf("reviewer.questions[%d].id %q is a duplicate", i, q.ID)
		}
		seen[q.ID] = struct{}{}
		if len(q.I18n.Name) == 0 {
			return fmt.Errorf("reviewer.questions[%d].i18n.name must name the question in at least one language", i)
		}
		for lang, name := range q.I18n.Name {
			if lang == "" || name == "" {
				return fmt.Errorf("reviewer.questions[%d].i18n.name has an empty language or name", i)
			}
		}
	}
	if r.MaxItems <= 0 {
		return fmt.Errorf("reviewer.maxItems must be a positive number of commands per batch (got %d)", r.MaxItems)
	}
	if r.MaxCommandLen <= 0 {
		return fmt.Errorf("reviewer.maxCommandLen must be a positive number of bytes (got %d)", r.MaxCommandLen)
	}
	return nil
}

func (d *Descriptor) validateReviewer() error {
	if d.Reviewer == nil {
		return nil
	}
	if err := d.Reviewer.validate(); err != nil {
		return fmt.Errorf("describe(): %w", err)
	}
	return nil
}

// IsCommandReviewer 报告扩展是否同时声明了审核能力并给出了 reviewer 段。
func (m *Manifest) IsCommandReviewer() bool {
	return m != nil && m.Capabilities.CommandReview && m.Reviewer != nil
}

// validateReviewer 要求能力与 reviewer 段成对：只声明一边会让安装确认和实际能力不一致。
func (m *Manifest) validateReviewer() error {
	declared := m.Capabilities.CommandReview
	reported := m.Reviewer != nil
	switch {
	case declared && !reported:
		return fmt.Errorf("capabilities.commandReview is declared but describe() has no reviewer section")
	case !declared && reported:
		return fmt.Errorf("describe() reports a reviewer section but capabilities.commandReview is not declared")
	case declared:
		if err := m.Reviewer.validate(); err != nil {
			return err
		}
	}
	return nil
}

// ReviewItem 是交给审核器的一条命令。不含资产 ID、名称和配置。
type ReviewItem struct {
	AssetType string `json:"assetType"`
	Command   string `json:"command"`
	Syntax    string `json:"syntax"`
}

// ReviewCommandResult 是审核器对一条命令的回答。
type ReviewCommandResult struct {
	Outcome string   `json:"outcome"`
	Failed  []string `json:"failed,omitempty"`
	Score   *float64 `json:"score,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

// validateReviewResults 校验整批结果。任何一条不合规，整批按无效结果失败。
// call 是实际调用；它返回错误（含运行时恢复的 panic）同样按无效结果处理。
func validateReviewResults(n int, questions []ReviewQuestion, call func() (json.RawMessage, error)) ([]ReviewCommandResult, error) {
	raw, err := call()
	if err != nil {
		return nil, fmt.Errorf("review %s: %w", ReasonInvalidResult, err)
	}
	var out []ReviewCommandResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("review %s: parse results: %w", ReasonInvalidResult, err)
	}
	if len(out) != n {
		return nil, fmt.Errorf("review %s: got %d results for %d commands", ReasonInvalidResult, len(out), n)
	}
	known := make(map[string]struct{}, len(questions))
	for _, q := range questions {
		known[q.ID] = struct{}{}
	}
	for i, r := range out {
		if err := validateOneReviewResult(i, r, known); err != nil {
			return nil, fmt.Errorf("review %s: %w", ReasonInvalidResult, err)
		}
	}
	return out, nil
}

func validateOneReviewResult(i int, r ReviewCommandResult, known map[string]struct{}) error {
	switch r.Outcome {
	case ReviewOutcomePass:
		if len(r.Failed) > 0 || r.Reason != "" {
			return fmt.Errorf("results[%d]: pass carries a failure", i)
		}
	case ReviewOutcomeReject:
		if len(r.Failed) == 0 {
			return fmt.Errorf("results[%d]: reject requires failed question ids", i)
		}
		if r.Reason != "" {
			return fmt.Errorf("results[%d]: reject carries a reason; reasons belong on error", i)
		}
	case ReviewOutcomeError:
		if r.Reason == "" {
			return fmt.Errorf("results[%d]: error requires a reason", i)
		}
		if len(r.Failed) > 0 {
			return fmt.Errorf("results[%d]: error carries question ids; those belong on reject", i)
		}
	default:
		return fmt.Errorf("results[%d]: outcome %q is not pass, reject or error", i, r.Outcome)
	}
	seen := make(map[string]struct{}, len(r.Failed))
	for _, id := range r.Failed {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("results[%d]: question id %q is not declared", i, id)
		}
		if _, dup := seen[id]; dup {
			return fmt.Errorf("results[%d]: question id %q is repeated", i, id)
		}
		seen[id] = struct{}{}
	}
	if r.Score != nil && (*r.Score < 0 || *r.Score > 1) {
		return fmt.Errorf("results[%d]: score %v is outside [0,1]", i, *r.Score)
	}
	return nil
}
