// Package command_review_svc 用 Jev 格式（TypeSafe System One API）的模型审核命令：
// 命令没被规则放行、原本要问人时，由它判断能不能自动执行。
// 不区分资产类型：所有类型用同一组题目、同一个通过标准。
package command_review_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/pkg/typesafe"
	"go.uber.org/zap"
)

// 题目 ID 只给代码用，不会发给模型；题目的完整含义写在 instructions 里。
// 审核未通过时，Result.Failed 里是这些 ID。
const (
	QuestionDestructive = "destructive"
	QuestionDisruptive  = "disruptive"
	QuestionRemoteCode  = "remote_code"
)

// 设置没填时的默认值：TypeSafe 官方地址上的 Jev 具体版本。
const (
	DefaultBaseURL   = typesafe.DefaultBaseURL
	DefaultModel     = "jev-1.13.0"
	DefaultTimeout   = 5 * time.Second
	DefaultThreshold = 0.5
	// MaxCommandLen 是能审核的最长命令（替换敏感信息后）；更长的按审核失败处理，不截断后硬判。
	MaxCommandLen = 4000
)

// ErrNotConfigured 表示没有配置审核模型的 API key。
var ErrNotConfigured = errors.New("typesafe api key not configured")

// questionsVersion 在题目文字或通过标准变化时递增，让旧的缓存失效。
const questionsVersion = "3"

// cacheTTL 是审核结果的缓存时长。
const cacheTTL = 7 * 24 * time.Hour

// maxParallelReviews 是批量审核时同时调用模型的上限。
const maxParallelReviews = 4

// maxAttempts 是一次审核最多调用模型的次数：超时或连接出错时再试一次。opsctl 每次都是新进程，
// 第一次请求要重新解析域名、建立连接，偶尔会超时；第二次通常很快。
const maxAttempts = 2

// riskQuestions 只问明确的危险：破坏数据、中断服务、下载并执行代码。只读的查看类命令
// （状态、日志、配置、进程、容器、定时任务、网络状态）在每道题的"否"里都写明了，
// 这类命令不应被拦下来。"破坏数据"只算不可恢复的修改，容易撤销的小改动也写成"否"。
var riskQuestions = map[string]typesafe.Question{
	QuestionDestructive: {
		Type:         typesafe.QuestionNoul,
		Instructions: "Would running `command` on a `asset_type` asset clearly destroy data or make an irreversible change, such as deleting files or directories, wiping or formatting disks, dropping or truncating databases, tables, or collections, bulk-deleting keys, records, or storage objects, or deleting users, credentials, or keys?",
		Criteria: map[string]string{
			"true":  "The command clearly deletes, wipes, overwrites, or irreversibly changes data, files, databases, users, credentials, or keys.",
			"false": "The command only reads, lists, searches, or shows status, logs, configuration, processes, containers, scheduled jobs, or network state, or it makes a small change that is easy to undo.",
		},
	},
	QuestionDisruptive: {
		Type:         typesafe.QuestionNoul,
		Instructions: "Would running `command` on a `asset_type` asset clearly interrupt a running system, such as stopping, restarting, killing, or disabling a service, container, process, or the host, or changing firewall, routing, or network settings so that the host or its services become unreachable?",
		Criteria: map[string]string{
			"true":  "The command stops, restarts, kills, or disables something that is running, or cuts off network access to the host or its services.",
			"false": "The command only reads, lists, searches, or shows status (such as service or VPN status), logs, configuration, processes, containers, scheduled jobs (such as timers), or network state, and leaves services, processes, the host, and the network running as they are.",
		},
	},
	QuestionRemoteCode: {
		Type:         typesafe.QuestionNoul,
		Instructions: "Does `command` download code or a script from the network and run it, for example by piping `curl` or `wget` output into a shell?",
		Criteria: map[string]string{
			"true":  "The command fetches code or a script from the network and runs it.",
			"false": "The command does not run code fetched from the network: it only reads, lists, searches, or shows status, logs, configuration, processes, containers, scheduled jobs, or network state, or it downloads a file without running it, or queries a remote service.",
		},
	},
}

// Outcome 是审核结果，和审批、审计里记的是同一个类型。
type Outcome = aictx.ReviewOutcome

const (
	OutcomePass   = aictx.ReviewPass   // 审核通过
	OutcomeReject = aictx.ReviewReject // 审核未通过
	OutcomeFail   = aictx.ReviewFail   // 审核失败：没能得到判断
)

