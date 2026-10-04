package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	"golang.org/x/crypto/ssh"
)

const auditOutputLimit = 32768 // 审计日志捕获输出大小限制

// execApprovalFn 是 exec 的审批入口。变量化是为了可测——与 cp.go 的 cpApprovalFn/
// cpBatchApprovalFn 同一套路：测试替换掉它，避免真的去连桌面端审批 socket。
var execApprovalFn = requireApproval

// execStdin is where `--<flag>-file -` reads from; a variable so tests can feed it.
var execStdin io.Reader = os.Stdin

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

	// 归属直接问资产类型注册表（启动时由 registerExtensionAssetTypes 按缓存的 describe()
	// 接线）：内置类型天然报 ("", false)，因为注册表拒绝让扩展占用一个内置类型名——
	// 与桌面端的冲突规则同一条，opsctl 不会因为磁盘上躺着这样一个 manifest 就改道。
	// 提前拿到这个答案，同时喂给 joinCommandWords（多词 argv 的引号策略——扩展的 flag
	// DSL 没有远端 shell，元字符必须逐词保真；ssh 等类型的多词语义不变）和下面的派发
	// 判断，避免同一个"是不是扩展资产"的问题被问两次、答案还可能不一致。
	extName, isExtensionAsset := assettype.ExtensionOwnerOf(asset.Type)

	// --type 是可选断言：不参与派发（协议永远来自 asset.Type），只把方言写错的情况
	// 提前变成一条点名双方类型的错误。必须在 requireApproval 之前——它会去问桌面端，
	// 用户不该为一条注定失败的命令点头。
	declaredType, scope, argv, err := parseExecArgv(args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		printExecUsage()
		return 1
	}
	// 注册了流式执行入口的类型（如通用资产）按 argv 执行：command 串逐个加引号拼成（单个词
	// 也加），canonicalize / 审批 / 审计看到的与 AI exec 写法一致、且能原样切回 argv。其余类型
	// 拼成一整条命令（joinCommandWords：单个词原样，多词按类型决定引号策略）。
	stream, streaming := permission.StreamExecutorFor(asset.Type)
	command := joinCommandWords(argv, isExtensionAsset)
	if streaming {
		command = quoteExecArgv(argv)
	}
	if err := permission.AssertAssetType(asset, declaredType); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	// --scope 只对 redis 资产有意义（库号 / 集群节点 host:port）；其它类型给出 --scope
	// 必须报错而不是静默忽略，见 validateRedisScope。同样要在 requireApproval 之前——
	// 不该为一条注定失败的调用弹审批。
	if err := validateRedisScope(asset, scope); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// 扩展提供的资产类型：命令交回桌面进程执行。理由是执行位置而不是语义——WASM 运行时、
	// 扩展的宿主能力与解密后的资产配置只存在于桌面进程里。桌面端跑的是同一个统一 exec
	// handler，策略/审批/grant/审计因此与内置类型逐字一致，也由那一端落库。
	if isExtensionAsset {
		return execViaDesktop(asset, extName, command, session)
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
	detail, err := execApprovalDetailFor(ctx, asset, args[0], scope, command)
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
	argsJSON := fmt.Sprintf(`{"asset_id":%d,"command":%q,"scope":%q}`, asset.ID, command, scope)
	approvalResult, err := execApprovalFn(ctx, approval.ApprovalRequest{
		Type:      approvalType,
		AssetID:   asset.ID,
		AssetName: asset.Name,
		Command:   checkCommand,
		// scope 不并进 Command/checkCommand：那两个字段驱动策略匹配与 grant pattern
		// 落库（见上方注释），掺入节点地址会让同一条命令因 scope 不同而匹配不上同一条
		// 规则。Detail 只是给审批人看的补充说明，同 cp 的 "src → dst" 用法。
		Detail:    detail,
		SessionID: session,
	})
	// 注入 SessionID 到 context，供审计写入器使用
	auditCtx := aictx.WithSessionID(ctx, approvalResult.SessionID)
	// 同样把 checkCommand 挂到 context 上，供 writeOpsctlAudit 读取（aictx.
	// AuditCommandSlot）：下面三条写审计的路径——本函数的错误分支、execSSHStreamFn、
	// callHandler 内部——都得落这个规范形式，而不是 callHandler 转发给 handler
	// 执行、必须保持原样的那个 command。ssh 资产没有注册 CanonicalizeFunc，
	// checkCommand == command，这里不需要为它特殊处理。
	auditCommand := checkCommand
	auditCtx = aictx.WithAuditCommandSlot(auditCtx, &auditCommand)

	if err != nil {
		writeOpsctlAudit(auditCtx, "exec", argsJSON, "", err, approvalResult.ToCheckResult())
		// 结构化拒绝 → 退出码 3（stderr 首行是裸标记）；其余错误保持 1。
		return writeApprovalFailure(os.Stderr, err)
	}

	if asset.IsSSH() {
		return execSSHStreamFn(ctx, auditCtx, asset, command, approvalResult)
	}
	if streaming {
		return execRegisteredStream(ctx, auditCtx, asset, stream, argv, argsJSON, approvalResult)
	}
	// 其余类型走统一 exec handler：opsctl 由此获得 database/redis/mongodb/etcd/kafka/k8s
	// 的全部覆盖。scope 原样透传给 handleExec（tool_handlers_unified.go），它已经知道
	// 按资产类型解释（目前只有 redis 会用到，非 redis 资产已在上面 validateRedisScope
	// 挡掉了非空 scope，这里传空字符串等价于不传）。
	return callHandler(auditCtx, handlers, "exec", map[string]any{
		"asset":   strconv.FormatInt(asset.ID, 10),
		"command": command,
		"scope":   scope,
	}, approvalResult.ToCheckResult())
}

