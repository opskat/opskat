package command_review_svc

import (
	"context"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extension"
)

// fakeReviewer 是一个审核扩展：记下每次收到的命令，按 reply 回答（默认全部通过）。
type fakeReviewer struct {
	name, version, settingsHash string
	maxItems, maxCommandLen     int
	questions                   []extension.ReviewQuestion
	reply                       func(items []extension.ReviewItem) ([]extension.ReviewCommandResult, error)

	mu    sync.Mutex
	calls [][]extension.ReviewItem
}

func (f *fakeReviewer) Identity() (string, string, string) {
	return f.name, f.version, f.settingsHash
}
func (f *fakeReviewer) Limits() (int, int)                    { return f.maxItems, f.maxCommandLen }
func (f *fakeReviewer) Questions() []extension.ReviewQuestion { return f.questions }
func (f *fakeReviewer) Review(_ context.Context, items []extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]extension.ReviewItem(nil), items...))
	f.mu.Unlock()
	if f.reply == nil {
		return passAll(items)
	}
	return f.reply(items)
}

// sent 是扩展收到的全部命令，按收到的先后排列。
func (f *fakeReviewer) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		for _, it := range c {
			out = append(out, it.Command)
		}
	}
	return out
}

func (f *fakeReviewer) lastSent(t *testing.T) string {
	t.Helper()
	sent := f.sent()
	require.NotEmpty(t, sent, "审核扩展没收到命令")
	return sent[len(sent)-1]
}

func passAll(items []extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
	out := make([]extension.ReviewCommandResult, len(items))
	for i := range out {
		out[i] = extension.ReviewCommandResult{Outcome: extension.ReviewOutcomePass}
	}
	return out, nil
}

var destructiveQuestion = []extension.ReviewQuestion{{
	ID:   "destructive",
	I18n: extension.I18nNames{Name: map[string]string{"en": "Destroys data", "zh-CN": "破坏数据"}},
}}

func newReviewer() *fakeReviewer {
	return &fakeReviewer{name: "rev", version: "1.0.0", settingsHash: "h1", maxItems: 8, maxCommandLen: 4000, questions: destructiveQuestion}
}

type memCache struct {
	mu sync.Mutex
	m  map[string]Result
}

