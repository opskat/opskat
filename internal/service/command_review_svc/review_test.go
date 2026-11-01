package command_review_svc

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
)

// call 是审核器收到的一次调用。
type call struct {
	AssetType, Command, Syntax string
}

// fakeReviewer 是一个审核器：记下每次收到的命令，按 reply 回答（默认通过）。
type fakeReviewer struct {
	name, version, settingsHash string
	maxCommandLen               int
	questions                   []Question
	reply                       func(c call) (Answer, error)

	mu    sync.Mutex
	calls []call
}

func (f *fakeReviewer) Identity() (string, string, string) {
	return f.name, f.version, f.settingsHash
}
func (f *fakeReviewer) MaxCommandLen() int    { return f.maxCommandLen }
func (f *fakeReviewer) Questions() []Question { return f.questions }
func (f *fakeReviewer) Review(_ context.Context, assetType, command, syntax string) (Answer, error) {
	c := call{AssetType: assetType, Command: command, Syntax: syntax}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.reply == nil {
		return Answer{Outcome: AnswerPass}, nil
	}
	return f.reply(c)
}

func (f *fakeReviewer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeReviewer) lastSent(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls, "审核器没收到命令")
	return f.calls[len(f.calls)-1].Command
}

var destructiveQuestion = []Question{{
	ID:    "destructive",
	Names: map[string]string{"en": "Destroys data", "zh-CN": "破坏数据"},
}}

