package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/sshagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

var otpChallenge = sshagent.MFAChallenge{Name: "Verification", Instruction: "Enter code", Prompts: []string{"OTP: "}, Echo: []bool{false}}

func mfaCode(t *testing.T, err error) string {
	t.Helper()
	code, ok := sshagent.CodeOf(err)
	require.True(t, ok, "want typed MFA error, got %v", err)
	return code
}

func TestMFACaller_CodeAnswersOneSinglePromptRoundPerConnection(t *testing.T) {
	caller := mfaSources{code: "123456"}.newCaller(1)

	answers, err := caller.SubmitChallenge(context.Background(), otpChallenge)
	require.NoError(t, err)
	assert.Equal(t, []string{"123456"}, answers)

	_, err = caller.SubmitChallenge(context.Background(), otpChallenge)
	assert.Equal(t, sshagent.CodeMFAFailed, mfaCode(t, err), "a second round on the same connection must not reuse the code")

	fresh := mfaSources{code: "123456"}.newCaller(1)
	answers, err = fresh.SubmitChallenge(context.Background(), otpChallenge)
	require.NoError(t, err, "each new connection may use the code once")
	assert.Equal(t, []string{"123456"}, answers)
}

func TestMFACaller_CodeRefusesMultiPromptRound(t *testing.T) {
	caller := mfaSources{code: "123456", interactive: true}.newCaller(1)
	_, err := caller.SubmitChallenge(context.Background(), sshagent.MFAChallenge{Prompts: []string{"PIN: ", "OTP: "}, Echo: []bool{false, false}})
	assert.Equal(t, sshagent.CodeMFAFailed, mfaCode(t, err))
	assert.Contains(t, err.Error(), "interactive terminal", "the error must name the way to complete this challenge")
}

func TestMFACaller_InteractiveReadsFromTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	_, _ = io.WriteString(w, "987654\n")

	var out bytes.Buffer
	caller := mfaSources{interactive: true, terminal: func() (*os.File, io.Writer) { return r, &out }}.newCaller(1)
	answers, err := caller.SubmitChallenge(context.Background(), otpChallenge)
	require.NoError(t, err)
	assert.Equal(t, []string{"987654"}, answers)
	assert.Contains(t, out.String(), "Enter code")
	assert.Contains(t, out.String(), "OTP: ")
}

func TestMFACaller_NoSourceIsMFARequired(t *testing.T) {
	caller := mfaSources{}.newCaller(1)
	_, err := caller.SubmitChallenge(context.Background(), otpChallenge)
	assert.Equal(t, sshagent.CodeMFARequired, mfaCode(t, err))
}

func TestWriteRemoteFailure_MFARequiredIsStructuredRefusal(t *testing.T) {
	// ssh 握手把应答方的类型化错误包进 "ssh: handshake failed: %w"。
	_, noSource := mfaSources{}.newCaller(1).SubmitChallenge(context.Background(), otpChallenge)
	wrapped := fmt.Errorf("SSH连接失败: %w", fmt.Errorf("ssh: handshake failed: %w", noSource))

	var out bytes.Buffer
	code := writeRemoteFailure(&out, wrapped)
	assert.Equal(t, refusalExitCode, code)
	firstLine, body, _ := strings.Cut(out.String(), "\n")
	assert.Equal(t, needsMFAMarker, firstLine)
	assert.Contains(t, body, "--mfa-code")
	assert.Contains(t, body, "OPSKAT_MFA_CODE")

	out.Reset()
	code = writeRemoteFailure(&out, &sshagent.Error{Code: sshagent.CodeMFAFailed, Message: "server rejected"})
	assert.Equal(t, 1, code, "a rejected answer is an ordinary failure, not a request for a code")
	assert.True(t, strings.HasPrefix(out.String(), "Error: "))

	// 没接 opsctl 应答方的拨号（如数据库 / Redis 经 Agent 资产的 SSH 隧道，不在 MFA 应答
	// 范围内）同样报 ssh_agent_mfa_required，但 --mfa-code 帮不上它：不能指引调用方去要码。
	out.Reset()
	code = writeRemoteFailure(&out, fmt.Errorf("ssh: handshake failed: %w",
		&sshagent.Error{Code: sshagent.CodeMFARequired, Message: "the server requires keyboard-interactive but no interactive caller is available"}))
	assert.Equal(t, 1, code, "a dial opsctl cannot answer is not NEEDS MFA")
	assert.True(t, strings.HasPrefix(out.String(), "Error: "))
}

