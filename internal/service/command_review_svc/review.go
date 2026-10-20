// Package command_review_svc 是命令的模型审核：命令没被规则放行、原本要问人时，交给审核扩展
// 判断能不能自动执行。宿主只负责和具体审核服务无关的部分——用哪个审核扩展、送审前的准备
// （展开解码后执行的命令、替换敏感信息）、缓存、并发、超时重试和失败记录；判断本身由扩展做。
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
	"github.com/opskat/opskat/pkg/extension"
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

// 审核失败的原因。not_configured / invalid_api_key / timeout / unavailable 也是审核扩展
// 回答 error 时用的原因名，宿主按同一套说明显示。
const (
	ReasonNotConfigured = "not_configured"
	ReasonInvalidAPIKey = "invalid_api_key"
	ReasonTimeout       = "timeout"
	ReasonUnavailable   = "unavailable"
	ReasonTooLong       = "too_long"
	ReasonUnparseable   = "unparseable" // 命令解析不了，没法替换敏感信息，不发送
	ReasonUndecodable   = "undecodable" // 认得出解码后执行，但解不开要执行的内容，不发送
	// ReasonInvalidResult 是审核扩展返回了不合规的整批结果。
	ReasonInvalidResult = "invalid_result"
	// ReasonNoReviewer 是没有可用的审核扩展：一个都没装（或都停用了），或装了多个却没选用哪个。
	ReasonNoReviewer = "no_reviewer"
	// ReasonReviewerUnavailable 是要用的审核扩展这次不可用：没加载、不是审核器，或来源/摘要检查没过。
	ReasonReviewerUnavailable = "reviewer_unavailable"
)

var (
	// ErrNoReviewer 见 ReasonNoReviewer。
	ErrNoReviewer = errors.New("no command reviewer extension is available")
	// ErrReviewerUnavailable 见 ReasonReviewerUnavailable。
	ErrReviewerUnavailable = errors.New("command reviewer is unavailable")
)

// isConfigError 判断审核失败是不是设置有问题：只有用户改设置才能恢复，要提醒用户。
func isConfigError(reason string) bool {
	switch reason {
	case ReasonNotConfigured, ReasonInvalidAPIKey, ReasonNoReviewer, ReasonReviewerUnavailable:
		return true
	}
	return false
}

