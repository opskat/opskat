package command_review_svc

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/pkg/typesafe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEvaluator 记录收到的请求，并按题目 ID 返回预设的"是"概率。
type fakeEvaluator struct {
	nouls map[string]float64
	err   error
	delay time.Duration
	calls int
	last  typesafe.Request
}

func (f *fakeEvaluator) Evaluate(ctx context.Context, req typesafe.Request) (*typesafe.Response, error) {
	f.calls++
	f.last = req
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	answers := map[string]typesafe.Answer{}
	for id := range req.Questions {
		answers[id] = typesafe.Answer{Type: typesafe.QuestionNoul, Noul: f.nouls[id]}
	}
	return &typesafe.Response{Model: "jev-1.13.0", Answers: answers}, nil
}

// memCache 是内存版缓存。
type memCache struct{ m map[string]Result }

func (c *memCache) Get(_ context.Context, key string) (*Result, error) {
	r, ok := c.m[key]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (c *memCache) Put(_ context.Context, key string, r Result, _ time.Duration) error {
	c.m[key] = r
	return nil
}

func newTestService(ev *fakeEvaluator, cfg Config) (*service, *memCache) {
	cache := &memCache{m: map[string]Result{}}
	s := New(func() (Config, error) { return cfg, nil }, cache, func(string, string) Evaluator { return ev }).(*service)
	return s, cache
}

func enabledConfig() Config {
	return Config{APIKey: "k", BaseURL: "https://api.typesafe.ai", Model: "jev-1.13.0", Threshold: 0.2, Timeout: time.Second, MaxCommandLen: 4000}
}

func TestReviewPassesWhenEveryRiskIsBelowThreshold(t *testing.T) {
	ev := &fakeEvaluator{nouls: map[string]float64{QuestionDestructive: 0.01, QuestionDisruptive: 0.05, QuestionRemoteCode: 0.0}}
	s, _ := newTestService(ev, enabledConfig())

	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "mkdir -p /data/app"})

	assert.Equal(t, OutcomePass, r.Outcome)
	assert.Equal(t, "jev-1.13.0", r.Model)
	assert.Len(t, r.Scores, 3)
}

func TestReviewRejectsWhenAnyRiskReachesThreshold(t *testing.T) {
	ev := &fakeEvaluator{nouls: map[string]float64{QuestionDestructive: 0.97}}
	s, _ := newTestService(ev, enabledConfig())

	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "rm -rf /data"})

	assert.Equal(t, OutcomeReject, r.Outcome)
	assert.Equal(t, []string{QuestionDestructive}, r.Failed)
	// 审计里要同时看到评分和当时的阈值，阈值以后改了也看得懂当初为什么没通过
	assert.Equal(t, 0.2, r.Threshold)
}

func TestReviewSendsRedactedCommandWithAssetType(t *testing.T) {
	ev := &fakeEvaluator{}
	s, _ := newTestService(ev, enabledConfig())

	s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: "AUTH x; mysql -uroot -pS3cret -e 'DROP DATABASE shop'"})

	state, ok := ev.last.State.(reviewState)
	require.True(t, ok)
	assert.Equal(t, "ssh", state.AssetType)
	// 只换密码本身，要执行的命令原样发给模型
	assert.Equal(t, "AUTH x; mysql -uroot -p*** -e 'DROP DATABASE shop'", state.Command)
	assert.Equal(t, "jev-1.13.0", ev.last.Model)
}

// 已保存的 API key 读不出来（比如解不开）：审核失败，按配置错误提醒，不调用模型。
func TestUnreadableAPIKeyIsAConfigError(t *testing.T) {
	ev := &fakeEvaluator{}
	s := New(func() (Config, error) { return Config{}, errors.New("decrypt: cipher: message authentication failed") },
		&memCache{m: map[string]Result{}}, func(string, string) Evaluator { return ev })
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})

	assert.Equal(t, OutcomeFail, r.Outcome)
	assert.Equal(t, ReasonAPIKeyUnreadable, r.Reason)
	assert.Zero(t, ev.calls)
	assert.Equal(t, []string{ReasonAPIKeyUnreadable}, notified)
	assert.Equal(t, ReasonAPIKeyUnreadable, s.Status().LastFailReason)
}

