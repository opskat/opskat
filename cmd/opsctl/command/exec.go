package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"

	"golang.org/x/crypto/ssh"
)

const auditOutputLimit = 32768 // 审计日志捕获输出大小限制

// execApprovalFn 是 exec 的审批入口。变量化是为了可测——与 cp.go 的 cpApprovalFn/
// cpBatchApprovalFn/cpSSHProxyClientFn 同一套路：测试替换掉它，避免真的去连桌面端审批 socket。
var execApprovalFn = requireApproval

// execSSHStreamFn 是 exec 对 ssh 资产的流式执行入口，同上一套路。测试只需要断言
// "ssh 资产走了这条路径"，不需要真的起一个 SSH 会话。
var execSSHStreamFn = execSSHStreaming

// cmdExec 按资产真实类型分派命令执行：ssh 走 execSSHStreaming 这条已文档化的流式
// 通道（stdin 管道转发、stdout/stderr 直写、远端 exit code 透传——SKILL.md 里
// `cat config.yml | opsctl exec web-01 --type ssh -- tee ...` 这类管道工作流靠的就是它，
// 统一 exec handler 返回的是捕获后的字符串，改道会静默打断它们）；其余类型
// （database/redis/mongodb/etcd/kafka/k8s/oss）走统一 exec handler——这是 opsctl 第一次
// 覆盖它们，此前只有 sql/redis/mongo 三个专用 verb，etcd/kafka/k8s 的 handler
// 注册着却没有任何 verb 能抵达。
func cmdExec(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printExecUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	// Stamp provenance once before any policy/approval/audit branch. SSH streaming and
	// approval failures bypass callHandler (which also stamps source for non-SSH tools),
	// so doing this at the command boundary is what keeps every terminal path consistent.
	ctx = aictx.WithAuditSource(ctx, "opsctl")

	asset, err := resolveAsset(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// --type 是可选断言：不参与派发（协议永远来自 asset.Type），只把方言写错的情况
	// 提前变成一条点名双方类型的错误。必须在 requireApproval 之前——它会去问桌面端，
	// 用户不该为一条注定失败的命令点头。
	declaredType, rest := extractTypeFlag(args[1:])
	if err := permission.AssertAssetType(asset, declaredType); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	command := extractCommand(rest)
	if command == "" {
		printExecUsage()
		return 1
	}

	// Executor lookup / canonicalize / precheck — all side-effect-free, all must run
	// before requireApproval (which pops a blocking desktop dialog). See
	// prepareExecCommand's doc comment for why. checkCommand is the (possibly
	// canonicalized) form used below for policy matching and approval display; command
	// stays raw and is what actually gets executed.
	checkCommand, err := prepareExecCommand(ctx, asset, command)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// Require approval. Type 用 asset 真实类型对应的审批类型（ApprovalTypeFor），
	// 不能写死 "exec"：requireApproval 内部拿它去 permission.CheckPermission 做
	// 策略/Grant 匹配，写死 "exec" 会让 redis/database/mongodb 资产统统走上
	// SSH 的 shell 命令策略检查，而不是它们各自的类型策略——策略配置形同虚设，
	// 且离线提示也会挂错检查结果。ApprovalTypeFor(asset.Type) 对 ssh 资产返回
	// "exec"，行为与改造前完全一致；对 database/redis/mongodb 资产返回
	// "sql"/"redis"/"mongo"，与旧 cmdSQL/cmdRedisCmd/cmdMongo 传入的字面量一致。
	//
	// req.Command 是 checkCommand（可能已规范化），不是原始 command：CheckPermission
	// 与桌面审批弹窗都按它匹配/展示——kafka 的策略规则是"恰好两个 token"的规范形状，
	// 喂原始富命令（"topic delete orders"）会让 deny 规则整条失配，见 prepareExecCommand
	// 的注释。用户点"始终允许"时落库的 grant pattern 同样取自 req.Command
	// （approval.go 的 SaveGrantPatternsForApproval 调用点），必须是规范形状才能在
	// 下一次同类命令上重新命中。
	approvalType := permission.ApprovalTypeFor(asset.Type)
	argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, asset.ID, command)
	approvalResult, err := execApprovalFn(ctx, approval.ApprovalRequest{
		Type:      approvalType,
		AssetID:   asset.ID,
		AssetName: asset.Name,
		Command:   checkCommand,
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

	if asset.IsSSH() {
		return execSSHStreamFn(ctx, auditCtx, asset, command, approvalResult)
	}
	// 其余类型走统一 exec handler：opsctl 由此获得 database/redis/mongodb/etcd/kafka/k8s
	// 的全部覆盖。
	return callHandler(auditCtx, handlers, "exec", map[string]any{
		"asset":   strconv.FormatInt(asset.ID, 10),
		"command": command,
	}, approvalResult.ToCheckResult())
}

// execSSHStreaming 是 ssh 资产的流式执行体，从旧的 cmdExec 原样搬迁而来：转发 stdin
// 管道、stdout/stderr 直写本地、透传远端 exit code（proxy 快路径 + helper.ExecWithStdio
// 回落）。ctx 用于直连回落时的 helper.ExecWithStdio 调用；auditCtx 已注入
// approvalResult.SessionID，专供审计写入使用。
func execSSHStreaming(ctx context.Context, auditCtx context.Context, asset *asset_entity.Asset, command string, approvalResult ApprovalResult) int {
	assetID := asset.ID
	argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q}`, assetID, command)

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

	// 尝试通过 proxy 执行（复用 opskat 连接池）
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
  opsctl [--session <id>] exec <asset> [--type <type>] [--] <command>

Arguments:
  asset       Asset name or numeric ID
  command     Command to execute on the remote asset.
              Use '--' to separate the command from opsctl flags.
              Everything after '--' is joined into a single command string.
              Dispatched by the asset's real type: ssh keeps its streaming
              channel (pipes, exit code); the other types (database, redis,
              mongodb, etcd, kafka, k8s, oss) run through the unified exec
              handler.

Flags:
  --type <type>   Optional assertion: fails fast if the asset is not of this
                  type (accepts protocol aliases, e.g. "sql" for database).
                  Does not select dispatch — that always comes from the
                  asset's real type.

Pipe Support (ssh assets only):
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
  opsctl exec web-server --type ssh -- uptime
  opsctl exec 1 --type ssh -- ls -la /var/log
  opsctl exec production/web-01 --type ssh -- cat /etc/hosts
  echo "hello" | opsctl exec web-server --type ssh -- cat
  opsctl exec prod-db --type database -- "SELECT * FROM users LIMIT 10"
  opsctl exec cache --type redis -- "GET session:abc123"
  opsctl --session $ID exec web-01 --type ssh -- systemctl restart nginx
`)
}
