// Package command_review_svc 是命令的模型审核：命令没被规则放行、原本要问人时，交给审核扩展
// 判断能不能自动执行。宿主只负责和具体审核服务无关的部分——用哪个审核扩展、长度限制、缓存、
// 并发、超时重试和失败记录；判断本身由扩展做。送审的文本由权限检查给出（permission.ReviewRequest.Text：
// 原命令原样开头，解码出的脚本追加在后），这里不改写。
package command_review_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
)

// DefaultTimeout 是每次调用审核扩展的默认超时。
const DefaultTimeout = 5 * time.Second

// cacheTTL 是审核结果的缓存时长。
const cacheTTL = 7 * 24 * time.Hour

// maxParallelReviews 是同时调用审核扩展的上限，与扩展的实例池大小一致。
const maxParallelReviews = 4

// maxAttempts 是一次审核最多调用的次数：超时时再试一次。opsctl 每次都是新进程，
// 审核扩展第一次请求要重新解析域名、建立连接，偶尔会超时；第二次通常很快。
const maxAttempts = 2

// Outcome 是审核结果，和审批、审计里记的是同一个类型。
type Outcome = aictx.ReviewOutcome

const (
	OutcomePass   = aictx.ReviewPass   // 审核通过
	OutcomeReject = aictx.ReviewReject // 审核未通过
	OutcomeFail   = aictx.ReviewFail   // 审核失败：没能得到判断
)

var (
	// ErrNoReviewer 见 aictx.ReviewReasonNoReviewer。
	ErrNoReviewer = errors.New("no command reviewer extension is available")
	// ErrReviewerUnavailable 见 aictx.ReviewReasonReviewerUnavailable。
	ErrReviewerUnavailable = errors.New("command reviewer is unavailable")
)

// isConfigError 判断审核失败是不是设置有问题：只有用户改设置才能恢复，要提醒用户。
func isConfigError(reason string) bool {
	switch reason {
	case aictx.ReviewReasonNotConfigured, aictx.ReviewReasonInvalidAPIKey, aictx.ReviewReasonNoReviewer, aictx.ReviewReasonReviewerUnavailable:
		return true
	}
	return false
}

// Result 是一次审核的结果。
type Result struct {
	Outcome  Outcome            `json:"outcome"`
	Reason   string             `json:"reason,omitempty"` // 审核失败的原因
	Failed   []string           `json:"failed,omitempty"` // 审核未通过时，没满足条件的题目
	Scores   map[string]float64 `json:"scores,omitempty"` // 未通过的题目的评分
	Duration time.Duration      `json:"duration,omitempty"`
	Cached   bool               `json:"cached,omitempty"`
	Attempts int                `json:"attempts,omitempty"` // 调用审核扩展的次数，超时时会重试
	// Extension / Version 是做出这次判断的审核扩展。
	Extension string `json:"extension,omitempty"`
	Version   string `json:"version,omitempty"`
	// FailedNames 是审核扩展为 Failed 里每道题声明的各语言名称（题目 id → 语言 → 名称）。
	FailedNames map[string]map[string]string `json:"failed_names,omitempty"`
}

// Info 是这次审核在审批和审计里记的样子；mode 是触发审核的权限模式。
func (r Result) Info(mode string) *aictx.ReviewInfo {
	return &aictx.ReviewInfo{
		Mode:        mode,
		Outcome:     r.Outcome,
		Reason:      r.Reason,
		Failed:      r.Failed,
		Scores:      r.Scores,
		DurationMs:  r.Duration.Milliseconds(),
		Cached:      r.Cached,
		Attempts:    r.Attempts,
		Extension:   r.Extension,
		Version:     r.Version,
		FailedNames: r.FailedNames,
	}
}

// Cache 保存审核结果。Get 找不到时返回 (nil, nil)。
type Cache interface {
	Get(ctx context.Context, key string) (*Result, error)
	Put(ctx context.Context, key string, r Result, ttl time.Duration) error
}

// Service 审核命令：实现权限检查的审核端口（permission.Reviewer），结果与请求一一对应。
type Service interface {
	permission.Reviewer
	// TestReviewer 用 rev 审一条无害的命令，不读写缓存：扩展设置页据此确认这个审核器能不能用。
	TestReviewer(ctx context.Context, rev Reviewer) Result
	// Status 返回本进程启动以来最近一次还没恢复的审核失败，没失败过或之后已有一次成功则为空。
	Status() Status
	// SetConfigErrorListener 注册设置问题（没有审核器、审核器没配置好等）的通知，
	// 同一种问题只通知一次，审核恢复成功后再出错会重新通知。
	SetConfigErrorListener(fn func(reason string))
	// SetStatusListener 注册 Status 变化（出现失败或失败已恢复）的通知，设置页打开着时据此实时更新。
	// 一批审核结果最多通知一次，状态没变不通知。
	SetStatusListener(fn func(Status))
}