func TestReviewFailsWithoutCallingModel(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		cmd    string
		reason string
	}{
		{"not configured", Config{}, "ls", ReasonNotConfigured},
		{"too long", enabledConfig(), strings.Repeat("a", 4001), ReasonTooLong},
		{"unparseable", enabledConfig(), "echo 'unterminated", ReasonUnparseable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := &fakeEvaluator{}
			s, _ := newTestService(ev, c.cfg)
			r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: c.cmd})
			assert.Equal(t, OutcomeFail, r.Outcome)
			assert.Equal(t, c.reason, r.Reason)
			assert.Zero(t, ev.calls)
		})
	}
}

func TestReviewFailsOnModelErrors(t *testing.T) {
	cases := []struct {
		name   string
		ev     *fakeEvaluator
		reason string
	}{
		{"timeout", &fakeEvaluator{delay: time.Second}, ReasonTimeout},
		{"auth", &fakeEvaluator{err: &typesafe.APIError{StatusCode: http.StatusUnauthorized}}, ReasonInvalidAPIKey},
		{"other", &fakeEvaluator{err: errors.New("connection refused")}, ReasonUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := enabledConfig()
			cfg.Timeout = 20 * time.Millisecond
			s, _ := newTestService(c.ev, cfg)
			r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
			assert.Equal(t, OutcomeFail, r.Outcome)
			assert.Equal(t, c.reason, r.Reason)
		})
	}
}

func TestReviewUsesCacheForSameCommandAndSkipsCachingFailures(t *testing.T) {
	ev := &fakeEvaluator{nouls: map[string]float64{QuestionDisruptive: 0.9}}
	s, cache := newTestService(ev, enabledConfig())

	first := s.Review(context.Background(), Input{AssetType: "ssh", Command: "systemctl stop nginx"})
	second := s.Review(context.Background(), Input{AssetType: "ssh", Command: "systemctl stop nginx"})
	assert.Equal(t, 1, ev.calls)
	assert.Equal(t, first.Outcome, second.Outcome)
	assert.True(t, second.Cached)
	assert.Len(t, cache.m, 1)

	failing := &fakeEvaluator{err: errors.New("boom")}
	s2, cache2 := newTestService(failing, enabledConfig())
	s2.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Empty(t, cache2.m)
}

// 缓存按原始命令区分：替换后长得一样的两条命令（这里只有密码不同）不共用审核结果。
func TestCacheDoesNotMixCommandsThatOnlyDifferInSecrets(t *testing.T) {
	ev := &fakeEvaluator{}
	s, cache := newTestService(ev, enabledConfig())

	s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: "mysql -pA -e 'select 1'"})
	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: "mysql -pB -e 'select 1'"})

	assert.Equal(t, 2, ev.calls)
	assert.False(t, r.Cached)
	assert.Len(t, cache.m, 2)
}

// 缓存的是评分，不是结论：在设置里调了阈值，已经审过的命令马上按新阈值判断。
func TestCachedScoresAreJudgedWithCurrentThreshold(t *testing.T) {
	ev := &fakeEvaluator{nouls: map[string]float64{QuestionDisruptive: 0.3}}
	cfg := enabledConfig()
	s := New(func() (Config, error) { return cfg, nil }, &memCache{m: map[string]Result{}}, func(string, string) Evaluator { return ev })
	in := Input{AssetType: "ssh", Command: "systemctl list-timers"}

	first := s.Review(context.Background(), in)
	assert.Equal(t, OutcomeReject, first.Outcome)

	cfg.Threshold = 0.5
	second := s.Review(context.Background(), in)
	assert.Equal(t, 1, ev.calls)
	assert.True(t, second.Cached)
	assert.Equal(t, OutcomePass, second.Outcome)
	assert.Empty(t, second.Failed)
	assert.Equal(t, 0.5, second.Threshold)
	assert.Equal(t, first.Scores, second.Scores)
}