// execApprovalDetail 构建审批弹窗里 exec 的补充说明：scope 非空时插进 --scope <value>，
// 让审批人看到命令会发往哪个节点/库（spec 例："opsctl exec cache --scope
// 10.0.0.1:6379 -- DBSIZE"）；scope 为空时与改造前完全一致。
func execApprovalDetail(assetRef, scope, command string) string {
	if scope == "" {
		return fmt.Sprintf("opsctl exec %s -- %s", assetRef, command)
	}
	return fmt.Sprintf("opsctl exec %s --scope %s -- %s", assetRef, scope, command)
}

// execApprovalDetailFor 是审批弹窗的补充说明：opsctl 调用原文（execApprovalDetail），
// 该类型注册了审批展示补充（permission.ApprovalDetailFor，如通用资产的操作类型与渲染后的
// 目标地址）时放在它前面。补充生成失败说明命令必然执行失败，调用方在审批之前报错。
func execApprovalDetailFor(ctx context.Context, asset *asset_entity.Asset, assetRef, scope, command string) (string, error) {
	detail := execApprovalDetail(assetRef, scope, command)
	describe, ok := permission.ApprovalDetailFor(asset.Type)
	if !ok {
		return detail, nil
	}
	extra, err := describe(ctx, asset, command)
	if err != nil {
		return "", err
	}
	return extra + "\n" + detail, nil
}