// 集群缺节点的错误（helper.RedisNodeRequiredError）已经列出当前主节点，但那条消息是
// AI/opsctl/桌面控制台共用的 helper 级文案，不认识 opsctl 的 --scope 标志（spec opsctl
// 一节："集群缺少节点时退出码 1，stderr 给出主节点列表与 --scope 用法"）。
// writeRemoteFailure 是 cmdExec 通往统一 exec handler 那条路径（callHandler）的唯一
// 出口，跟 needsMFA 用的是同一种"识别特定错误类型再追加文案"手法——不按资产类型
// 字符串分支，只认错误类型。
func TestWriteRemoteFailure_RedisNodeRequiredErrorShowsScopeUsage(t *testing.T) {
	err := &helper.RedisNodeRequiredError{Command: "DBSIZE", Masters: []string{"10.0.0.1:6379", "10.0.0.2:6379"}}

	var out bytes.Buffer
	code := writeRemoteFailure(&out, err)

	assert.Equal(t, 1, code, "not a structured refusal")
	assert.Contains(t, out.String(), "10.0.0.1:6379")
	assert.Contains(t, out.String(), "10.0.0.2:6379")
	assert.Contains(t, out.String(), "--scope")
	assert.Contains(t, out.String(), "host:port")
}

func TestResolveMFACode_FlagBeatsEnv(t *testing.T) {
	t.Setenv("OPSKAT_MFA_CODE", "from-env")
	assert.Equal(t, "from-flag", resolveMFACode("from-flag"))
	assert.Equal(t, "from-env", resolveMFACode(""))
}

// TestTerminalMFACaller_PresentsChallengeAndReturnsAnswers 是 opsctl 交互式 SSH
// 路径挑战帧约定的核心：把服务器结构化挑战（名称/说明/逐条提示与回显标记）呈现到
// 输出，按服务器顺序读取答案并原样返回，非回显提示的输入不可见（ReadPassword）。
func TestTerminalMFACaller_PresentsChallengeAndReturnsAnswers(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	// 两路读取都按字节进行（ReadPassword 在非终端 fd 上退化为逐字节行读取），
	// 因此可以在调用前把全部答案写进管道，不会发生缓冲器提前吞掉后续答案。
	_, _ = io.WriteString(w, "answer-one\n")
	_, _ = io.WriteString(w, "answer-two\n")

	var out bytes.Buffer
	c := &terminalMFACaller{out: &out, in: r}

	answers, err := c.SubmitChallenge(context.Background(), sshagent.MFAChallenge{
		Name:        "Verification code",
		Instruction: "Enter the code shown on your device",
		Prompts:     []string{"Code:", "PIN:"},
		Echo:        []bool{true, false},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"answer-one", "answer-two"}, answers)

	// 结构化文本原样呈现：名称、说明与每条提示都可见，服务器顺序保持。
	text := out.String()
	assert.Contains(t, text, "Verification code")
	assert.Contains(t, text, "Enter the code shown on your device")
	assert.Contains(t, text, "Code:")
	assert.Contains(t, text, "PIN:")
	// 提示按服务器顺序（Code: 在 PIN: 之前）。
	ci := bytes.Index([]byte(text), []byte("Code:"))
	pi := bytes.Index([]byte(text), []byte("PIN:"))
	assert.True(t, ci >= 0 && pi > ci, "prompts must appear in server order, got %q", text)
}

