package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/sshagent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestIsAgentMFARequired 覆盖代理→直连的交接判定：桌面连接池以非交互方式拨号，
// Agent 资产需要 MFA 时把稳定错误码 ssh_agent_mfa_required 作为字符串回传（JSON
// 握手无法携带类型化错误），交互式 opsctl 据此交接回直连以呈现挑战；其它 Agent
// 错误（如 sign_failed）不触发交接。
func TestIsAgentMFARequired(t *testing.T) {
	typed := &sshagent.Error{Code: sshagent.CodeMFARequired, Message: "server requires interaction"}
	assert.True(t, isAgentMFARequired(typed))

	viaProxy := fmt.Errorf("proxy error: get connection: ssh_agent_mfa_required: the server requires keyboard-interactive")
	assert.True(t, isAgentMFARequired(viaProxy))

	signFailed := fmt.Errorf("proxy error: get connection: ssh_agent_sign_failed: provider refused to sign")
	assert.False(t, isAgentMFARequired(signFailed))

	assert.False(t, isAgentMFARequired(nil))
	assert.False(t, isAgentMFARequired(fmt.Errorf("proxy error: get connection: no such host")))
}

// TestOnlyInteractiveSSHWiresMFA 防止非交互命令（exec/cp/batch）意外接入交互式
// MFA 提示：MFA 挑战适配器与交互式拨号只能被 cmdSSHDirect 这条交互路径引用。
// exec/cp/batch 必须保持非交互——新建 Agent 连接需要 MFA 时只返回
// ssh_agent_mfa_required，绝不显示隐藏提示。
func TestOnlyInteractiveSSHWiresMFA(t *testing.T) {
	for _, f := range []string{"ssh.go", "exec.go", "cp.go", "batch.go"} {
		src, err := os.ReadFile(f) //nolint:gosec // f 是固定包内文件名，用于 AST 守卫测试
		require.NoError(t, err, "read %s", f)
		for _, sig := range []string{"terminalMFACaller", "DialSSHClientInteractive"} {
			got := bytes.Contains(src, []byte(sig))
			if f == "ssh.go" {
				assert.True(t, got, "%s must be used by the interactive ssh path (%s)", sig, f)
			} else {
				assert.False(t, got, "%s must not be referenced by the non-interactive path %s", sig, f)
			}
		}
	}
}