func newReviewer() *fakeReviewer {
	return &fakeReviewer{name: "rev", version: "1.0.0", settingsHash: "h1", maxCommandLen: 4000, questions: destructiveQuestion}
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

// source 是审核器来源的替身。
type source struct {
	rev Reviewer
	err error
}

func (s source) Current(context.Context) (Reviewer, error) { return s.rev, s.err }

func newService(rev *fakeReviewer) (Service, *memCache) {
	cache := &memCache{m: map[string]Result{}}
	return New(func() time.Duration { return DefaultTimeout }, cache, source{rev: rev}), cache
}

// review 经审核端口审一条命令。
func review(s Service, req permission.ReviewRequest) aictx.ReviewInfo {
	return s.ReviewCommands(context.Background(), []permission.ReviewRequest{req})[0]
}

func ssh(cmd string) permission.ReviewRequest {
	return permission.ReviewRequest{AssetType: "ssh", Command: cmd, Syntax: permission.SyntaxShell}
}

func text(cmd string) permission.ReviewRequest {
	return permission.ReviewRequest{AssetType: "ssh", Command: cmd, Syntax: permission.SyntaxText}
}

// ---- 用哪个审核器 ----

func TestNoReviewerFailsAsAConfigError(t *testing.T) {
	s := New(func() time.Duration { return DefaultTimeout }, &memCache{m: map[string]Result{}}, source{err: ErrNoReviewer})
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	r := review(s, text("ls"))
	assert.Equal(t, aictx.ReviewFail, r.Outcome)
	assert.Equal(t, aictx.ReviewReasonNoReviewer, r.Reason)
	assert.Equal(t, []string{aictx.ReviewReasonNoReviewer}, notified)
}

func TestUnavailableReviewerFails(t *testing.T) {
	s := New(func() time.Duration { return DefaultTimeout }, &memCache{m: map[string]Result{}}, source{err: ErrReviewerUnavailable})
	r := review(s, text("ls"))
	assert.Equal(t, aictx.ReviewFail, r.Outcome)
	assert.Equal(t, aictx.ReviewReasonReviewerUnavailable, r.Reason)
}

// 每次审核都向来源要当前的审核器：换了审核器之后不再用旧的。
func TestAsksTheSourceForTheCurrentReviewerEachTime(t *testing.T) {
	first, second := newReviewer(), newReviewer()
	second.name = "second"
	src := &switchingSource{rev: first}
	s := New(func() time.Duration { return DefaultTimeout }, &memCache{m: map[string]Result{}}, src)

	assert.Equal(t, "rev", review(s, text("ls")).Extension)
	src.rev = second
	assert.Equal(t, "second", review(s, text("pwd")).Extension)
	assert.Equal(t, 1, first.callCount())
	assert.Equal(t, 1, second.callCount())
}

type switchingSource struct{ rev Reviewer }

func (s *switchingSource) Current(context.Context) (Reviewer, error) { return s.rev, nil }

// ---- 审核器的回答 ----

func TestRejectCarriesQuestionNamesAndScore(t *testing.T) {
	rev := newReviewer()
	rev.reply = func(call) (Answer, error) {
		return Answer{Outcome: AnswerReject, Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": 0.93}}, nil
	}
	s, _ := newService(rev)

	r := review(s, text("rm -rf /data"))
	assert.Equal(t, aictx.ReviewReject, r.Outcome)
	assert.Equal(t, map[string]map[string]string{"destructive": {"en": "Destroys data", "zh-CN": "破坏数据"}}, r.FailedNames)
	assert.Equal(t, map[string]float64{"destructive": 0.93}, r.Scores)
	assert.Equal(t, "rev", r.Extension)
	assert.Equal(t, "1.0.0", r.Version)
	assert.Empty(t, r.Mode, "模式由权限检查填写")
}

// 审核器的 error 是"没能得到判断"，按审核失败处理，也不能缓存——否则一次限流会让同一条命令
// 在缓存有效期内都审核失败。
func TestReviewerErrorIsFailureAndNotCached(t *testing.T) {
	rev := newReviewer()
	rev.reply = func(call) (Answer, error) {
		return Answer{Outcome: AnswerError, Reason: "rate_limited"}, nil
	}
	s, cache := newService(rev)

	r := review(s, text("ls"))
	assert.Equal(t, aictx.ReviewFail, r.Outcome)
	assert.Equal(t, "rate_limited", r.Reason)
	assert.Empty(t, cache.m)

	review(s, text("ls"))
	assert.Equal(t, 2, rev.callCount())
}

// 调用出错（不是超时）：按无效结果失败，不重试，不缓存。
func TestCallErrorIsInvalidResultWithoutRetry(t *testing.T) {
	rev := newReviewer()
	rev.reply = func(call) (Answer, error) { return Answer{}, errors.New("tool call failed") }
	s, cache := newService(rev)

	r := review(s, text("ls"))
	assert.Equal(t, aictx.ReviewFail, r.Outcome)
	assert.Equal(t, aictx.ReviewReasonInvalidResult, r.Reason)
	assert.Equal(t, 1, rev.callCount())
	assert.Equal(t, 1, r.Attempts)
	assert.Empty(t, cache.m)
}

// 回答不合规时这条命令按无效结果失败，不缓存，绝不当成通过或未通过；同时送审的其他命令不受影响。
func TestMalformedAnswerFailsThatCommandAndIsNotCached(t *testing.T) {
	for name, bad := range map[string]Answer{
		"unknown outcome":     {Outcome: "allow"},
		"undeclared question": {Outcome: AnswerReject, Failed: []string{"not_declared"}},
		"reject without ids":  {Outcome: AnswerReject},
		"error without cause": {Outcome: AnswerError},
		"pass with failure":   {Outcome: AnswerPass, Failed: []string{"destructive"}},
		"score out of range":  {Outcome: AnswerReject, Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": 2}},
		"negative score":      {Outcome: AnswerReject, Failed: []string{"destructive"}, Scores: map[string]float64{"destructive": -0.1}},
	} {
		t.Run(name, func(t *testing.T) {
			rev := newReviewer()
			rev.reply = func(c call) (Answer, error) {
				if c.Command == "pwd" {
					return bad, nil
				}
				return Answer{Outcome: AnswerPass}, nil
			}
			s, cache := newService(rev)

			got := s.ReviewCommands(context.Background(), []permission.ReviewRequest{text("ls"), text("pwd")})
			assert.Equal(t, aictx.ReviewPass, got[0].Outcome)
			assert.Equal(t, aictx.ReviewFail, got[1].Outcome)
			assert.Equal(t, aictx.ReviewReasonInvalidResult, got[1].Reason)
			assert.Len(t, cache.m, 1, "只缓存合规的那条")
		})
	}
}

// ---- 送审前的准备 ----

// 只换密码本身，要执行的命令、资产类型和写法原样交给审核器。
func TestSendsRedactedCommandWithAssetTypeAndSyntax(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	review(s, ssh("AUTH x; mysql -uroot -pS3cret -e 'DROP DATABASE shop'"))
	review(s, permission.ReviewRequest{AssetType: "redis", Command: "AUTH S3cret", Syntax: permission.SyntaxRedis})
	review(s, permission.ReviewRequest{AssetType: "database", Command: "SELECT 1", Syntax: permission.SyntaxText})

	require.Len(t, rev.calls, 3)
	assert.Equal(t, call{AssetType: "ssh", Command: "AUTH x; mysql -uroot -p*** -e 'DROP DATABASE shop'", Syntax: "shell"}, rev.calls[0])
	assert.Equal(t, "redis", rev.calls[1].Syntax)
	assert.NotContains(t, rev.calls[1].Command, "S3cret")
	assert.Equal(t, call{AssetType: "database", Command: "SELECT 1", Syntax: "text"}, rev.calls[2])
}

func TestFailsWithoutCallingTheReviewer(t *testing.T) {
	cases := map[string]struct {
		cmd    string
		reason string
	}{
		"too long":    {strings.Repeat("a", 4001), aictx.ReviewReasonTooLong},
		"unparseable": {"echo 'unterminated", aictx.ReviewReasonUnparseable},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rev := newReviewer()
			s, _ := newService(rev)
			r := review(s, ssh(c.cmd))
			assert.Equal(t, aictx.ReviewFail, r.Outcome)
			assert.Equal(t, c.reason, r.Reason)
			assert.Empty(t, rev.calls)
		})
	}
}

// 一条命令调用一次审核器；超过它声明的长度的命令不送审。
func TestOneCallPerCommandAndOverlongNotSent(t *testing.T) {
	rev := newReviewer()
	rev.maxCommandLen = 4
	s, _ := newService(rev)

	got := s.ReviewCommands(context.Background(), []permission.ReviewRequest{text("ls"), text("pwd"), text("who"), text("too-long")})

	assert.Equal(t, 3, rev.callCount())
	for i := range 3 {
		assert.Equal(t, aictx.ReviewPass, got[i].Outcome)
	}
	assert.Equal(t, aictx.ReviewFail, got[3].Outcome)
	assert.Equal(t, aictx.ReviewReasonTooLong, got[3].Reason)
}

// pipeDecodeExec 是「把脚本 base64 之后交给 shell 执行」的写法。审核器必须看到脚本本身，
// 不能只看到编码后的外壳。
func pipeDecodeExec(script, shell string) string {
	return "echo '" + base64.StdEncoding.EncodeToString([]byte(script)) + "' | base64 -d | " + shell
}

func TestSendsDecodedScriptInsteadOfBase64(t *testing.T) {
	script := "cd /root/docker/vaalhub-web\ndocker compose up -d"
	rev := newReviewer()
	s, _ := newService(rev)

	r := review(s, ssh(pipeDecodeExec(script, "bash")))

	assert.Equal(t, aictx.ReviewPass, r.Outcome)
	assert.Equal(t, script, rev.lastSent(t))
}

func TestRedactsSecretsInsideDecodedScript(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	review(s, ssh(pipeDecodeExec("mysql -uroot -pS3cret -e 'DROP DATABASE shop'", "bash")))

	got := rev.lastSent(t)
	assert.Contains(t, got, "DROP DATABASE shop")
	assert.Contains(t, got, "-p***")
	assert.NotContains(t, got, "S3cret")
}

func TestUndecodableExecIsNotSent(t *testing.T) {
	cases := []string{
		"echo '!!!' | base64 -d | bash",
		"base64 -d /tmp/payload | bash",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			rev := newReviewer()
			s, _ := newService(rev)
			r := review(s, ssh(cmd))
			assert.Equal(t, aictx.ReviewFail, r.Outcome)
			assert.Equal(t, aictx.ReviewReasonUndecodable, r.Reason)
			assert.Empty(t, rev.calls)
		})
	}
}