// execRegisteredStream 执行注册了流式入口的类型（permission.StreamExecutorFor）：argv 保留
// 调用方的参数边界，stdin / stdout / stderr 直接透传，退出码取自执行结果。请求没有完成时
// 执行器保证 stdout 为空，这里把错误写到 stderr 并以 1 退出。审计 result 用执行器给出的
// 摘要（HTTP：状态行），不捕获输出——响应体不进审计。
func execRegisteredStream(ctx context.Context, auditCtx context.Context, asset *asset_entity.Asset, stream permission.StreamExecFunc, argv []string, argsJSON string, approvalResult ApprovalResult) int {
	res, err := stream(ctx, asset, argv, permission.Stdio{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	writeOpsctlAudit(auditCtx, "exec", argsJSON, res.AuditResult, err, approvalResult.ToCheckResult())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return res.ExitCode
}

// execSSHStreaming 是 ssh 资产的流式执行体：转发 stdin 管道、stdout/stderr 直写
// 本地、透传远端 exit code，全程走 helper.ExecWithStdio 自行拨号。ctx 用于该调用；
// auditCtx 已注入 approvalResult.SessionID，专供审计写入使用。
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

	execErr := helper.ExecWithStdio(withMFA(ctx), assetID, command, stdin, stdoutW, stderrW)

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
		return writeRemoteFailure(os.Stderr, execErr)
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
  opsctl exec <asset> [--type <type>] [--scope <db|host:port>] [--] <command>

Arguments:
  asset       Asset name or numeric ID
  command     Command to execute on the remote asset.
              Use '--' to separate the command from opsctl flags.
              Everything after '--' becomes one command string, in one of two
              ways. A single word is that string verbatim, so quoting the whole
              command passes shell syntax through untouched:
                opsctl exec web-01 -- 'tail -n50 /var/log/app.log | grep ERR'
              Two or more words are joined back into one string, and how much
              of each word's original shape survives that join depends on the
              asset's real type. For ssh and the other built-in types, only a
              word containing whitespace is re-quoted, so the boundary your
              own shell consumed survives the re-split downstream and
              everything else — globs, pipes, redirection — reaches the
              remote shell as it always has:
                opsctl exec web-01 -- grep "foo bar" *.log  →  grep 'foo bar' *.log
              For an extension asset every word is re-quoted whenever needed,
              because the extension flag DSL has no remote shell: a value
              like --path='/x?a=1&b=2' must reach it as that exact literal,
              not as shell operators:
                opsctl exec my-store -- request --path='/x?a=1&b=2'
              A custom-type (generic) asset is the exception: it keeps each
              argument as given, so -H 'X-Caller: two words' stays one
              argument (see "Custom types" below).
              Dispatched by the asset's real type: ssh keeps its streaming
              channel (pipes, exit code); generic assets stream too (see
              below); the other built-in types (database,
              redis, mongodb, etcd, kafka, k8s, oss) run through the unified
              exec handler; an extension-provided type is executed by the
              running desktop app, which owns the WASM runtime.
              For an extension asset the command is "<tool> --flag=value" or
              "<tool> --flag value" (space form; a boolean flag never
              consumes the following word as its value — pass --flag=false
              explicitly to set it to false); run 'opsctl help <asset>' for
              its tool and flag reference.

Flags:
  --type <type>   Optional assertion: fails fast if the asset is not of this
                  type. Accepts protocol aliases ("sql"/"db" for database,
                  "exec" for ssh, "mongo", "kubernetes"/"kube" for k8s), and
                  database driver names ("mysql", "postgres", "mssql",
                  "sqlite"), which additionally assert the asset's driver —
                  "--type mysql" fails on a PostgreSQL asset.
                  Does not select dispatch — that always comes from the
                  asset's real type.
  --scope <s>     Only meaningful for redis assets (fails with exit code 1 on
                  any other type — it is never silently ignored). Standalone /
                  sentinel: a db index, defaulting to the asset's configured
                  db; SELECT is always rejected. Cluster: a node "host:port"
                  the command runs on, required for commands that have no key
                  to route by (PING/ECHO/TIME/COMMAND and CLUSTER INFO/NODES/
                  SLOTS/SHARDS/KEYSLOT run on any node when scope is
                  omitted; a keyed command routes by slot and
                  ignores scope). Missing/invalid scope on a cluster command
                  that needs one exits 1 and lists the current master
                  addresses on stderr.

Pipe Support (ssh assets only):
  If stdin is not a terminal (i.e., data is piped in), it is forwarded to the
  remote command's stdin. The remote command's stdout and stderr are written
  directly to local stdout and stderr, enabling Unix pipe chains.

  The exit code of the remote command is propagated as opsctl's exit code.

Custom types, HTTP exec mode (--type accepts the custom type slug):
  <METHOD> <PATH> [-H 'Name: value']... [-d <data> | -d @<file> | -d @-] [-i]
  PATH starts with / and is appended to the asset's Base URL; the host injects
  the type's authentication. The response body goes to stdout, the status line
  to stderr (-i also puts the status line and headers on stdout). Exit code 0
  for 2xx, 1 for any other status (the body is still written), 1 with nothing
  on stdout when the request did not complete. Run 'opsctl help <asset>' first.

Approval:
  This command requires approval. Commands matching the asset's allow list
  execute without approval; commands matching the deny list are rejected
  immediately. Everything else needs an approver: an interactive terminal
  (stdin and stderr both TTYs) prompts here, otherwise the running desktop
  app is asked; with neither available opsctl exits with code 3 and a
  NEEDS AUTHORIZATION marker telling you which 'opsctl policy allow' line
  to run. A shell command the policy cannot split into sub-commands (e.g. an
  unclosed quote) matches no rule: it stops with NEEDS TTY and the parse
  error instead — fix the command. Piped stdin (cat file | opsctl exec ...)
  counts as non-interactive — authorize it beforehand instead.

Examples:
  opsctl exec web-server --type ssh -- uptime
  opsctl exec 1 --type ssh -- ls -la /var/log
  opsctl exec production/web-01 --type ssh -- cat /etc/hosts
  echo "hello" | opsctl exec web-server --type ssh -- cat
  opsctl exec web-server --type ssh -- grep "connection refused" /var/log/app.log
  opsctl exec web-server --type ssh -- 'ls /var/log/*.log | wc -l'
  opsctl exec prod-db --type database -- "SELECT * FROM users LIMIT 10"
  opsctl exec cache --type redis -- "GET session:abc123"
  opsctl exec cache --type redis --scope 1 -- "GET session:abc123"
  opsctl exec cache-cluster --type redis --scope 10.0.0.1:6379 -- DBSIZE
  opsctl exec my-bucket -- list_objects --bucket=logs --maxKeys=100
  opsctl exec grafana-prod --type grafana -- GET '/api/search?query=cpu'
  cat dash.json | opsctl exec grafana-prod -- POST /api/dashboards/db -H 'Content-Type: application/json' -d @-
`)
}

// execViaDesktop 把一条扩展资产上的命令交给运行中的桌面端执行。
//
// 桌面端不在时 fail closed：本进程既没有 WASM 运行时，也没有扩展的策略引擎，
// 在这里"本地跑一下"等于同时绕开两者。
func execViaDesktop(asset *asset_entity.Asset, extName, command, session string) int {
	// `--<flag>-file` is opsctl-only: read here, so the desktop (and its approval,
	// grants and audit) only ever sees the inline `--<flag> <content>` form.
	command, err := extreg.ExpandFileFlags(extName, command, execStdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	result, err := delegateExtExecFn(asset.ID, asset.Name, command, session)
	if err != nil {
		if strings.Contains(err.Error(), "cannot connect") {
			fmt.Fprintf(os.Stderr,
				"Error: asset %q is type=%s, provided by extension %q; the desktop app must be running to execute it\n",
				asset.Name, asset.Type, extName)
			return 1
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	// 结果原样输出，供管道与脚本消费。
	fmt.Print(result)
	return 0
}