// TestTerminalMFACaller_CanceledContextReturnsImmediately 覆盖"命令 context 取消时
// 取消 MFA 等待"：已取消的 ctx 不读取任何输入，立即返回取消错误。
func TestTerminalMFACaller_CanceledContextReturnsImmediately(t *testing.T) {
	r, _, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	c := &terminalMFACaller{out: io.Discard, in: r}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = c.SubmitChallenge(ctx, sshagent.MFAChallenge{Prompts: []string{"code:"}})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "got %v", err)
}

// TestTerminalMFACaller_CancelDuringWait 覆盖 MFA 等待中的取消：读取被阻塞时
// ctx 取消立即返回，而不是继续等待用户输入。
func TestTerminalMFACaller_CancelDuringWait(t *testing.T) {
	r, _, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	c := &terminalMFACaller{out: io.Discard, in: r}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = c.SubmitChallenge(ctx, sshagent.MFAChallenge{Prompts: []string{"code:"}})
	assert.True(t, errors.Is(err, context.Canceled), "got %v", err)
}

// fakeDesktop 记录发往桌面端的 MFA 请求并按预设回应。
type fakeDesktop struct {
	calls []approval.ApprovalRequest
	resp  approval.ApprovalResponse
	err   error
}

func (f *fakeDesktop) send(_ context.Context, req approval.ApprovalRequest) (approval.ApprovalResponse, error) {
	f.calls = append(f.calls, req)
	return f.resp, f.err
}

func TestMFACaller_NonInteractiveAsksDesktop(t *testing.T) {
	desk := &fakeDesktop{resp: approval.ApprovalResponse{Approved: true, MFAAnswers: []string{"424242"}}}
	caller := mfaSources{desktop: desk.send}.newCaller(7)

	answers, err := caller.SubmitChallenge(context.Background(), otpChallenge)
	require.NoError(t, err)
	assert.Equal(t, []string{"424242"}, answers)
	require.Len(t, desk.calls, 1)
	req := desk.calls[0]
	assert.Equal(t, "mfa", req.Type)
	assert.Equal(t, int64(7), req.AssetID)
	assert.Equal(t, &approval.MFAChallenge{Name: "Verification", Instruction: "Enter code", Prompts: []string{"OTP: "}, Echo: []bool{false}}, req.MFA)
}

func TestMFACaller_DesktopCancelIsCanceled(t *testing.T) {
	desk := &fakeDesktop{resp: approval.ApprovalResponse{Approved: false, Reason: approval.MFACanceledReason}}
	_, err := mfaSources{desktop: desk.send}.newCaller(7).SubmitChallenge(context.Background(), otpChallenge)
	assert.Equal(t, sshagent.CodeCancelled, mfaCode(t, err))
	var out bytes.Buffer
	assert.Equal(t, 1, writeRemoteFailure(&out, err), "a cancel is an ordinary failure, not NEEDS MFA")
}

func TestMFACaller_DesktopUnavailableIsMFARequired(t *testing.T) {
	desk := &fakeDesktop{err: errors.New("cannot connect to desktop app")}
	_, err := mfaSources{desktop: desk.send}.newCaller(7).SubmitChallenge(context.Background(), otpChallenge)
	assert.Equal(t, sshagent.CodeMFARequired, mfaCode(t, err))
}

func TestMFACaller_TerminalAndCodeComeBeforeDesktop(t *testing.T) {
	desk := &fakeDesktop{resp: approval.ApprovalResponse{Approved: true, MFAAnswers: []string{"desk"}}}

	answers, err := mfaSources{code: "flag", desktop: desk.send}.newCaller(7).SubmitChallenge(context.Background(), otpChallenge)
	require.NoError(t, err)
	assert.Equal(t, []string{"flag"}, answers)

	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	_, _ = io.WriteString(w, "tty\n")
	src := mfaSources{interactive: true, terminal: func() (*os.File, io.Writer) { return r, io.Discard }, desktop: desk.send}
	answers, err = src.newCaller(7).SubmitChallenge(context.Background(), otpChallenge)
	require.NoError(t, err)
	assert.Equal(t, []string{"tty"}, answers)
	assert.Empty(t, desk.calls)
}

