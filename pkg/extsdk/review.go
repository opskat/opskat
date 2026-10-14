package opskat

import (
	"encoding/json"
	"fmt"
)

// 审核结果的取值。宿主整批校验后才采用。
const (
	ReviewPass   = "pass"
	ReviewReject = "reject"
	ReviewError  = "error"
)

// ReviewItem 是宿主送来的一条命令。敏感信息已由宿主替换；不含资产 ID、名称和配置。
type ReviewItem struct {
	AssetType string `json:"assetType"`
	Command   string `json:"command"`
	Syntax    string `json:"syntax"`
}

// ReviewResult 是对一条命令的回答。
// reject 必须带未通过的问题 id；error 必须带原因 id；pass 两者都不带。
type ReviewResult struct {
	Outcome string   `json:"outcome"`
	Failed  []string `json:"failed,omitempty"`
	Score   *float64 `json:"score,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

// ReviewQuestion 是审核器声明的一道题。Name 的 key 是语言代码。
type ReviewQuestion struct {
	ID   string
	Name map[string]string
}

// ReviewContext 是一次批量审核的上下文。没有资产身份。
type ReviewContext struct{}

// reviewerEntry 是已注册的审核器。nil 表示没有注册。
type reviewerEntry struct {
	questions     []ReviewQuestion
	maxItems      int
	maxCommandLen int
	review        func(ctx *ReviewContext, items []ReviewItem) ([]ReviewResult, error)
}

var commandReviewer *reviewerEntry

// CommandReviewer 注册命令审核器。
// questions 至少一道；maxItems、maxCommandLen 必须为正。重复注册或参数不合法在 init 时 panic，
// 让扩展加载失败而不是第一次审核才发现。
func CommandReviewer(questions []ReviewQuestion, maxItems, maxCommandLen int, review func(ctx *ReviewContext, items []ReviewItem) ([]ReviewResult, error)) {
	if commandReviewer != nil {
		panic("opskat: command reviewer is already registered")
	}
	if len(questions) == 0 {
		panic("opskat: command reviewer needs at least one question")
	}
	seen := map[string]struct{}{}
	for i, q := range questions {
		if q.ID == "" {
			panic(fmt.Sprintf("opskat: command reviewer question %d has an empty id", i))
		}
		if _, dup := seen[q.ID]; dup {
			panic(fmt.Sprintf("opskat: command reviewer question %q is declared twice", q.ID))
		}
		seen[q.ID] = struct{}{}
		if len(q.Name) == 0 {
			panic(fmt.Sprintf("opskat: command reviewer question %q needs a name in at least one language", q.ID))
		}
	}
	if maxItems <= 0 || maxCommandLen <= 0 {
		panic(fmt.Sprintf("opskat: command reviewer maxItems (%d) and maxCommandLen (%d) must be positive", maxItems, maxCommandLen))
	}
	if review == nil {
		panic("opskat: command reviewer function is required")
	}
	copied := append([]ReviewQuestion(nil), questions...)
	commandReviewer = &reviewerEntry{
		questions:     copied,
		maxItems:      maxItems,
		maxCommandLen: maxCommandLen,
		review:        review,
	}
}

func dispatchReview(input []byte) (json.RawMessage, error) {
	if commandReviewer == nil {
		return nil, fmt.Errorf("no command reviewer registered")
	}
	var req struct {
		Items []ReviewItem `json:"items"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("parse review request: %w", err)
	}
	if req.Items == nil {
		req.Items = []ReviewItem{}
	}
	results, err := commandReviewer.review(&ReviewContext{}, req.Items)
	if err != nil {
		return nil, err
	}
	if results == nil {
		results = []ReviewResult{}
	}
	return json.Marshal(results)
}