// 同名模型换了服务地址，评分不一定一样，缓存要分开。
func TestCacheKeyDependsOnServiceModelAssetTypeAndCommand(t *testing.T) {
	const api = "https://api.typesafe.ai"
	base := cacheKey(api, "jev-1.13.0", "ssh", "ls")
	assert.NotEqual(t, base, cacheKey("http://10.0.0.5:8080", "jev-1.13.0", "ssh", "ls"))
	assert.NotEqual(t, base, cacheKey(api, "jev-1.14.0", "ssh", "ls"))
	assert.NotEqual(t, base, cacheKey(api, "jev-1.13.0", "redis", "ls"))
	assert.NotEqual(t, base, cacheKey(api, "jev-1.13.0", "ssh", "ls -la"))
	assert.Equal(t, base, cacheKey(api, "jev-1.13.0", "ssh", "ls"))
}

func TestNewConfigFillsDefaults(t *testing.T) {
	cfg := NewConfig(Settings{APIKey: "k"})
	assert.Equal(t, Config{APIKey: "k", BaseURL: DefaultBaseURL, Model: DefaultModel, Threshold: DefaultThreshold, Timeout: DefaultTimeout, MaxCommandLen: MaxCommandLen}, cfg)

	cfg = NewConfig(Settings{APIKey: "k", BaseURL: "http://10.0.0.5:8080", Model: "my-jev", TimeoutMs: 3000, Threshold: 0.5})
	assert.Equal(t, "http://10.0.0.5:8080", cfg.BaseURL)
	assert.Equal(t, "my-jev", cfg.Model)
	assert.Equal(t, 3*time.Second, cfg.Timeout)
	assert.InDelta(t, 0.5, cfg.Threshold, 1e-9)
}

// 测试模型用设置页上还没保存的值：发到填的地址、用填的模型名，返回服务端实际作答的版本。
func TestTestModelUsesGivenConfig(t *testing.T) {
	ev := &fakeEvaluator{}
	var gotKey, gotURL string
	saved := enabledConfig()
	s := New(func() (Config, error) { return saved, nil }, &memCache{m: map[string]Result{}}, func(key, baseURL string) Evaluator {
		gotKey, gotURL = key, baseURL
		return ev
	})

	trial := NewConfig(Settings{APIKey: "new-key", BaseURL: "http://10.0.0.5:8080", Model: "jev-latest", TimeoutMs: 3000, Threshold: 0.2})
	model, err := s.TestModel(context.Background(), trial)

	assert.NoError(t, err)
	assert.Equal(t, "jev-1.13.0", model) // fakeEvaluator 作答的版本
	assert.Equal(t, "new-key", gotKey)
	assert.Equal(t, "http://10.0.0.5:8080", gotURL)
	assert.Equal(t, "jev-latest", ev.last.Model)
}

func TestTestModelErrors(t *testing.T) {
	s, _ := newTestService(&fakeEvaluator{}, enabledConfig())
	_, err := s.TestModel(context.Background(), Config{})
	assert.ErrorIs(t, err, ErrNotConfigured)

	authErr := &typesafe.APIError{StatusCode: http.StatusUnauthorized}
	s, _ = newTestService(&fakeEvaluator{err: authErr}, enabledConfig())
	_, err = s.TestModel(context.Background(), enabledConfig())
	assert.ErrorIs(t, err, authErr)
}

// 审核请求发到设置里的服务地址。
func TestReviewCallsConfiguredService(t *testing.T) {
	var gotURL string
	cfg := enabledConfig()
	cfg.BaseURL = "http://10.0.0.5:8080"
	s := New(func() (Config, error) { return cfg, nil }, &memCache{m: map[string]Result{}}, func(_, baseURL string) Evaluator {
		gotURL = baseURL
		return &fakeEvaluator{}
	})

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Equal(t, "http://10.0.0.5:8080", gotURL)
}