func (c *memCache) Get(_ context.Context, key string) (*Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[key]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (c *memCache) Put(_ context.Context, key string, r Result, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = r
	return nil
}

func newService(rev *fakeReviewer) (*service, *memCache) {
	cache := &memCache{m: map[string]Result{}}
	s := New(func() time.Duration { return DefaultTimeout }, cache, func(context.Context) (ExtensionReviewer, error) {
		return rev, nil
	}).(*service)
	return s, cache
}

// ---- 用哪个审核扩展 ----

func TestNoReviewerFailsAsAConfigError(t *testing.T) {
	s := New(func() time.Duration { return DefaultTimeout }, &memCache{m: map[string]Result{}},
		func(context.Context) (ExtensionReviewer, error) { return nil, ErrNoReviewer })
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Equal(t, OutcomeFail, r.Outcome)
	assert.Equal(t, ReasonNoReviewer, r.Reason)
	assert.Equal(t, []string{ReasonNoReviewer}, notified)
}

func TestUnavailableReviewerFails(t *testing.T) {
	s := New(func() time.Duration { return DefaultTimeout }, &memCache{m: map[string]Result{}},
		func(context.Context) (ExtensionReviewer, error) { return nil, ErrReviewerUnavailable })
	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Equal(t, OutcomeFail, r.Outcome)
	assert.Equal(t, ReasonReviewerUnavailable, r.Reason)
}

// ---- 审核扩展的回答 ----

func TestRejectCarriesQuestionNamesAndScore(t *testing.T) {
	rev := newReviewer()
	rev.reply = func([]extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		return []extension.ReviewCommandResult{{Outcome: extension.ReviewOutcomeReject, Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": 0.93}}}, nil
	}
	s, _ := newService(rev)

	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "rm -rf /data"})
	assert.Equal(t, OutcomeReject, r.Outcome)
	assert.Equal(t, map[string]map[string]string{"destructive": {"en": "Destroys data", "zh-CN": "破坏数据"}}, r.FailedNames)
	assert.Equal(t, map[string]float64{"destructive": 0.93}, r.Scores)
	assert.Equal(t, "rev", r.Extension)
	assert.Equal(t, "1.0.0", r.Version)
}

// 审核扩展的 error 是"没能得到判断"，按审核失败处理，也不能缓存——否则一次限流会让同一条命令
// 在缓存有效期内都审核失败。
func TestReviewerErrorIsFailureAndNotCached(t *testing.T) {
	rev := newReviewer()
	rev.reply = func([]extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		return []extension.ReviewCommandResult{{Outcome: extension.ReviewOutcomeError, Reason: "rate_limited"}}, nil
	}
	s, cache := newService(rev)

	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Equal(t, OutcomeFail, r.Outcome)
	assert.Equal(t, "rate_limited", r.Reason)
	assert.Empty(t, cache.m)

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Len(t, rev.calls, 2)
}

func TestInvalidResultFailsTheBatch(t *testing.T) {
	rev := newReviewer()
	rev.reply = func([]extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		return nil, errors.New("review invalid_result: got 1 results for 2 commands")
	}
	s, _ := newService(rev)

	got := s.ReviewBatch(context.Background(), []Input{{AssetType: "ssh", Command: "ls"}, {AssetType: "ssh", Command: "pwd"}})
	for _, r := range got {
		assert.Equal(t, OutcomeFail, r.Outcome)
		assert.Equal(t, ReasonInvalidResult, r.Reason)
	}
}

// ---- 送审前的准备 ----

// 只换密码本身，要执行的命令和资产类型原样交给审核扩展。
func TestSendsRedactedCommandWithAssetType(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: "AUTH x; mysql -uroot -pS3cret -e 'DROP DATABASE shop'"})

	require.Len(t, rev.calls, 1)
	assert.Equal(t, extension.ReviewItem{AssetType: "ssh", Command: "AUTH x; mysql -uroot -p*** -e 'DROP DATABASE shop'", Syntax: "shell"}, rev.calls[0][0])
}

func TestFailsWithoutCallingTheReviewer(t *testing.T) {
	cases := map[string]struct {
		cmd    string
		reason string
	}{
		"too long":    {strings.Repeat("a", 4001), ReasonTooLong},
		"unparseable": {"echo 'unterminated", ReasonUnparseable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rev := newReviewer()
			s, _ := newService(rev)
			r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: c.cmd})
			assert.Equal(t, OutcomeFail, r.Outcome)
			assert.Equal(t, c.reason, r.Reason)
			assert.Empty(t, rev.calls)
		})
	}
}

// 按审核扩展声明的单批条数拆批，超过它声明的长度的命令不送审。
func TestSplitsBatchesAndRejectsOverlongByTheReviewersLimits(t *testing.T) {
	rev := newReviewer()
	rev.maxItems, rev.maxCommandLen = 2, 4
	s, _ := newService(rev)

	got := s.ReviewBatch(context.Background(), []Input{
		{AssetType: "ssh", Command: "ls", Syntax: SyntaxText},
		{AssetType: "ssh", Command: "pwd", Syntax: SyntaxText},
		{AssetType: "ssh", Command: "who", Syntax: SyntaxText},
		{AssetType: "ssh", Command: "too-long", Syntax: SyntaxText},
	})

	require.Len(t, rev.calls, 2, "maxItems=2，三条可审命令要拆成两次调用")
	// 批次并发调用，先后不定：按批的大小排好再比。
	sort.Slice(rev.calls, func(i, j int) bool { return len(rev.calls[i]) > len(rev.calls[j]) })
	assert.Equal(t, []string{"ls", "pwd"}, []string{rev.calls[0][0].Command, rev.calls[0][1].Command})
	assert.Equal(t, "who", rev.calls[1][0].Command)
	assert.Equal(t, OutcomePass, got[0].Outcome)
	assert.Equal(t, OutcomePass, got[2].Outcome)
	assert.Equal(t, OutcomeFail, got[3].Outcome)
	assert.Equal(t, ReasonTooLong, got[3].Reason)
}