// Input 是一次审核的输入。Syntax 决定怎样找出命令里的敏感信息（见 RedactSensitive）。
type Input struct {
	AssetType string
	Command   string
	Syntax    Syntax
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

// ExtensionReviewer 是要用的审核扩展。
type ExtensionReviewer interface {
	// Identity 是扩展名、版本和设置哈希。设置哈希参与缓存键。
	Identity() (name, version, settingsHash string)
	// Limits 是 describe() 声明的单批条数和单条命令最大长度。
	Limits() (maxItems, maxCommandLen int)
	// Questions 是 describe() 声明的题目，结果里只回题目 id，宿主按这里的名称显示。
	Questions() []extension.ReviewQuestion
	Review(ctx context.Context, items []extension.ReviewItem) ([]extension.ReviewCommandResult, error)
}

// ReviewerFunc 找出这次要用的审核扩展。没有可用的返回 ErrNoReviewer，
// 要用的这次不可用返回 ErrReviewerUnavailable。
type ReviewerFunc func(ctx context.Context) (ExtensionReviewer, error)

// Cache 保存审核结果。Get 找不到时返回 (nil, nil)。
type Cache interface {
	Get(ctx context.Context, key string) (*Result, error)
	Put(ctx context.Context, key string, r Result, ttl time.Duration) error
}

// Service 审核命令。
type Service interface {
	Review(ctx context.Context, in Input) Result
	// ReviewBatch 审核多条命令，结果与输入一一对应；调用审核扩展这一步并行。
	ReviewBatch(ctx context.Context, ins []Input) []Result
	// TestReviewer 用 rev 审一条无害的命令，不读写缓存：扩展设置页据此确认这个审核扩展能不能用。
	TestReviewer(ctx context.Context, rev ExtensionReviewer) Result
	// Status 返回本进程启动以来最近一次还没恢复的审核失败，没失败过或之后已有一次成功则为空。
	Status() Status
	// SetConfigErrorListener 注册设置问题（没有审核扩展、扩展没配置好等）的通知，
	// 同一种问题只通知一次，审核恢复成功后再出错会重新通知。
	SetConfigErrorListener(fn func(reason string))
}

// Status 是最近一次还没恢复的审核失败，设置页据此提示；之后有一次审核成功就为空。
type Status struct {
	LastFailReason string    `json:"lastFailReason,omitempty"`
	LastFailAt     time.Time `json:"lastFailAt,omitempty"`
}

type service struct {
	timeout  func() time.Duration
	cache    Cache
	reviewer ReviewerFunc

	mu       sync.Mutex
	status   Status
	listener func(reason string)
	notified map[string]bool
}

// New 创建审核服务。timeout 是每次调用审核扩展的超时，每次审核时读取，设置修改后立即生效。
func New(timeout func() time.Duration, cache Cache, reviewer ReviewerFunc) Service {
	return &service{timeout: timeout, cache: cache, reviewer: reviewer, notified: map[string]bool{}}
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

// recordFailure 记下审核失败；设置问题通知一次。
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

func (s *service) Review(ctx context.Context, in Input) Result {
	return s.ReviewBatch(ctx, []Input{in})[0]
}

// ReviewBatch 审核多条命令，结果与输入一一对应。读写缓存按顺序做（SQLite 不适合并发写），
// 只有调用审核扩展这一步并行，最多 maxParallelReviews 路同时进行。
func (s *service) ReviewBatch(ctx context.Context, ins []Input) []Result {
	rev, failed := s.resolve(ctx, len(ins))
	if failed != nil {
		return failed
	}
	return s.reviewWith(ctx, rev, ins, true)
}

// testCommand 是测试审核服务时送审的命令：只读、无害，正常的审核服务应当判它通过。
var testCommand = Input{AssetType: "ssh", Command: "ls -la /tmp", Syntax: SyntaxShell}

func (s *service) TestReviewer(ctx context.Context, rev ExtensionReviewer) Result {
	return s.reviewWith(ctx, rev, []Input{testCommand}, false)[0]
}

// resolve 找出要用的审核扩展；找不到时返回 n 条同样的失败结果。
func (s *service) resolve(ctx context.Context, n int) (ExtensionReviewer, []Result) {
	rev, err := s.reviewer(ctx)
	if err == nil {
		return rev, nil
	}
	reason := ReasonReviewerUnavailable
	if errors.Is(err, ErrNoReviewer) {
		reason = ReasonNoReviewer
	}
	logger.Ctx(ctx).Warn("command reviewer unavailable", zap.String("reason", reason), zap.Error(err))
	results := make([]Result, n)
	for i := range results {
		results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: reason})
	}
	return nil, results
}

