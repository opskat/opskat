package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/opskat/opskat/internal/ai/helper"

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

	if rejectExtraArgs(args[1:]) {
		return 1
	}
	asset, err := resolveAsset(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	return cmdSSHDirect(ctx, asset.ID)
}

// cmdSSHDirect 直连建立交互式 SSH。MFA 挑战在原始流量开始前按 withMFA 的应答来源
// 顺序完成（--mfa-code → 终端提示 → 桌面弹窗 → NEEDS MFA）。
// 拨号阶段用 signal.NotifyContext 让 Ctrl-C 取消 MFA 等待与握手；进入 raw 模式后
// 恢复默认信号处理，^C 交由远端 shell。
func cmdSSHDirect(ctx context.Context, assetID int64) int {
	// 拨号阶段（Agent 列举/签名/MFA 等待/握手）把 SIGINT 转成 ctx 取消，让 Ctrl-C
	// 能打断 MFA 等待；stop() 在拨号一返回就恢复默认信号处理。
	dialCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	client, cleanup, err := helper.DialAssetSSH(withMFA(dialCtx), assetID)
	stop()
	if err != nil {
		return writeRemoteFailure(os.Stderr, err)
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