// pipeDecodeExec 是「把脚本 base64 之后交给 shell 执行」的写法。审核扩展必须看到脚本本身，
// 不能只看到编码后的外壳。
func pipeDecodeExec(script, shell string) string {
	return "echo '" + base64.StdEncoding.EncodeToString([]byte(script)) + "' | base64 -d | " + shell
}

func TestSendsDecodedScriptInsteadOfBase64(t *testing.T) {
	script := "cd /root/docker/vaalhub-web\ndocker compose up -d"
	rev := newReviewer()
	s, _ := newService(rev)

	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: pipeDecodeExec(script, "bash")})

	assert.Equal(t, OutcomePass, r.Outcome)
	assert.Equal(t, script, rev.lastSent(t))
}

func TestDecodesCommonShellWrappers(t *testing.T) {
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
			rev := newReviewer()
			s, _ := newService(rev)
			s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})
			got := rev.lastSent(t)
			assert.Contains(t, got, script)
			assert.NotContains(t, got, payload)
		})
	}
}

func TestKeepsSiblingCommandsWhenDecoding(t *testing.T) {
	script := "docker inspect vaalhub-web"
	rev := newReviewer()
	s, _ := newService(rev)

	s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: pipeDecodeExec(script, "bash") + " && rm -rf /var/lib/gone"})

	got := rev.lastSent(t)
	assert.Contains(t, got, script)
	assert.Contains(t, got, "rm -rf /var/lib/gone")
	assert.NotContains(t, got, "base64")
}

func TestRedactsSecretsInsideDecodedScript(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: pipeDecodeExec("mysql -uroot -pS3cret -e 'DROP DATABASE shop'", "bash")})

	got := rev.lastSent(t)
	assert.Contains(t, got, "DROP DATABASE shop")
	assert.Contains(t, got, "-p***")
	assert.NotContains(t, got, "S3cret")
}

func TestUndecodableExecIsNotSent(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("docker compose up -d"))
	cases := []string{
		"echo '!!!' | base64 -d | bash",
		"base64 -d /tmp/payload | bash",
		"echo \"$payload\" | base64 -d | bash",
		// 藏在条件里的解码执行同样不送审
		"if true; then " + pipeDecodeExec("docker compose up -d", "bash") + "; fi",
		"if true; then echo '" + payload + "' | base64 -d > /tmp/x.sh && bash /tmp/x.sh; fi",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			rev := newReviewer()
			s, _ := newService(rev)
			r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})
			assert.Equal(t, OutcomeFail, r.Outcome)
			assert.Equal(t, ReasonUndecodable, r.Reason)
			assert.Empty(t, rev.calls)
		})
	}
}

func TestBase64WriteWithoutExecutionIsReviewedAsWritten(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte("/root/docker/nginx/logs/*.log {\n}"))
	rev := newReviewer()
	s, _ := newService(rev)

	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: "echo '" + payload + "' | base64 -d > /etc/logrotate.d/nginx-docker"})

	assert.Equal(t, OutcomePass, r.Outcome)
	assert.Contains(t, rev.lastSent(t), payload)
}

func TestOrdinaryEvalIsStillReviewed(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: `eval "$cmd"`})

	assert.Equal(t, OutcomePass, r.Outcome)
	assert.Contains(t, rev.lastSent(t), "eval")
}

func TestNonShellCommandIsNotDecoded(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	s.Review(context.Background(), Input{AssetType: "mysql", Syntax: SyntaxText, Command: pipeDecodeExec("docker compose up -d", "bash")})

	assert.Contains(t, rev.lastSent(t), "base64")
}

// ---- 缓存 ----

