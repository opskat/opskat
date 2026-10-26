// Command jev-command-review 是 OpsKat 的命令审核扩展：把辅助审批 / Autopilot 下原本要问人的
// 命令交给 TypeSafe 的 Jev 模型判断，只拦明确的危险（破坏数据、中断服务、下载并执行代码）。
//
// 宿主负责其余的一切：哪些命令送审、敏感信息替换、解码执行的展开、缓存、超时与重试、按权限
// 模式落结果。这里只回答"这条命令危不危险"。API key 由宿主加密保存，扩展只拿到占位
// {{setting.api_key}}，写进请求头后由宿主在发往设置里的服务地址时替换。
package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/opskat/opskat/extensions/jev-command-review/jevreview"
	"github.com/opskat/opskat/extensions/jev-command-review/typesafe"
	opskat "github.com/opskat/opskat/pkg/extsdk"
)

func main() {}

// settings 是扩展的设置项。base_url 是 format:"endpoint"：宿主只把密钥发往这里填的地址。
type settings struct {
	APIKey    string `json:"api_key" title:"settings.apiKey.title" desc:"settings.apiKey.desc" format:"password"`
	BaseURL   string `json:"base_url" title:"settings.baseUrl.title" desc:"settings.baseUrl.desc" format:"endpoint" default:"https://api.typesafe.ai"`
	Model     string `json:"model" title:"settings.model.title" desc:"settings.model.desc" default:"jev-latest"`
	Threshold string `json:"threshold" title:"settings.threshold.title" desc:"settings.threshold.desc" default:"0.5"`
}

// 一次只审一条，宿主把多条命令并发地分批送来。
const (
	maxItems      = 1
	maxCommandLen = 4000
)

var questions = []opskat.ReviewQuestion{
	{ID: jevreview.QuestionDestructive, Name: map[string]string{"en": "may destroy or irreversibly change data", "zh-CN": "可能破坏或不可恢复地修改数据"}},
	{ID: jevreview.QuestionDisruptive, Name: map[string]string{"en": "may interrupt services", "zh-CN": "可能中断服务"}},
	{ID: jevreview.QuestionRemoteCode, Name: map[string]string{"en": "downloads and runs code", "zh-CN": "会下载并执行代码"}},
}

func init() {
	opskat.Extension(opskat.Meta{
		Icon:        "shield",
		DisplayName: "extension.displayName",
		Description: "extension.description",
	})
	opskat.SettingsSchema[settings]()
	opskat.CommandReviewer(questions, maxItems, maxCommandLen, review)
}

// 设置有问题时每条命令的失败原因，与宿主的审核失败原因同名，宿主据此提示用户去改设置。
const reasonNotConfigured = "not_configured"

func review(_ *opskat.ReviewContext, items []opskat.ReviewItem) ([]opskat.ReviewResult, error) {
	cfg, ok := loadConfig()
	out := make([]opskat.ReviewResult, len(items))
	for i, item := range items {
		if !ok {
			out[i] = opskat.ReviewResult{Outcome: opskat.ReviewError, Reason: reasonNotConfigured}
			continue
		}
		out[i] = reviewOne(cfg, item)
	}
	return out, nil
}

type config struct {
	apiKey, baseURL, model string
	threshold              float64
}

// loadConfig 读设置。没有 API key、服务地址，或阈值不是 0~1 之间的数，都算没配置好。
func loadConfig() (config, bool) {
	s, err := opskat.Settings()
	if err != nil {
		opskat.Log("error", "read settings: "+err.Error())
		return config{}, false
	}
	cfg := config{apiKey: s["api_key"], baseURL: strings.TrimRight(s["base_url"], "/"), model: s["model"]}
	if cfg.model == "" {
		cfg.model = jevreview.DefaultModel
	}
	cfg.threshold = jevreview.DefaultThreshold
	if v := strings.TrimSpace(s["threshold"]); v != "" {
		t, err := strconv.ParseFloat(v, 64)
		if err != nil || t <= 0 || t >= 1 {
			opskat.Log("warn", "threshold must be a number between 0 and 1: "+v)
			return config{}, false
		}
		cfg.threshold = t
	}
	return cfg, cfg.apiKey != "" && cfg.baseURL != ""
}

// reviewOne 审一条命令。超时与重试由宿主控制（到时取消这次调用并重试一次），这里不重试。
func reviewOne(cfg config, item opskat.ReviewItem) opskat.ReviewResult {
	client := typesafe.New(cfg.apiKey,
		typesafe.WithBaseURL(cfg.baseURL),
		typesafe.WithHTTPClient(&http.Client{Transport: opskat.NewHTTPTransport()}))
	ctx := context.Background()
	resp, err := client.Evaluate(ctx, jevreview.Request(cfg.model, item.AssetType, item.Command))
	if err != nil {
		opskat.Log("warn", "evaluate: "+err.Error())
		return opskat.ReviewResult{Outcome: opskat.ReviewError, Reason: jevreview.FailReason(err)}
	}
	// 只取声明过的题目：兼容服务多回的答案不算，宿主会把未声明的题目判成无效结果。
	// 声明过的每一题 Evaluate 都保证有答案。
	scores := make(map[string]float64, len(jevreview.Questions))
	for id := range jevreview.Questions {
		scores[id] = resp.Answers[id].Noul
	}
	failed := jevreview.Failed(scores, cfg.threshold)
	if len(failed) == 0 {
		return opskat.ReviewResult{Outcome: opskat.ReviewPass}
	}
	// 只回未通过题目的评分：宿主按题目 id 显示，审计里每题一个数。
	failedScores := make(map[string]float64, len(failed))
	for _, id := range failed {
		failedScores[id] = scores[id]
	}
	return opskat.ReviewResult{Outcome: opskat.ReviewReject, Failed: failed, Scores: failedScores}
}