func TestNonShellCommandIsNotDecoded(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	review(s, permission.ReviewRequest{AssetType: "mysql", Syntax: permission.SyntaxText, Command: pipeDecodeExec("docker compose up -d", "bash")})

	assert.Contains(t, rev.lastSent(t), "base64")
}

// ---- 缓存 ----

func TestCachesTheSameCommandButNotFailures(t *testing.T) {
	rev := newReviewer()
	s, cache := newService(rev)

	review(s, text("ls"))
	second := review(s, text("ls"))
	assert.Equal(t, 1, rev.callCount())
	assert.True(t, second.Cached)
	assert.Equal(t, "rev", second.Extension)
	assert.Len(t, cache.m, 1)

	failing := newReviewer()
	failing.reply = func(call) (Answer, error) { return Answer{}, errors.New("boom") }
	s2, cache2 := newService(failing)
	review(s2, text("ls"))
	assert.Empty(t, cache2.m)
}

// 缓存按原始命令区分：替换后长得一样的两条命令（这里只有密码不同）不共用审核结果。
func TestCacheDoesNotMixCommandsThatOnlyDifferInSecrets(t *testing.T) {
	rev := newReviewer()
	s, cache := newService(rev)

	review(s, ssh("mysql -pA -e 'select 1'"))
	r := review(s, ssh("mysql -pB -e 'select 1'"))

	assert.Equal(t, 2, rev.callCount())
	assert.False(t, r.Cached)
	assert.Len(t, cache.m, 2)
}

