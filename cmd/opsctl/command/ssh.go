package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/sshagent"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

func cmdSSH(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printSSHUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}

	asset, err := resolveAsset(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// 尝试通过 proxy 连接（复用 opskat 连接池）
	if proxy := getSSHProxyClient(); proxy != nil {
		return cmdSSHViaProxy(ctx, proxy, asset.ID)
	}

	// Fallback: 直连
	return cmdSSHDirect(ctx, asset.ID)
}

// cmdSSHViaProxy 通过 opskat 连接池代理建立交互式 SSH。桌面连接池以非交互方式
// 拨号：Agent 资产需要 MFA 时返回 ssh_agent_mfa_required，此时交互式 opsctl 交接
// 回直连路径，把结构化挑战呈现到终端（挑战帧约定）；其它错误原样报出。
func cmdSSHViaProxy(ctx context.Context, proxy *sshpool.Client, assetID int64) int {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to set raw terminal: %v\n", err)
		return 1
	}
	defer func() {
		if err := term.Restore(fd, oldState); err != nil {
			logger.Default().Warn("restore terminal state", zap.Error(err))
		}
	}()

	width, height, err := term.GetSize(fd)
	if err != nil {
		width, height = 80, 24
	}

	resizeCh, stopResize := watchTerminalResizeCh(fd)
	defer stopResize()

	exitCode, err := proxy.InteractiveSSH(sshpool.ProxyRequest{
		AssetID: assetID,
		Cols:    width,
		Rows:    height,
	}, os.Stdin, os.Stdout, resizeCh)
	if err != nil {
		if restoreErr := term.Restore(fd, oldState); restoreErr != nil {
			logger.Default().Warn("restore terminal state", zap.Error(restoreErr))
		}
		// 交互式交接：连接池无法完成交互式 MFA，直连路径负责呈现挑战。
		if isAgentMFARequired(err) {
			return cmdSSHDirect(ctx, assetID)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return exitCode
}

// isAgentMFARequired 判断错误是否由"新建连接需要调用方无法提供的交互"引起。桌面
// 连接池经 JSON 握手回传错误（无法携带类型化错误），故按稳定错误码子串判定；
// 直接拨号路径的 *sshagent.Error 同样命中。
func isAgentMFARequired(err error) bool {
	return err != nil && strings.Contains(err.Error(), sshagent.CodeMFARequired)
}

// terminalMFACaller 是 opsctl 交互式 SSH 路径的 MFA 挑战适配器（挑战帧约定）：把
// 服务器结构化挑战（名称/说明/逐条提示与回显标记）按原样呈现到 out，从终端按
// 服务器顺序读取答案并返回。非回显提示用 ReadPassword 隐藏输入；答案只存在于当前
// 请求，返回后立即丢弃。取消（ctx.Done）时不读取并立即返回，不残留等待。
type terminalMFACaller struct {
	out io.Writer // 挑战呈现出口（stderr）
	in  *os.File  // 答案读取入口（stdin）
}

// SubmitChallenge 实现 sshagent.InteractiveCaller。
func (c *terminalMFACaller) SubmitChallenge(ctx context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	type answerResult struct {
		answers []string
		err     error
	}
	resCh := make(chan answerResult, 1)
	go func() {
		answers, err := c.presentAndRead(ctx, ch)
		resCh <- answerResult{answers: answers, err: err}
	}()
	select {
	case res := <-resCh:
		return res.answers, res.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// presentAndRead 呈现挑战并逐条读取答案。
func (c *terminalMFACaller) presentAndRead(ctx context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	// 挑战呈现是面向终端用户的输出，写入失败不值得中断认证——与包内
	// fmt.Fprintf(os.Stderr, ...) 同一语义（errcheck 默认豁免 os.Stderr 字面量，
	// 这里经 io.Writer 字段所以需要显式豁免）。
	fmt.Fprintln(c.out)                             //nolint:errcheck // 终端呈现尽力而为
	fmt.Fprintln(c.out, "SSH Agent MFA challenge:") //nolint:errcheck // 终端呈现尽力而为
	if ch.Name != "" {
		fmt.Fprintf(c.out, "  name: %s\n", ch.Name) //nolint:errcheck // 终端呈现尽力而为
	}
	if ch.Instruction != "" {
		fmt.Fprintf(c.out, "  instruction: %s\n", ch.Instruction) //nolint:errcheck // 终端呈现尽力而为
	}
	answers := make([]string, len(ch.Prompts))
	for i, prompt := range ch.Prompts {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		echo := true
		if ch.Echo != nil && i < len(ch.Echo) {
			echo = ch.Echo[i]
		}
		fmt.Fprintf(c.out, "  %s: ", prompt) //nolint:errcheck // 终端呈现尽力而为
		val, err := readMFAAnswer(c.in, echo)
		fmt.Fprintln(c.out) //nolint:errcheck // 终端呈现尽力而为
		if err != nil {
			return nil, fmt.Errorf("read MFA answer: %w", err)
		}
		answers[i] = val
	}
	return answers, nil
}

// readMFAAnswer 从终端读取一行答案；echo=false 时隐藏输入。term.ReadPassword 在
// 非终端 fd 上会因 ioctl 失败而报错，故先用 IsTerminal 分流：非终端（测试/管道）
// 直接按字节读行。两条读取路径都按字节进行，避免缓冲器提前吞掉同一轮挑战的后续
// 答案。
func readMFAAnswer(in *os.File, echo bool) (string, error) {
	if !echo && term.IsTerminal(int(in.Fd())) {
		b, err := term.ReadPassword(int(in.Fd()))
		return string(b), err
	}
	var buf []byte
	tmp := make([]byte, 1)
	for {
		n, err := in.Read(tmp)
		if n > 0 {
			if tmp[0] == '\n' {
				break
			}
			buf = append(buf, tmp[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
	}
	return strings.TrimRight(string(buf), "\r"), nil
}

// cmdSSHDirect 直连建立交互式 SSH。交互式路径在原始流量开始前，把 Agent MFA 挑战
// 呈现到终端并读取答案（helper.DialSSHClientInteractive + terminalMFACaller）。
// 拨号阶段用 signal.NotifyContext 让 Ctrl-C 取消 MFA 等待与握手；进入 raw 模式后
// 恢复默认信号处理，^C 交由远端 shell。
func cmdSSHDirect(ctx context.Context, assetID int64) int {
	// 拨号阶段（Agent 列举/签名/MFA 等待/握手）把 SIGINT 转成 ctx 取消，让 Ctrl-C
	// 能打断 MFA 等待；stop() 在拨号一返回就恢复默认信号处理。
	dialCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	client, cleanup, err := helper.DialSSHClientInteractive(dialCtx, assetID, &terminalMFACaller{out: os.Stderr, in: os.Stdin})
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	defer cleanup()

	session, err := client.NewSession()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create session: %v\n", err)
		return 1
	}
	defer func() {
		if err := session.Close(); err != nil {
			logger.Default().Warn("close SSH session", zap.Error(err))
		}
	}()

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to set raw terminal: %v\n", err)
		return 1
	}
	defer func() {
		if err := term.Restore(fd, oldState); err != nil {
			logger.Default().Warn("restore terminal state", zap.Error(err))
		}
	}()

	width, height, err := term.GetSize(fd)
	if err != nil {
		width, height = 80, 24
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", height, width, modes); err != nil {
		if restoreErr := term.Restore(fd, oldState); restoreErr != nil {
			logger.Default().Warn("restore terminal state", zap.Error(restoreErr))
		}
		fmt.Fprintf(os.Stderr, "Error: failed to request PTY: %v\n", err)
		return 1
	}

	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	stopResize := watchTerminalResize(session, fd)
	defer stopResize()

	if err := session.Shell(); err != nil {
		if restoreErr := term.Restore(fd, oldState); restoreErr != nil {
			logger.Default().Warn("restore terminal state", zap.Error(restoreErr))
		}
		fmt.Fprintf(os.Stderr, "Error: failed to start shell: %v\n", err)
		return 1
	}

	if err := session.Wait(); err != nil {
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitStatus()
		}
		return 1
	}
	return 0
}

func printSSHUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl ssh <asset>

Arguments:
  asset     Asset name or numeric ID

Opens an interactive SSH terminal session to the specified asset.
This is intended for human use and does not require desktop app approval.

Examples:
  opsctl ssh web-server
  opsctl ssh 1
  opsctl ssh production/web-01
`)
}