// 审核失败的原因。
const (
	ReasonNotConfigured = "not_configured"
	// 保存过 API key，但读不出来（如换了主密钥解不开）
	ReasonAPIKeyUnreadable = "api_key_unreadable" // #nosec G101 -- 失败原因的名字，不是凭据。
	ReasonTooLong          = "too_long"
	ReasonUnparseable      = "unparseable" // 命令解析不了，没法替换敏感信息，不发送
	ReasonUndecodable      = "undecodable" // 认得出解码后执行，但解不开要执行的内容，不发送
	ReasonTimeout          = "timeout"
	ReasonInvalidAPIKey    = "invalid_api_key"
	ReasonUnavailable      = "unavailable"
)

// isConfigError 判断审核失败是不是设置有问题：只有用户改设置才能恢复，要提醒用户。
func isConfigError(reason string) bool {
	return reason == ReasonNotConfigured || reason == ReasonAPIKeyUnreadable || reason == ReasonInvalidAPIKey
}

// Input 是一次审核的输入。Syntax 决定怎样找出命令里的敏感信息（见 RedactSensitive）。
type Input struct {
	AssetType string
	Command   string
	Syntax    Syntax
}

// Result 是一次审核的结果。
type Result struct {
	Outcome   Outcome            `json:"outcome"`
	Reason    string             `json:"reason,omitempty"` // 审核失败的原因
	Failed    []string           `json:"failed,omitempty"` // 审核未通过时，没满足条件的题目
	Model     string             `json:"model,omitempty"`
	Scores    map[string]float64 `json:"scores,omitempty"`    // 每道题回答"是"的概率
	Threshold float64            `json:"threshold,omitempty"` // 判断用的阈值
	Duration  time.Duration      `json:"duration,omitempty"`
	Cached    bool               `json:"cached,omitempty"`
	Attempts  int                `json:"attempts,omitempty"` // 调用模型的次数，超时或连接出错时会重试
}

// Info 是这次审核在审批和审计里记的样子；mode 是触发审核的权限模式。
func (r Result) Info(mode string) *aictx.ReviewInfo {
	return &aictx.ReviewInfo{
		Mode:       mode,
		Outcome:    r.Outcome,
		Reason:     r.Reason,
		Failed:     r.Failed,
		Model:      r.Model,
		Scores:     r.Scores,
		Threshold:  r.Threshold,
		DurationMs: r.Duration.Milliseconds(),
		Cached:     r.Cached,
		Attempts:   r.Attempts,
	}
}

// Config 是审核设置。APIKey 为空表示没有配置。
type Config struct {
	APIKey        string
	BaseURL       string // 兼容 TypeSafe System One API 的服务地址
	Model         string
	Threshold     float64       // 任意一题"是"的概率达到它就不通过
	Timeout       time.Duration // 每次调用模型的超时；超时后会重试（见 maxAttempts）
	MaxCommandLen int
}

// Evaluator 是模型调用，*typesafe.Client 实现了它。
type Evaluator interface {
	Evaluate(ctx context.Context, req typesafe.Request) (*typesafe.Response, error)
}

// Cache 保存审核结果。Get 找不到时返回 (nil, nil)。
type Cache interface {
	Get(ctx context.Context, key string) (*Result, error)
	Put(ctx context.Context, key string, r Result, ttl time.Duration) error
}

// Service 审核命令。
type Service interface {
	Review(ctx context.Context, in Input) Result
	// ReviewBatch 审核多条命令，结果与输入一一对应；调用模型这一步并行。
	ReviewBatch(ctx context.Context, ins []Input) []Result
	// TestModel 用给定的设置（可以是设置页上还没保存的值）发一次最小请求，
	// 检查地址、API key 和模型是否可用，返回服务端实际作答的模型版本。
	TestModel(ctx context.Context, cfg Config) (string, error)
	// Status 返回本进程启动以来最近一次审核失败的情况，没失败过则为空。
	Status() Status
	// SetConfigErrorListener 注册配置错误（未配置 / API key 读不出来 / API key 无效）的通知，
	// 同一种错误只通知一次，审核恢复成功后再出错会重新通知。
	SetConfigErrorListener(fn func(reason string))
}

// Status 是最近一次审核失败的情况，设置页据此提示配置问题。
type Status struct {
	LastFailReason string    `json:"lastFailReason,omitempty"`
	LastFailAt     time.Time `json:"lastFailAt,omitempty"`
}

type service struct {
	config    func() (Config, error)
	cache     Cache
	evaluator func(apiKey, baseURL string) Evaluator

	mu       sync.Mutex
	status   Status
	listener func(reason string)
	notified map[string]bool
}

// Settings 是用户填的审核设置，没填的项为零值。
type Settings struct {
	APIKey    string
	BaseURL   string
	Model     string
	TimeoutMs int
	Threshold float64
}