// Status 是最近一次还没恢复的审核失败，设置页据此提示；之后有一次审核成功就为空。
type Status struct {
	LastFailReason string    `json:"lastFailReason,omitempty"`
	LastFailAt     time.Time `json:"lastFailAt,omitempty"`
}

type service struct {
	timeout func() time.Duration
	cache   Cache
	source  ReviewerSource

	mu       sync.Mutex
	status   Status
	listener func(reason string)
	onStatus func(Status)
	notified map[string]bool

	// statusMu 让状态通知串行：每次在锁内读当前状态再发，几批结果同时结束时最后发出的就是当前状态。
	statusMu sync.Mutex
	sent     Status // 上一次发给 onStatus 的状态
}

// New 创建审核服务。timeout 是每次调用审核器的超时，每次审核时读取，设置修改后立即生效；
// source 给出每次审核时要用的审核器。
func New(timeout func() time.Duration, cache Cache, source ReviewerSource) Service {
	return &service{timeout: timeout, cache: cache, source: source, notified: map[string]bool{}}
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

func (s *service) SetStatusListener(fn func(Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onStatus = fn
}

// record 按一次正式审核的结果更新状态：失败记下（设置问题通知一次）；扩展真的给出了
// 判断就算恢复——只是调用成功、回答却是"API key 无效"之类不算，命中缓存也不算。
// 测试审核扩展不走这里：测的未必是在用的那个。
func (s *service) record(results []Result) {
	for _, r := range results {
		switch {
		case r.Outcome == OutcomeFail:
			s.recordFailure(r)
		case !r.Cached:
			s.recordSuccess()
		}
	}
	s.notifyStatus()
}

// notifyStatus 把当前状态发给 onStatus，与上一次发出的相同则不发。
func (s *service) notifyStatus() {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.mu.Lock()
	cur, notify := s.status, s.onStatus
	s.mu.Unlock()
	if notify == nil || cur == s.sent {
		return
	}
	s.sent = cur
	notify(cur)
}

// recordFailure 记下审核失败；设置问题通知一次。
func (s *service) recordFailure(r Result) {
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
}

// recordSuccess 清掉最近一次失败：设置页只显示还没恢复的失败。
// 同时让同一种设置问题之后再出现时重新通知。
func (s *service) recordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = Status{}
	s.notified = map[string]bool{}
}

var defaultService Service

// Register 注册默认审核服务。
func Register(s Service) { defaultService = s }

// Default 返回默认审核服务，没有注册时为 nil。
func Default() Service { return defaultService }

// ReviewCommands 审核多条命令，结果与请求一一对应（见 permission.Reviewer）。
func (s *service) ReviewCommands(ctx context.Context, reqs []permission.ReviewRequest) []aictx.ReviewInfo {
	rev, results := s.resolve(ctx, len(reqs))
	if results == nil {
		results = s.reviewWith(ctx, rev, reqs, true)
	}
	s.record(results)
	out := make([]aictx.ReviewInfo, len(results))
	for i, r := range results {
		out[i] = *r.Info("")
	}
	return out
}

// testCommand 是测试审核器时送审的命令：只读、无害，正常的审核器应当判它通过。
var testCommand = permission.ReviewRequest{AssetType: "ssh", Command: "ls -la /tmp", Syntax: permission.SyntaxShell}

func (s *service) TestReviewer(ctx context.Context, rev Reviewer) Result {
	return s.reviewWith(ctx, rev, []permission.ReviewRequest{testCommand}, false)[0]
}

// resolve 向审核器来源要这次用的审核器；拿不到时返回 n 条同样的失败结果。
func (s *service) resolve(ctx context.Context, n int) (Reviewer, []Result) {
	rev, err := s.source.Current(ctx)
	if err == nil {
		return rev, nil
	}
	reason := aictx.ReviewReasonReviewerUnavailable
	if errors.Is(err, ErrNoReviewer) {
		reason = aictx.ReviewReasonNoReviewer
	}
	logger.Ctx(ctx).Warn("command reviewer unavailable", zap.String("reason", reason), zap.Error(err))
	results := make([]Result, n)
	for i := range results {
		results[i] = Result{Outcome: OutcomeFail, Reason: reason}
	}
	return nil, results
}

// pendingReview 是一条要调用审核器的命令。
type pendingReview struct {
	idx       int
	assetType string
	command   string // 送审文本（permission.ReviewRequest.Text）
	syntax    string
	key       string
}

// reviewWith 用 rev 审核 reqs。送审文本（原命令加追加的解码脚本）超过审核器声明的长度时
// 不送审，按该条失败；其余每条调用一次审核器。读写缓存按顺序做（SQLite 不适合并发写），
// 只有调用审核器这一步并行，最多 maxParallelReviews 路同时进行。
// useCache 为假时不读也不写缓存（测试审核器用）。
func (s *service) reviewWith(ctx context.Context, rev Reviewer, reqs []permission.ReviewRequest, useCache bool) []Result {
	name, version, settingsHash := rev.Identity()
	maxLen := rev.MaxCommandLen()
	questions := rev.Questions()
	results := make([]Result, len(reqs))
	var pending []pendingReview
	for i, req := range reqs {
		command := req.Text()
		if len(command) > maxLen {
			results[i] = Result{Outcome: OutcomeFail, Reason: aictx.ReviewReasonTooLong, Extension: name, Version: version}
			continue
		}
		key := extensionCacheKey(name, version, settingsHash, req.AssetType, command)
		if useCache {
			if cached, err := s.cache.Get(ctx, key); err != nil {
				logger.Ctx(ctx).Warn("read command review cache", zap.String("extension", name), zap.Error(err))
			} else if cached != nil {
				cached.Cached = true
				cached.Extension = name
				cached.Version = version
				results[i] = *cached
				continue
			}
		}
		pending = append(pending, pendingReview{idx: i, assetType: req.AssetType, command: command, syntax: string(req.Syntax), key: key})
	}

	calls := s.callAll(ctx, rev, name, pending)

	for j, p := range pending {
		c := calls[j]
		err := c.err
		if err == nil {
			err = validateAnswer(questions, c.answer)
		}
		if err != nil {
			reason := aictx.ReviewReasonInvalidResult
			if errors.Is(err, context.DeadlineExceeded) {
				reason = aictx.ReviewReasonTimeout
			}
			logger.Ctx(ctx).Warn("command review failed", zap.String("extension", name), zap.String("reason", reason), zap.Error(err))
			results[p.idx] = Result{Outcome: OutcomeFail, Reason: reason, Extension: name, Version: version, Duration: c.elapsed, Attempts: c.attempts}
			continue
		}
		r := extensionResult(name, version, questions, c.answer, c.elapsed, c.attempts)
		results[p.idx] = r
		if r.Outcome == OutcomeFail || !useCache {
			continue
		}
		if err := s.cache.Put(ctx, p.key, r, cacheTTL); err != nil {
			logger.Ctx(ctx).Warn("write command review cache", zap.String("extension", name), zap.Error(err))
		}
	}
	return results
}

// reviewCall 是对一条命令调用审核器的结果。
type reviewCall struct {
	answer   Answer
	err      error
	elapsed  time.Duration
	attempts int
}

// callAll 对 pending 里每条命令调用一次审核器，最多 maxParallelReviews 路同时进行（扩展的
// 实例池大小）；每次调用单独计时，超时重试一次。结果与 pending 一一对应。
func (s *service) callAll(ctx context.Context, rev Reviewer, name string, pending []pendingReview) []reviewCall {
	timeout := s.timeout()
	calls := make([]reviewCall, len(pending))
	sem := make(chan struct{}, maxParallelReviews)
	var wg sync.WaitGroup
	for j, p := range pending {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			startAt := time.Now()
			for attempt := 1; ; attempt++ {
				reqCtx, cancel := context.WithTimeout(ctx, timeout)
				answer, err := rev.Review(reqCtx, p.assetType, p.command, p.syntax)
				cancel()
				timedOut := errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil
				if !timedOut || attempt == maxAttempts {
					calls[j] = reviewCall{answer: answer, err: err, elapsed: time.Since(startAt), attempts: attempt}
					return
				}
				logger.Ctx(ctx).Warn("command review timed out, retrying", zap.String("extension", name), zap.Int("attempt", attempt))
			}
		}()
	}
	wg.Wait()
	return calls
}