func TestConfigErrorsNotifyOnceAndShowInStatus(t *testing.T) {
	ev := &fakeEvaluator{err: &typesafe.APIError{StatusCode: http.StatusUnauthorized}}
	s, _ := newTestService(ev, enabledConfig())
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	s.Review(context.Background(), Input{AssetType: "ssh", Command: "pwd"})
	assert.Equal(t, []string{ReasonInvalidAPIKey}, notified, "同一种配置错误只提醒一次")
	assert.Equal(t, ReasonInvalidAPIKey, s.Status().LastFailReason)
	assert.False(t, s.Status().LastFailAt.IsZero())

	ev.err = nil
	failedAt := s.Status().LastFailAt
	s.Review(context.Background(), Input{AssetType: "ssh", Command: "uptime"})
	assert.Equal(t, Status{LastFailReason: ReasonInvalidAPIKey, LastFailAt: failedAt}, s.Status(),
		"设置页显示本次启动以来最近一次审核失败，之后审核成功也保留")

	ev.err = &typesafe.APIError{StatusCode: http.StatusUnauthorized}
	s.Review(context.Background(), Input{AssetType: "ssh", Command: "df -h"})
	assert.Equal(t, []string{ReasonInvalidAPIKey, ReasonInvalidAPIKey}, notified, "恢复之后再出错会重新提醒")
}

func TestNotConfiguredIsAConfigError(t *testing.T) {
	s, _ := newTestService(&fakeEvaluator{}, Config{})
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Equal(t, []string{ReasonNotConfigured}, notified)
}

func TestTransientFailuresShowInStatusWithoutNotifying(t *testing.T) {
	cfg := enabledConfig()
	cfg.Timeout = 20 * time.Millisecond
	s, _ := newTestService(&fakeEvaluator{delay: time.Second}, cfg)
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Empty(t, notified)
	assert.Equal(t, ReasonTimeout, s.Status().LastFailReason)
}

// slowEvaluator 每次调用耗时固定，按命令返回预设的"是"概率，用来验证并行和顺序。
type slowEvaluator struct {
	delay  time.Duration
	byCmd  map[string]float64
	mu     sync.Mutex
	active int
	peak   int
}

func (e *slowEvaluator) Evaluate(ctx context.Context, req typesafe.Request) (*typesafe.Response, error) {
	e.mu.Lock()
	e.active++
	if e.active > e.peak {
		e.peak = e.active
	}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.active--
		e.mu.Unlock()
	}()
	time.Sleep(e.delay)
	p := e.byCmd[req.State.(reviewState).Command]
	answers := map[string]typesafe.Answer{}
	for id := range req.Questions {
		answers[id] = typesafe.Answer{Type: typesafe.QuestionNoul, Noul: p}
	}
	return &typesafe.Response{Model: "jev-1.13.0", Answers: answers}, nil
}

func TestReviewBatchRunsModelCallsInParallelAndKeepsOrder(t *testing.T) {
	ev := &slowEvaluator{delay: 50 * time.Millisecond, byCmd: map[string]float64{"rm -rf /data": 0.99}}
	cache := &memCache{m: map[string]Result{}}
	s := New(func() (Config, error) { return enabledConfig(), nil }, cache, func(string, string) Evaluator { return ev }).(*service)

	ins := []Input{
		{AssetType: "ssh", Command: "ls"},
		{AssetType: "ssh", Command: "rm -rf /data"},
		{AssetType: "ssh", Command: "df -h"},
		{AssetType: "ssh", Command: "uptime"},
		{AssetType: "ssh", Command: strings.Repeat("a", 4001)},
	}
	start := time.Now()
	out := s.ReviewBatch(context.Background(), ins)
	elapsed := time.Since(start)

	assert.Len(t, out, 5)
	assert.Equal(t, OutcomePass, out[0].Outcome)
	assert.Equal(t, OutcomeReject, out[1].Outcome)
	assert.Equal(t, OutcomePass, out[2].Outcome)
	assert.Equal(t, OutcomePass, out[3].Outcome)
	assert.Equal(t, OutcomeFail, out[4].Outcome)
	assert.Equal(t, ReasonTooLong, out[4].Reason)
	assert.Greater(t, ev.peak, 1, "模型调用应当并行")
	assert.LessOrEqual(t, ev.peak, maxParallelReviews)
	assert.Less(t, elapsed, 4*50*time.Millisecond, "4 条需要调用模型的命令不应串行等待")
	assert.Len(t, cache.m, 4, "成功的结果都写进缓存")

	again := s.ReviewBatch(context.Background(), ins[:2])
	assert.True(t, again[0].Cached)
	assert.True(t, again[1].Cached)
}

// flakyEvaluator 前 fails 次调用失败（delay 大于超时时是超时，否则返回 err），之后正常作答。
type flakyEvaluator struct {
	fails int
	delay time.Duration
	err   error
	calls int
}