// 换了扩展版本或设置都不命中旧结果。
func TestCacheKeyIncludesTheReviewersIdentity(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)

	review(s, text("ls"))
	assert.True(t, review(s, text("ls")).Cached)

	rev.settingsHash = "h2"
	assert.False(t, review(s, text("ls")).Cached)
	rev.version = "1.0.1"
	assert.False(t, review(s, text("ls")).Cached)
	assert.Equal(t, 3, rev.callCount())
}

// 展开过的命令不能命中只按外壳算的旧缓存。
func TestExpandedCommandDoesNotReuseWrapperCache(t *testing.T) {
	cmd := pipeDecodeExec("docker compose up -d", "bash")
	rev := newReviewer()
	s, cache := newService(rev)
	cache.m[extensionCacheKey("rev", "1.0.0", "h1", "ssh", cmd)] = Result{Outcome: OutcomePass}

	r := review(s, ssh(cmd))

	assert.False(t, r.Cached)
	assert.Equal(t, 1, rev.callCount())
}

// ---- 调用：并发、超时 ----

// 多条命令并发调用审核器，同时最多 4 路（扩展的实例池大小）。
func TestCommandsRunConcurrentlyAtMostFourAtATime(t *testing.T) {
	var inflight, peak atomic.Int32
	release := make(chan struct{})
	rev := newReviewer()
	rev.reply = func(call) (Answer, error) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		inflight.Add(-1)
		return Answer{Outcome: AnswerPass}, nil
	}
	s, _ := newService(rev)

	done := make(chan []aictx.ReviewInfo)
	go func() {
		done <- s.ReviewCommands(context.Background(), []permission.ReviewRequest{
			text("c1"), text("c2"), text("c3"), text("c4"), text("c5"), text("c6"),
		})
	}()
	require.Eventually(t, func() bool { return inflight.Load() == 4 }, 2*time.Second, time.Millisecond,
		"四条命令应同时在审")
	time.Sleep(50 * time.Millisecond) // 给第五路一个越界的机会
	close(release)
	got := <-done

	assert.Equal(t, int32(4), peak.Load())
	assert.Equal(t, 6, rev.callCount())
	for _, r := range got {
		assert.Equal(t, aictx.ReviewPass, r.Outcome)
	}
}