// extensionResult 把审核扩展的回答落成宿主的结果：error 是"没能得到判断"，按审核失败处理。
func extensionResult(name, version string, questions []Question, r Answer, elapsed time.Duration, attempts int) Result {
	out := Result{
		Outcome:   Outcome(r.Outcome),
		Reason:    r.Reason,
		Failed:    r.Failed,
		Extension: name,
		Version:   version,
		Duration:  elapsed,
		Attempts:  attempts,
	}
	if r.Outcome == AnswerError {
		out.Outcome = OutcomeFail
	}
	if len(r.Failed) > 0 {
		names := make(map[string]map[string]string, len(questions))
		for _, q := range questions {
			names[q.ID] = q.Names
		}
		out.FailedNames = make(map[string]map[string]string, len(r.Failed))
		for _, id := range r.Failed {
			out.FailedNames[id] = names[id]
		}
	}
	if len(r.Scores) > 0 {
		out.Scores = r.Scores
	}
	return out
}

// extensionCacheKey 由扩展名、版本、设置哈希、资产类型和送审文本算出：换了扩展、版本或设置都
// 不命中旧结果，审核器看到的文本不同也不命中。表里只存哈希，不存命令。
func extensionCacheKey(name, version, settingsHash, assetType, command string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{name, version, settingsHash, assetType, command}, "\x00")))
	return hex.EncodeToString(h[:])
}