func (f *flakyEvaluator) Evaluate(ctx context.Context, req typesafe.Request) (*typesafe.Response, error) {
	f.calls++
	if f.calls <= f.fails {
		if f.err != nil {
			return nil, f.err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	return (&fakeEvaluator{}).Evaluate(ctx, req)
}

// opsctl 每次都是新进程，第一次请求要重新建立连接，偶尔会超时。超时或连接出错时再试一次，
// 第二次通常很快；服务端明确返回的错误（如 API key 无效）重试也没用，不重试。
func TestReviewRetriesOnceOnTimeoutOrNetworkError(t *testing.T) {
	cases := []struct {
		name    string
		ev      *flakyEvaluator
		outcome Outcome
		reason  string
		calls   int
	}{
		{"timeout then answered", &flakyEvaluator{fails: 1, delay: time.Second}, OutcomePass, "", 2},
		{"network error then answered", &flakyEvaluator{fails: 1, err: errors.New("read tcp: connection reset by peer")}, OutcomePass, "", 2},
		{"times out twice", &flakyEvaluator{fails: 2, delay: time.Second}, OutcomeFail, ReasonTimeout, 2},
		{"invalid api key is not retried", &flakyEvaluator{fails: 2, err: &typesafe.APIError{StatusCode: http.StatusUnauthorized}}, OutcomeFail, ReasonInvalidAPIKey, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := enabledConfig()
			cfg.Timeout = 20 * time.Millisecond
			s := New(func() (Config, error) { return cfg, nil }, &memCache{m: map[string]Result{}}, func(string, string) Evaluator { return c.ev })

			r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})

			assert.Equal(t, c.outcome, r.Outcome)
			assert.Equal(t, c.reason, r.Reason)
			assert.Equal(t, c.calls, c.ev.calls)
			// 审计里记下这次审核请求了几次，看得出重试有没有起作用
			assert.Equal(t, c.calls, r.Attempts)
		})
	}
}

// pipeDecodeExec 是「把脚本 base64 之后交给 shell 执行」的写法。模型必须看到脚本本身，
// 不能只看到编码后的外壳。
func pipeDecodeExec(script, shell string) string {
	return "echo '" + base64.StdEncoding.EncodeToString([]byte(script)) + "' | base64 -d | " + shell
}

func sentCommand(t *testing.T, ev *fakeEvaluator) string {
	t.Helper()
	state, ok := ev.last.State.(reviewState)
	require.True(t, ok)
	return state.Command
}

func TestReviewSendsDecodedScriptInsteadOfBase64(t *testing.T) {
	script := "cd /root/docker/vaalhub-web\ndocker compose up -d"
	ev := &fakeEvaluator{}
	s, _ := newTestService(ev, enabledConfig())

	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: pipeDecodeExec(script, "bash")})

	assert.Equal(t, OutcomePass, r.Outcome)
	assert.Equal(t, script, sentCommand(t, ev))
	assert.NotContains(t, sentCommand(t, ev), "base64")
}

func TestReviewDecodesCommonShellWrappers(t *testing.T) {
	script := "docker compose up -d"
	payload := base64.StdEncoding.EncodeToString([]byte(script))
	cases := []string{
		pipeDecodeExec(script, "/bin/bash"),
		"printf '%s' '" + payload + "' | base64 --decode | sh",
		"echo '" + payload + "' | base64 -d | sudo bash",
		`bash -c "echo '` + payload + `' | base64 -d | bash"`,
		"echo '" + payload + "' | base64 -d > /tmp/x.sh && bash /tmp/x.sh",
		"bash -c \"$(echo '" + payload + "' | base64 -d)\"",
		"eval \"$(echo '" + payload + "' | base64 -d)\"",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			ev := &fakeEvaluator{}
			s, _ := newTestService(ev, enabledConfig())
			s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})
			got := sentCommand(t, ev)
			assert.Contains(t, got, script)
			assert.NotContains(t, got, payload)
		})
	}
}