// NewConfig 按设置生成 Config，没填（零值）的项用默认值。
func NewConfig(s Settings) Config {
	cfg := Config{APIKey: s.APIKey, BaseURL: s.BaseURL, Model: s.Model, Threshold: s.Threshold, Timeout: time.Duration(s.TimeoutMs) * time.Millisecond, MaxCommandLen: MaxCommandLen}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	return cfg
}

// New 创建审核服务。config 每次审核时读取，设置修改后立即生效；它返回错误表示保存过的
// API key 读不出来，这时审核失败（ReasonAPIKeyUnreadable），不会当成没配置。
func New(config func() (Config, error), cache Cache, evaluator func(apiKey, baseURL string) Evaluator) Service {
	return &service{config: config, cache: cache, evaluator: evaluator, notified: map[string]bool{}}
}

func (s *service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *service) SetConfigErrorListener(fn func(reason string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listener = fn
}

// recordFailure 记下审核失败；配置错误通知一次。
func (s *service) recordFailure(r Result) Result {
	s.mu.Lock()
	s.status = Status{LastFailReason: r.Reason, LastFailAt: time.Now()}
	var notify func(string)
	if isConfigError(r.Reason) && !s.notified[r.Reason] {
		s.notified[r.Reason] = true
		notify = s.listener
	}
	s.mu.Unlock()
	if notify != nil {
		notify(r.Reason)
	}
	return r
}

// recordSuccess 让之后再出的配置错误重新通知。最近一次失败留着，设置页照样显示。
func (s *service) recordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notified = map[string]bool{}
}

var defaultService Service

// Register 注册默认审核服务。
func Register(s Service) { defaultService = s }

// Default 返回默认审核服务，没有注册时为 nil。
func Default() Service { return defaultService }

// reviewState 是发给模型的上下文。
type reviewState struct {
	AssetType string `json:"asset_type"`
	Command   string `json:"command"`
}

func (s *service) Review(ctx context.Context, in Input) Result {
	return s.ReviewBatch(ctx, []Input{in})[0]
}

// pendingReview 是一条需要调用模型的审核。
type pendingReview struct {
	idx     int
	key     string
	command string
	in      Input
	log     *zap.Logger
}

// ReviewBatch 审核多条命令，结果与输入一一对应。读写缓存按顺序做（SQLite 不适合并发写），
// 只有调用模型这一步并行，最多 maxParallelReviews 条同时进行。
func (s *service) ReviewBatch(ctx context.Context, ins []Input) []Result {
	results := make([]Result, len(ins))
	cfg, err := s.config()
	if err != nil {
		logger.Ctx(ctx).Error("read command review api key", zap.Error(err))
		for i := range results {
			results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: ReasonAPIKeyUnreadable})
		}
		return results
	}
	var pending []pendingReview
	for i, in := range ins {
		if cfg.APIKey == "" {
			results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: ReasonNotConfigured})
			continue
		}
		reviewed, err := commandForReview(in)
		if err != nil {
			reason := ReasonUnparseable
			if errors.Is(err, ErrUndecodable) {
				reason = ReasonUndecodable
			}
			logger.Ctx(ctx).Warn("prepare command for review", zap.String("assetType", in.AssetType), zap.String("reason", reason), zap.Error(err))
			results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: reason})
			continue
		}
		command, err := RedactSensitive(in.Syntax, reviewed)
		if err != nil {
			logger.Ctx(ctx).Warn("redact command for review", zap.String("assetType", in.AssetType), zap.Error(err))
			results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: ReasonUnparseable})
			continue
		}
		if len(command) > cfg.MaxCommandLen {
			results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: ReasonTooLong})
			continue
		}
		key := cacheKey(cfg.BaseURL, cfg.Model, in.AssetType, cacheKeyCommand(in.Command, reviewed))
		log := logger.Ctx(ctx).With(zap.String("assetType", in.AssetType), zap.String("reviewKey", key[:12]))
		if cached, err := s.cache.Get(ctx, key); err != nil {
			log.Warn("read command review cache", zap.Error(err))
		} else if cached != nil {
			// 缓存的是评分：按当前阈值重新判断，设置里改了阈值马上生效。
			r := decide(cached.Model, cached.Scores, cfg.Threshold)
			r.Cached = true
			log.Info("command review cache hit", zap.String("outcome", string(r.Outcome)))
			results[i] = r
			continue
		}
		pending = append(pending, pendingReview{idx: i, key: key, command: command, in: in, log: log})
	}

	type evaluation struct {
		resp     *typesafe.Response
		err      error
		elapsed  time.Duration
		attempts int
	}
	evals := make([]evaluation, len(pending))
	sem := make(chan struct{}, maxParallelReviews)
	var wg sync.WaitGroup
	for j, p := range pending {
		wg.Add(1)
		go func(j int, p pendingReview) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p.log.Info("command review start", zap.String("model", cfg.Model))
			start := time.Now()
			resp, attempts, err := s.evaluate(ctx, cfg, p)
			evals[j] = evaluation{resp: resp, err: err, elapsed: time.Since(start), attempts: attempts}
		}(j, p)
	}
	wg.Wait()

	for j, p := range pending {
		e := evals[j]
		if e.err != nil {
			reason := failReason(e.err)
			p.log.Warn("command review failed", zap.String("reason", reason), zap.Int("attempts", e.attempts), zap.Duration("duration", e.elapsed), zap.Error(e.err))
			results[p.idx] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: reason, Model: cfg.Model, Duration: e.elapsed, Attempts: e.attempts})
			continue
		}
		s.recordSuccess()
		scores := make(map[string]float64, len(e.resp.Answers))
		for id, a := range e.resp.Answers {
			scores[id] = a.Noul
		}
		r := decide(e.resp.Model, scores, cfg.Threshold)
		r.Duration = e.elapsed
		r.Attempts = e.attempts
		p.log.Info("command review done", zap.String("outcome", string(r.Outcome)), zap.Strings("failed", r.Failed), zap.String("model", r.Model), zap.Int("attempts", e.attempts), zap.Duration("duration", e.elapsed))
		if err := s.cache.Put(ctx, p.key, r, cacheTTL); err != nil {
			p.log.Warn("write command review cache", zap.Error(err))
		}
		results[p.idx] = r
	}
	return results
}