func TestCachesTheSameCommandButNotFailures(t *testing.T) {
	rev := newReviewer()
	s, cache := newService(rev)

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	second := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Len(t, rev.calls, 1)
	assert.True(t, second.Cached)
	assert.Len(t, cache.m, 1)

	failing := newReviewer()
	failing.reply = func([]extension.ReviewItem) ([]extension.ReviewCommandResult, error) { return nil, errors.New("boom") }
	s2, cache2 := newService(failing)
	s2.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Empty(t, cache2.m)
}

// 缓存按原始命令区分：替换后长得一样的两条命令（这里只有密码不同）不共用审核结果。
func TestCacheDoesNotMixCommandsThatOnlyDifferInSecrets(t *testing.T) {
	rev := newReviewer()
	s, cache := newService(rev)

	s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: "mysql -pA -e 'select 1'"})
	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: "mysql -pB -e 'select 1'"})

	assert.Len(t, rev.calls, 2)
	assert.False(t, r.Cached)
	assert.Len(t, cache.m, 2)
}

// 换了扩展版本或设置都不命中旧结果。
func TestCacheKeyIncludesTheReviewersIdentity(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.True(t, s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"}).Cached)

	rev.settingsHash = "h2"
	assert.False(t, s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"}).Cached)
	rev.version = "1.0.1"
	assert.False(t, s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"}).Cached)
	assert.Len(t, rev.calls, 3)
}

// 展开过的命令不能命中只按外壳算的旧缓存。
func TestExpandedCommandDoesNotReuseWrapperCache(t *testing.T) {
	cmd := pipeDecodeExec("docker compose up -d", "bash")
	rev := newReviewer()
	s, cache := newService(rev)
	cache.m[extensionCacheKey("rev", "1.0.0", "h1", "ssh", cmd)] = Result{Outcome: OutcomePass}

	r := s.Review(context.Background(), Input{AssetType: "ssh", Syntax: SyntaxShell, Command: cmd})

	assert.False(t, r.Cached)
	assert.Len(t, rev.calls, 1)
}

// ---- 调用：并发、超时 ----

// 单批只收一条的审核器也不能让一批命令排队：多个批次并发调用（扩展的实例池最多 4 个）。
func TestBatchesRunConcurrently(t *testing.T) {
	var waiting sync.WaitGroup
	waiting.Add(2)
	rev := newReviewer()
	rev.maxItems = 1
	rev.reply = func(items []extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		waiting.Done()
		done := make(chan struct{})
		go func() { waiting.Wait(); close(done) }()
		select {
		case <-done:
			return passAll(items)
		case <-time.After(2 * time.Second):
			return nil, errors.New("the other batch never ran alongside this one")
		}
	}
	s, _ := newService(rev)

	got := s.ReviewBatch(context.Background(), []Input{{AssetType: "ssh", Command: "ls"}, {AssetType: "ssh", Command: "pwd"}})
	assert.Equal(t, OutcomePass, got[0].Outcome)
	assert.Equal(t, OutcomePass, got[1].Outcome)
}

func TestTimeoutIsRetriedOnceThenFails(t *testing.T) {
	rev := newReviewer()
	rev.reply = func([]extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		return nil, context.DeadlineExceeded
	}
	s, _ := newService(rev)

	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Equal(t, OutcomeFail, r.Outcome)
	assert.Equal(t, ReasonTimeout, r.Reason)
	assert.Len(t, rev.calls, 2)
	assert.Equal(t, 2, r.Attempts)
}

// 第一次超时（如 opsctl 新进程里第一次建连接）、第二次成功：照常得出结果。
func TestSecondAttemptAfterTimeoutSucceeds(t *testing.T) {
	var n atomic.Int32
	rev := newReviewer()
	rev.reply = func(items []extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		if n.Add(1) == 1 {
			return nil, context.DeadlineExceeded
		}
		return passAll(items)
	}
	s, _ := newService(rev)

	r := s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Equal(t, OutcomePass, r.Outcome)
	assert.Equal(t, 2, r.Attempts)
}

// ---- 设置问题的提醒与状态 ----

func TestConfigErrorsNotifyOnceAndShowInStatus(t *testing.T) {
	rev := newReviewer()
	invalidKey := func([]extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		return []extension.ReviewCommandResult{{Outcome: extension.ReviewOutcomeError, Reason: ReasonInvalidAPIKey}}, nil
	}
	rev.reply = invalidKey
	s, _ := newService(rev)
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	s.Review(context.Background(), Input{AssetType: "ssh", Command: "pwd"})
	assert.Equal(t, []string{ReasonInvalidAPIKey}, notified, "同一种设置问题只提醒一次")
	assert.Equal(t, ReasonInvalidAPIKey, s.Status().LastFailReason)

	rev.reply = nil
	s.Review(context.Background(), Input{AssetType: "ssh", Command: "uptime"})
	assert.Equal(t, Status{}, s.Status(), "之后审核成功，设置页不再显示已经恢复的失败")

	rev.reply = invalidKey
	s.Review(context.Background(), Input{AssetType: "ssh", Command: "df -h"})
	assert.Equal(t, []string{ReasonInvalidAPIKey, ReasonInvalidAPIKey}, notified, "恢复之后再出错会重新提醒")
}

func TestTransientFailuresShowInStatusWithoutNotifying(t *testing.T) {
	rev := newReviewer()
	rev.reply = func([]extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		return nil, context.DeadlineExceeded
	}
	s, _ := newService(rev)
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Empty(t, notified)
	assert.Equal(t, ReasonTimeout, s.Status().LastFailReason)
}

// ---- 测试审核服务 ----

// 测试要的是这一刻审核扩展的真实回答：不命中、也不写入缓存。
func TestTestReviewerBypassesTheCache(t *testing.T) {
	rev := newReviewer()
	s, cache := newService(newReviewer())

	assert.Equal(t, OutcomePass, s.TestReviewer(context.Background(), rev).Outcome)
	assert.Equal(t, OutcomePass, s.TestReviewer(context.Background(), rev).Outcome)
	assert.Len(t, rev.calls, 2, "测的是传进来的那个扩展，不是在用的那个")
	assert.Empty(t, cache.m)
}

// 测试审核扩展（扩展设置页的"保存并测试"）测的未必是在用的那个，结果只回给测试的人：
// 不改设置页显示的最近失败，也不触发设置问题的提醒。
func TestTestReviewerLeavesTheReviewStatusAlone(t *testing.T) {
	broken := newReviewer()
	broken.reply = func([]extension.ReviewItem) ([]extension.ReviewCommandResult, error) {
		return []extension.ReviewCommandResult{{Outcome: extension.ReviewOutcomeError, Reason: ReasonNotConfigured}}, nil
	}
	s, _ := newService(broken)
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	require.Equal(t, ReasonNotConfigured, s.Status().LastFailReason)
	require.Equal(t, []string{ReasonNotConfigured}, notified)

	other := newReviewer()
	other.name = "other"
	assert.Equal(t, OutcomePass, s.TestReviewer(context.Background(), other).Outcome)
	assert.Equal(t, ReasonNotConfigured, s.Status().LastFailReason, "测试别的扩展通过，不能清掉在用扩展的失败")

	s.Review(context.Background(), Input{AssetType: "ssh", Command: "ls"})
	assert.Equal(t, []string{ReasonNotConfigured}, notified, "在用的扩展还没恢复过，同一种问题不再提醒")

	fresh, _ := newService(newReviewer())
	var freshNotified []string
	fresh.SetConfigErrorListener(func(reason string) { freshNotified = append(freshNotified, reason) })
	assert.Equal(t, OutcomeFail, fresh.TestReviewer(context.Background(), broken).Outcome)
	assert.Equal(t, Status{}, fresh.Status(), "测试一个没配置好的扩展，不记成全局最近失败")
	assert.Empty(t, freshNotified, "也不弹设置问题的提醒")
}