func TestReviewKeepsSiblingCommandsWhenDecoding(t *testing.T) {
	script := "docker inspect vaalhub-web"
	cmd := pipeDecodeExec(script, "bash") + " && rm -rf /var/lib/gone"
	ev := &fakeEvaluator{}
	s, _ := newTestService(ev, enabledConfig())

	s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})

	got := sentCommand(t, ev)
	assert.Contains(t, got, script)
	assert.Contains(t, got, "rm -rf /var/lib/gone")
	assert.NotContains(t, got, "base64")
}

func TestReviewRedactsSecretsInsideDecodedScript(t *testing.T) {
	script := "mysql -uroot -pS3cret -e 'DROP DATABASE shop'"
	ev := &fakeEvaluator{}
	s, _ := newTestService(ev, enabledConfig())

	s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: pipeDecodeExec(script, "bash")})

	got := sentCommand(t, ev)
	assert.Contains(t, got, "DROP DATABASE shop")
	assert.Contains(t, got, "-p***")
	assert.NotContains(t, got, "S3cret")
}

func TestUndecodableExecIsNotSentToTheModel(t *testing.T) {
	cases := []string{
		"echo '!!!' | base64 -d | bash",
		"base64 -d /tmp/payload | bash",
		"echo \"$payload\" | base64 -d | bash",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			ev := &fakeEvaluator{}
			s, _ := newTestService(ev, enabledConfig())
			r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})
			assert.Equal(t, OutcomeFail, r.Outcome)
			assert.Equal(t, ReasonUndecodable, r.Reason)
			assert.Zero(t, ev.calls)
		})
	}
}

func TestBase64WriteWithoutExecutionIsReviewedAsWritten(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("/root/docker/nginx/logs/*.log {\n}"))
	cmd := "echo '" + payload + "' | base64 -d > /etc/logrotate.d/nginx-docker"
	ev := &fakeEvaluator{}
	s, _ := newTestService(ev, enabledConfig())

	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})

	assert.Equal(t, OutcomePass, r.Outcome)
	assert.Contains(t, sentCommand(t, ev), payload)
}

func TestExpandedCommandDoesNotReuseWrapperCache(t *testing.T) {
	cmd := pipeDecodeExec("docker compose up -d", "bash")
	ev := &fakeEvaluator{nouls: map[string]float64{QuestionDisruptive: 0.9}}
	s, cache := newTestService(ev, enabledConfig())
	cfg := enabledConfig()
	cache.m[cacheKey(cfg.BaseURL, cfg.Model, "ssh", cmd)] = Result{
		Outcome: OutcomePass, Model: "jev-1.13.0", Scores: map[string]float64{QuestionDisruptive: 0.01},
	}

	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})

	assert.Equal(t, 1, ev.calls)
	assert.False(t, r.Cached)
	assert.Equal(t, OutcomeReject, r.Outcome)
	assert.Contains(t, sentCommand(t, ev), "docker compose up -d")
}

func TestOrdinaryEvalIsStillReviewed(t *testing.T) {
	ev := &fakeEvaluator{}
	s, _ := newTestService(ev, enabledConfig())

	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: `eval "$cmd"`})

	assert.Equal(t, OutcomePass, r.Outcome)
	assert.Equal(t, 1, ev.calls)
	assert.Contains(t, sentCommand(t, ev), `eval`)
}

func TestDecodeExecHiddenInAConditionalIsNotSent(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("docker compose up -d"))
	cases := []string{
		"if true; then " + pipeDecodeExec("docker compose up -d", "bash") + "; fi",
		"if true; then echo '" + payload + "' | base64 -d > /tmp/x.sh && bash /tmp/x.sh; fi",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			ev := &fakeEvaluator{}
			s, _ := newTestService(ev, enabledConfig())
			r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})
			assert.Equal(t, OutcomeFail, r.Outcome)
			assert.Equal(t, ReasonUndecodable, r.Reason)
			assert.Zero(t, ev.calls)
		})
	}
}

func TestNonShellCommandIsNotDecoded(t *testing.T) {
	cmd := pipeDecodeExec("docker compose up -d", "bash")
	ev := &fakeEvaluator{}
	s, _ := newTestService(ev, enabledConfig())

	s.Review(context.Background(), Input{AssetType: "mysql", Syntax: SyntaxText, Command: cmd})

	assert.Contains(t, sentCommand(t, ev), "base64")
}
