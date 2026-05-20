package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/sshpool"

	"golang.org/x/crypto/ssh"
)

const auditOutputLimit = 32768 // 审计日志捕获输出大小限制

func cmdExec(ctx context.Context, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printExecUsage()
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
	assetID := asset.ID

	command := extractCommand(args[1:])
	if command == "" {
		printExecUsage()
		return 1
	}

	// Require approval
	argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, assetID, command)
	approvalResult, err := requireApproval(ctx, approval.ApprovalRequest{
		Type:      "exec",
		AssetID:   assetID,
		AssetName: asset.Name,
		Command:   command,
		Detail:    fmt.Sprintf("opsctl exec %s -- %s", args[0], command),
		SessionID: session,
	})
	// 注入 SessionID 到 context，供审计写入器使用
	auditCtx := aictx.WithSessionID(ctx, approvalResult.SessionID)

	if err != nil {
		writeOpsctlAudit(auditCtx, "exec", argsJSON, "", err, approvalResult.ToCheckResult())
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Detect if stdin is a pipe (not a terminal)
	var stdin io.Reader
	if stat, err := os.Stdin.Stat(); err == nil {
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			stdin = os.Stdin
		}
	}

	// 捕获输出用于审计日志
	outBuf := audit.NewLimitedBuffer(auditOutputLimit)
	errBuf := audit.NewLimitedBuffer(auditOutputLimit)
	stdoutW := io.MultiWriter(os.Stdout, outBuf)
	stderrW := io.MultiWriter(os.Stderr, errBuf)

	// 尝试通过 proxy 执行（复用 ops-cat 连接池）
	if proxy := getSSHProxyClient(); proxy != nil {
		exitCode, execErr := proxy.Exec(sshpool.ProxyRequest{
			AssetID: assetID,
			Command: command,
		}, stdin, stdoutW, stderrW)
		auditResult := buildExecAuditResult(exitCode, outBuf.String(), errBuf.String())
		writeOpsctlAudit(auditCtx, "exec", argsJSON, auditResult, execErr, approvalResult.ToCheckResult())
		if execErr != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", execErr)
			return 1
		}
		return exitCode
	}

	// Fallback: 直连
	execErr := helper.ExecWithStdio(ctx, assetID, command, stdin, stdoutW, stderrW)

	// 审计日志
	exitCode := 0
	if execErr != nil {
		var exitErr *ssh.ExitError
		if errors.As(execErr, &exitErr) {
			exitCode = exitErr.ExitStatus()
		} else {
			exitCode = -1
		}
	}
	auditResult := buildExecAuditResult(exitCode, outBuf.String(), errBuf.String())
	writeOpsctlAudit(auditCtx, "exec", argsJSON, auditResult, execErr, approvalResult.ToCheckResult())

	if execErr != nil {
		// Propagate remote command exit code
		var exitErr *ssh.ExitError
		if errors.As(execErr, &exitErr) {
			return exitErr.ExitStatus()
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", execErr)
		return 1
	}
	return 0
}

// buildExecAuditResult 构建 exec 审计日志的 Result 内容
func buildExecAuditResult(exitCode int, stdout, stderr string) string {
	output := stdout
	if stderr != "" {
		if output != "" {
			output += "\nSTDERR:\n" + stderr
		} else {
			output = "STDERR:\n" + stderr
		}
	}
	if output == "" {
		return fmt.Sprintf(`{"exit_code":%d}`, exitCode)
	}
	return fmt.Sprintf("exit_code: %d\n%s", exitCode, output)
}

func printExecUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl [--session <id>] exec <asset> [--] <command>

Arguments:
  asset       Asset name or numeric ID
  command     Shell command to execute on the remote server.
              Use '--' to separate the command from opsctl flags.
              Everything after '--' is joined into a single command string.

Pipe Support:
  If stdin is not a terminal (i.e., data is piped in), it is forwarded to the
  remote command's stdin. The remote command's stdout and stderr are written
  directly to local stdout and stderr, enabling Unix pipe chains.

  The exit code of the remote command is propagated as opsctl's exit code.

Approval:
  This command requires approval from the running desktop app.
  - Commands matching the asset's allow list execute without approval.
  - Commands matching the deny list are rejected immediately.
  - A session is auto-created if not specified. Once the user approves with
    "Allow Session", subsequent commands in the same session skip approval.

Examples:
  opsctl exec web-server -- uptime
  opsctl exec 1 -- ls -la /var/log
  opsctl exec production/web-01 -- cat /etc/hosts
  echo "hello" | opsctl exec web-server -- cat
  opsctl --session $ID exec web-01 -- systemctl restart nginx
`)
}