func TestTimeoutIsRetriedOnceThenFails(t *testing.T) {
	rev := newReviewer()
	rev.reply = func(call) (Answer, error) { return Answer{}, context.DeadlineExceeded }
	s, cache := newService(rev)

	r := review(s, text("ls"))
	assert.Equal(t, aictx.ReviewFail, r.Outcome)
	assert.Equal(t, aictx.ReviewReasonTimeout, r.Reason)
	assert.Equal(t, 2, rev.callCount())
	assert.Equal(t, 2, r.Attempts)
	assert.Empty(t, cache.m)
}

// 每次调用单独计时：审核器拖过超时，按超时重试一次。
func TestPerCallTimeoutIsApplied(t *testing.T) {
	slow := &blockingReviewer{fakeReviewer: newReviewer()}
	cache := &memCache{m: map[string]Result{}}
	s := New(func() time.Duration { return 20 * time.Millisecond }, cache, source{rev: slow})

	r := review(s, text("ls"))
	assert.Equal(t, aictx.ReviewReasonTimeout, r.Reason)
	assert.Equal(t, int32(2), slow.calls.Load())
}

// blockingReviewer 一直等到调用被取消。
type blockingReviewer struct {
	*fakeReviewer
	calls atomic.Int32
}

func (b *blockingReviewer) Review(ctx context.Context, _, _, _ string) (Answer, error) {
	b.calls.Add(1)
	<-ctx.Done()
	return Answer{}, ctx.Err()
}

// 第一次超时（如 opsctl 新进程里第一次建连接）、第二次成功：照常得出结果。
func TestSecondAttemptAfterTimeoutSucceeds(t *testing.T) {
	var n atomic.Int32
	rev := newReviewer()
	rev.reply = func(call) (Answer, error) {
		if n.Add(1) == 1 {
			return Answer{}, context.DeadlineExceeded
		}
		return Answer{Outcome: AnswerPass}, nil
	}
	s, _ := newService(rev)

	r := review(s, text("ls"))
	assert.Equal(t, aictx.ReviewPass, r.Outcome)
	assert.Equal(t, 2, r.Attempts)
}

// ---- 设置问题的提醒与状态 ----

func TestConfigErrorsNotifyOnceAndShowInStatus(t *testing.T) {
	rev := newReviewer()
	invalidKey := func(call) (Answer, error) {
		return Answer{Outcome: AnswerError, Reason: aictx.ReviewReasonInvalidAPIKey}, nil
	}
	rev.reply = invalidKey
	s, _ := newService(rev)
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	review(s, text("ls"))
	review(s, text("pwd"))
	assert.Equal(t, []string{aictx.ReviewReasonInvalidAPIKey}, notified, "同一种设置问题只提醒一次")
	assert.Equal(t, aictx.ReviewReasonInvalidAPIKey, s.Status().LastFailReason)

	rev.reply = nil
	review(s, text("uptime"))
	assert.Equal(t, Status{}, s.Status(), "之后审核成功，设置页不再显示已经恢复的失败")

	rev.reply = invalidKey
	review(s, text("df -h"))
	assert.Equal(t, []string{aictx.ReviewReasonInvalidAPIKey, aictx.ReviewReasonInvalidAPIKey}, notified, "恢复之后再出错会重新提醒")
}