// reviewWith 用审核扩展审核一批命令。先展开解码后执行的命令、再替换敏感信息；超长命令不送审，
// 按该条失败；其余按 maxItems 拆成多次 review_commands。一次调用失败则该批全部失败。
// useCache 为假时不读也不写缓存（测试审核服务用）。
func (s *service) reviewWith(ctx context.Context, rev ExtensionReviewer, ins []Input, useCache bool) []Result {
	timeout := s.timeout()
	name, version, settingsHash := rev.Identity()
	maxItems, maxLen := rev.Limits()
	questions := rev.Questions()
	results := make([]Result, len(ins))
	type pendingExt struct {
		idx  int
		item extension.ReviewItem
		key  string
	}
	var pending []pendingExt
	for i, in := range ins {
		reviewed, err := commandForReview(in)
		if err != nil {
			reason := ReasonUnparseable
			if errors.Is(err, ErrUndecodable) {
				reason = ReasonUndecodable
			}
			logger.Ctx(ctx).Warn("prepare command for review", zap.String("assetType", in.AssetType), zap.String("reason", reason), zap.Error(err))
			results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: reason, Extension: name, Version: version})
			continue
		}
		command, err := RedactSensitive(in.Syntax, reviewed)
		if err != nil {
			logger.Ctx(ctx).Warn("redact command for review", zap.String("assetType", in.AssetType), zap.Error(err))
			results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: ReasonUnparseable, Extension: name, Version: version})
			continue
		}
		if len(command) > maxLen {
			results[i] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: ReasonTooLong, Extension: name, Version: version})
			continue
		}
		key := extensionCacheKey(name, version, settingsHash, in.AssetType, cacheKeyCommand(in.Command, reviewed))
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
		pending = append(pending, pendingExt{idx: i, key: key, item: extension.ReviewItem{AssetType: in.AssetType, Command: command, Syntax: string(in.Syntax)}})
	}

	var batches [][]pendingExt
	for start := 0; start < len(pending); start += maxItems {
		batches = append(batches, pending[start:min(start+maxItems, len(pending))])
	}

	// 和内置审核一样：只有调用这一步并行（扩展的实例池最多 maxParallelReviews 个），
	// 每次调用单独计时，超时重试一次；读写缓存按顺序做。
	type call struct {
		out      []extension.ReviewCommandResult
		err      error
		elapsed  time.Duration
		attempts int
	}
	calls := make([]call, len(batches))
	sem := make(chan struct{}, maxParallelReviews)
	var wg sync.WaitGroup
	for j, batch := range batches {
		wg.Add(1)
		go func(j int, batch []pendingExt) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			items := make([]extension.ReviewItem, len(batch))
			for i, p := range batch {
				items[i] = p.item
			}
			startAt := time.Now()
			for attempt := 1; ; attempt++ {
				reqCtx, cancel := context.WithTimeout(ctx, timeout)
				out, err := rev.Review(reqCtx, items)
				cancel()
				timedOut := errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil
				if !timedOut || attempt == maxAttempts {
					calls[j] = call{out: out, err: err, elapsed: time.Since(startAt), attempts: attempt}
					return
				}
				logger.Ctx(ctx).Warn("extension command review timed out, retrying", zap.String("extension", name), zap.Int("attempt", attempt))
			}
		}(j, batch)
	}
	wg.Wait()

	for j, batch := range batches {
		c := calls[j]
		if c.err != nil || len(c.out) != len(batch) {
			reason := ReasonInvalidResult
			if errors.Is(c.err, context.DeadlineExceeded) {
				reason = ReasonTimeout
			}
			logger.Ctx(ctx).Warn("extension command review failed", zap.String("extension", name), zap.String("reason", reason), zap.Error(c.err))
			for _, p := range batch {
				results[p.idx] = s.recordFailure(Result{Outcome: OutcomeFail, Reason: reason, Extension: name, Version: version, Duration: c.elapsed, Attempts: c.attempts})
			}
			continue
		}
		for i, p := range batch {
			r := extensionResult(name, version, questions, c.out[i], c.elapsed, c.attempts)
			if r.Outcome == OutcomeFail {
				results[p.idx] = s.recordFailure(r)
				continue
			}
			// 扩展真的给出了判断才算恢复；只是调用成功、回答却是"API key 无效"之类不算。
			s.recordSuccess()
			if !useCache {
				results[p.idx] = r
				continue
			}
			if err := s.cache.Put(ctx, p.key, r, cacheTTL); err != nil {
				logger.Ctx(ctx).Warn("write command review cache", zap.String("extension", name), zap.Error(err))
			}
			results[p.idx] = r
		}
	}
	return results
}

// extensionResult 把审核扩展的回答落成宿主的结果：error 是"没能得到判断"，按审核失败处理。
func extensionResult(name, version string, questions []extension.ReviewQuestion, r extension.ReviewCommandResult, elapsed time.Duration, attempts int) Result {
	out := Result{
		Outcome:   Outcome(r.Outcome),
		Reason:    r.Reason,
		Failed:    r.Failed,
		Extension: name,
		Version:   version,
		Duration:  elapsed,
		Attempts:  attempts,
	}
	if r.Outcome == extension.ReviewOutcomeError {
		out.Outcome = OutcomeFail
	}
	if len(r.Failed) > 0 {
		names := make(map[string]map[string]string, len(questions))
		for _, q := range questions {
			names[q.ID] = q.I18n.Name
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

// extensionCacheKey 由扩展名、版本、设置哈希、资产类型和命令算出：换了扩展、版本或设置都不命中
// 旧结果。命令用原始命令而不是替换敏感信息后的——替换后长得一样的两条命令（比如只有密码不同）
// 各自审核；解码展开过的再带上展开后的文本（cacheKeyCommand）。表里只存哈希，不存命令。
func extensionCacheKey(name, version, settingsHash, assetType, command string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{name, version, settingsHash, assetType, command}, "\x00")))
	return hex.EncodeToString(h[:])
}