// 经真实 approval.sock 往返：桌面端回的答案原样到达应答方。
func TestDesktopMFAOverRealIPC(t *testing.T) {
	dir, err := os.MkdirTemp("", "mfa-ipc-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := approval.SocketPath(dir)
	var got approval.ApprovalRequest
	server := approval.NewServer(func(_ context.Context, req approval.ApprovalRequest) approval.ApprovalResponse {
		got = req
		return approval.ApprovalResponse{Approved: true, MFAAnswers: []string{"777777"}}
	}, "tok")
	require.NoError(t, server.Start(path))
	t.Cleanup(server.Stop)

	ctrl := gomock.NewController(t)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	mockAsset.EXPECT().Find(gomock.Any(), int64(9)).Return(&asset_entity.Asset{ID: 9, Name: "bastion"}, nil).AnyTimes()
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

	caller := mfaSources{desktop: desktopMFASender(path, "tok")}.newCaller(9)
	answers, err := caller.SubmitChallenge(context.Background(), otpChallenge)
	require.NoError(t, err)
	assert.Equal(t, []string{"777777"}, answers)
	assert.Equal(t, int64(9), got.AssetID)
	assert.Equal(t, "bastion", got.AssetName, "the dialog names the asset being connected")

	server.Stop()
	_, err = caller.SubmitChallenge(context.Background(), otpChallenge)
	assert.Equal(t, sshagent.CodeMFARequired, mfaCode(t, err), "desktop gone -> NEEDS MFA")
}

// syncBuffer 是可并发写读的输出缓冲。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// batch 里多个 MFA 资产并发拨号：终端只有一个，挑战必须逐个呈现、逐个读答案，
// 否则两条提示交错、两个读协程抢同一 stdin 的字节，答案错配。
func TestMFACaller_ConcurrentTerminalChallengesAreSerialized(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	out := &syncBuffer{}
	newSrc := func() mfaSources {
		return mfaSources{interactive: true, terminal: func() (*os.File, io.Writer) { return r, out }}
	}
	type result struct {
		answers []string
		err     error
	}
	first, second := make(chan result, 1), make(chan result, 1)
	go func() {
		a, err := newSrc().newCaller(1).SubmitChallenge(context.Background(), otpChallenge)
		first <- result{a, err}
	}()
	require.Eventually(t, func() bool { return strings.Count(out.String(), "SSH MFA challenge:") == 1 }, 2*time.Second, 5*time.Millisecond)
	go func() {
		a, err := newSrc().newCaller(2).SubmitChallenge(context.Background(), otpChallenge)
		second <- result{a, err}
	}()
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, strings.Count(out.String(), "SSH MFA challenge:"), "the second challenge must wait for the first to finish")

	_, _ = io.WriteString(w, "111111\n")
	res := <-first
	require.NoError(t, res.err)
	assert.Equal(t, []string{"111111"}, res.answers)
	_, _ = io.WriteString(w, "222222\n")
	res = <-second
	require.NoError(t, res.err)
	assert.Equal(t, []string{"222222"}, res.answers)
}

// 桌面端回的非批准响应只有「用户取消」才算取消；鉴权失败等其它原因必须如实上报。
func TestMFACaller_DesktopRefusalReportsReason(t *testing.T) {
	desk := &fakeDesktop{resp: approval.ApprovalResponse{Approved: false, Reason: "authentication failed"}}
	_, err := mfaSources{desktop: desk.send}.newCaller(7).SubmitChallenge(context.Background(), otpChallenge)
	require.Error(t, err)
	code, _ := sshagent.CodeOf(err)
	assert.NotEqual(t, sshagent.CodeCancelled, code, "an auth failure is not a user cancel")
	assert.Contains(t, err.Error(), "authentication failed")
}
