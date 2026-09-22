package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/sshagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	wrapped := fmt.Errorf("SSH连接失败: %w", fmt.Errorf("ssh: handshake failed: %w",
		&sshagent.Error{Code: sshagent.CodeMFARequired, Message: "no responder"}))

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
	desk := &fakeDesktop{resp: approval.ApprovalResponse{Approved: false, Reason: "mfa canceled"}}
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

	caller := mfaSources{desktop: desktopMFASender(path, "tok")}.newCaller(9)
	answers, err := caller.SubmitChallenge(context.Background(), otpChallenge)
	require.NoError(t, err)
	assert.Equal(t, []string{"777777"}, answers)
	assert.Equal(t, int64(9), got.AssetID)

	server.Stop()
	_, err = caller.SubmitChallenge(context.Background(), otpChallenge)
	assert.Equal(t, sshagent.CodeMFARequired, mfaCode(t, err), "desktop gone -> NEEDS MFA")
}