// 设置页打开着时要实时看到失败和恢复：状态变了才通知，一批结果只通知一次；状态没变不通知。
func TestStatusListenerNotifiesOnFailureAndRecoveryOnly(t *testing.T) {
	rev := newReviewer()
	s, _ := newService(rev)
	var seen []Status
	s.SetStatusListener(func(st Status) { seen = append(seen, st) })

	review(s, text("ls"))
	assert.Empty(t, seen, "成功且本来就没有失败，状态没变")

	rev.reply = func(call) (Answer, error) { return Answer{}, context.DeadlineExceeded }
	s.ReviewCommands(context.Background(), []permission.ReviewRequest{text("pwd"), text("uptime")})
	require.Len(t, seen, 1, "一批里多条失败只通知一次")
	assert.Equal(t, aictx.ReviewReasonTimeout, seen[0].LastFailReason)
	assert.False(t, seen[0].LastFailAt.IsZero())

	rev.reply = nil
	review(s, text("df -h"))
	require.Len(t, seen, 2)
	assert.Equal(t, Status{}, seen[1], "之后审核成功，通知已恢复")

	review(s, text("free"))
	assert.Len(t, seen, 2, "再成功一次状态没变，不通知")
}

func TestTransientFailuresShowInStatusWithoutNotifying(t *testing.T) {
	rev := newReviewer()
	rev.reply = func(call) (Answer, error) { return Answer{}, context.DeadlineExceeded }
	s, _ := newService(rev)
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	review(s, text("ls"))
	assert.Empty(t, notified)
	assert.Equal(t, aictx.ReviewReasonTimeout, s.Status().LastFailReason)
}

// ---- 测试审核器 ----

// 测试要的是这一刻审核器的真实回答：不命中、也不写入缓存。
func TestTestReviewerBypassesTheCache(t *testing.T) {
	rev := newReviewer()
	s, cache := newService(newReviewer())

	assert.Equal(t, OutcomePass, s.TestReviewer(context.Background(), rev).Outcome)
	assert.Equal(t, OutcomePass, s.TestReviewer(context.Background(), rev).Outcome)
	assert.Equal(t, 2, rev.callCount(), "测的是传进来的那个审核器，不是在用的那个")
	assert.Empty(t, cache.m)
}

// 测试审核器（扩展设置页的"保存并测试"）测的未必是在用的那个，结果只回给测试的人：
// 不改设置页显示的最近失败，也不触发设置问题的提醒。
func TestTestReviewerLeavesTheReviewStatusAlone(t *testing.T) {
	broken := newReviewer()
	broken.reply = func(call) (Answer, error) {
		return Answer{Outcome: AnswerError, Reason: aictx.ReviewReasonNotConfigured}, nil
	}
	s, _ := newService(broken)
	var notified []string
	s.SetConfigErrorListener(func(reason string) { notified = append(notified, reason) })

	review(s, text("ls"))
	require.Equal(t, aictx.ReviewReasonNotConfigured, s.Status().LastFailReason)
	require.Equal(t, []string{aictx.ReviewReasonNotConfigured}, notified)

	other := newReviewer()
	other.name = "other"
	assert.Equal(t, OutcomePass, s.TestReviewer(context.Background(), other).Outcome)
	assert.Equal(t, aictx.ReviewReasonNotConfigured, s.Status().LastFailReason, "测试别的审核器通过，不能清掉在用审核器的失败")

	review(s, text("ls"))
	assert.Equal(t, []string{aictx.ReviewReasonNotConfigured}, notified, "在用的审核器还没恢复过，同一种问题不再提醒")

	fresh, _ := newService(newReviewer())
	var freshNotified []string
	fresh.SetConfigErrorListener(func(reason string) { freshNotified = append(freshNotified, reason) })
	assert.Equal(t, OutcomeFail, fresh.TestReviewer(context.Background(), broken).Outcome)
	assert.Equal(t, Status{}, fresh.Status(), "测试一个没配置好的审核器，不记成全局最近失败")
	assert.Empty(t, freshNotified, "也不弹设置问题的提醒")
}