// evaluate 为一条命令调用模型，返回结果和调用次数。每次调用的超时由设置控制，
// 失败时按 retryable 决定是否再试，最多 maxAttempts 次。
func (s *service) evaluate(ctx context.Context, cfg Config, p pendingReview) (*typesafe.Response, int, error) {
	ev := s.evaluator(cfg.APIKey, cfg.BaseURL)
	req := typesafe.Request{
		Model:     cfg.Model,
		State:     reviewState{AssetType: p.in.AssetType, Command: p.command},
		Questions: riskQuestions,
	}
	for attempt := 1; ; attempt++ {
		reqCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		resp, err := ev.Evaluate(reqCtx, req)
		cancel()
		if err == nil || attempt == maxAttempts || !retryable(ctx, err) {
			return resp, attempt, err
		}
		p.log.Warn("command review attempt failed, retrying", zap.Int("attempt", attempt), zap.Error(err))
	}
}

// retryable 判断一次调用失败后值不值得再试：这次调用超时或网络出错时重试。服务端明确返回的
// 错误（API key 无效、请求有误等）重试也不会成功，429 / 529 已经在客户端里退避重试过；
// 调用方已经取消时也不再试。
func retryable(ctx context.Context, err error) bool {
	var apiErr *typesafe.APIError
	return ctx.Err() == nil && !errors.As(err, &apiErr)
}

func (s *service) TestModel(ctx context.Context, cfg Config) (string, error) {
	if cfg.APIKey == "" {
		return "", ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	resp, err := s.evaluator(cfg.APIKey, cfg.BaseURL).Evaluate(ctx, typesafe.Request{
		Model:     cfg.Model,
		State:     reviewState{AssetType: "ssh", Command: "ls"},
		Questions: map[string]typesafe.Question{QuestionDestructive: riskQuestions[QuestionDestructive]},
	})
	if err != nil {
		return "", err
	}
	return resp.Model, nil
}

// decide 按阈值判断：任意一题"是"的概率达到阈值即不通过。
func decide(model string, scores map[string]float64, threshold float64) Result {
	r := Result{Outcome: OutcomePass, Model: model, Scores: scores, Threshold: threshold}
	for id, p := range scores {
		if p >= threshold {
			r.Failed = append(r.Failed, id)
		}
	}
	if len(r.Failed) > 0 {
		sort.Strings(r.Failed)
		r.Outcome = OutcomeReject
	}
	return r
}

func failReason(err error) string {
	var apiErr *typesafe.APIError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ReasonTimeout
	case errors.As(err, &apiErr) && apiErr.IsAuth():
		return ReasonInvalidAPIKey
	default:
		return ReasonUnavailable
	}
}

// cacheKey 由服务地址、模型、题目版本、资产类型和命令算出。命令用原始命令而不是替换敏感信息后的：
// 替换后长得一样的两条命令（比如只有密码不同）各自审核，不共用结果。解码展开过的命令还带上
// 展开后的文本，避免展开前的评分被当成展开后的结果。表里只存哈希，不存命令。
func cacheKey(baseURL, model, assetType, command string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{baseURL, model, questionsVersion, assetType, command}, "\x00")))
	return hex.EncodeToString(h[:])
}
