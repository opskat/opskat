package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	_ "github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/buildinfo"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/cago-frame/cago/configs"
)

// Execute runs the opsctl CLI and returns the exit code.
func Execute() int {
	if len(os.Args) < 2 {
		printUsage()
		return 1
	}

	// Global flags may precede the verb; ones written after it are hoisted below.
	globalFlags := flag.NewFlagSet("opsctl", flag.ContinueOnError)
	dataDir := globalFlags.String("data-dir", "", "Override the application data directory")
	masterKey := globalFlags.String("master-key", "", "Override the master encryption key (env: OPSKAT_MASTER_KEY)")
	mfaCodeFlag := globalFlags.String("mfa-code", "", "Answer an SSH MFA one-time-code prompt (env: OPSKAT_MFA_CODE)")

	// Find the first non-flag argument (verb) position
	verbIdx := 1
	for verbIdx < len(os.Args) && strings.HasPrefix(os.Args[verbIdx], "-") {
		verbIdx++
		if verbIdx < len(os.Args) && !strings.HasPrefix(os.Args[verbIdx], "-") &&
			verbIdx-1 > 0 && !strings.Contains(os.Args[verbIdx-1], "=") {
			verbIdx++
		}
	}

	if err := globalFlags.Parse(os.Args[1:verbIdx]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	remaining := os.Args[verbIdx:]
	if len(remaining) == 0 {
		printUsage()
		return 1
	}

	verb := remaining[0]
	hoisted, args, err := hoistGlobalFlags(verb, remaining[1:])
	if err == nil {
		err = globalFlags.Parse(hoisted)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	*dataDir, *masterKey = applyEnvironmentOverrides(*dataDir, *masterKey)

	if verb == "version" {
		v := configs.Version
		if c := buildinfo.ShortCommitID(); c != "" {
			v += " (" + c + ")"
		}
		fmt.Println(v)
		return 0
	}
	if isCLIUsageHelp(remaining) {
		printUsage()
		return 0
	}
	// Initialize database, credentials, repositories
	ctx := context.Background()
	if err := bootstrap.Init(ctx, bootstrap.Options{
		DataDir:   *dataDir,
		MasterKey: *masterKey,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	// 扩展提供的资产类型：内置类型由 assettype 的 init() 注册，扩展的那份接线在桌面
	// 进程里随 WASM 加载发生，opsctl 只能自己按缓存的 describe() 再接一次。必须早于
	// buildHandlerMap —— 工具描述里的类型清单是那时候取的。
	registerExtensionAssetTypes()

	// 策略消息语言跟随系统 locale（LC_ALL → LC_MESSAGES → LANG）
	ctx = aictx.WithPolicyLang(ctx, resolvePolicyLang(
		os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")))

	// Load app config (MCP port, etc.)
	resolvedDataDir := *dataDir
	if resolvedDataDir == "" {
		resolvedDataDir = bootstrap.AppDataDir()
	}
	if _, err := bootstrap.LoadConfig(resolvedDataDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load config: %v\n", err)
	}

	handlers := buildHandlerMap()

	// 创建 SSH 连接池，供统一 exec/cp 等命令复用 SSH 隧道使用
	sshPool := sshpool.NewPool(&helper.AIPoolDialer{}, 5*time.Minute)
	defer sshPool.Close()
	ctx = helper.WithSSHPool(ctx, sshPool)
	ctx = withMFACode(ctx, resolveMFACode(*mfaCodeFlag))

	// Resolve the active session ID from the data dir (machine-wide single session)
	resolvedSession := resolveSessionID()

	switch verb {
	case "list":
		return cmdList(ctx, handlers, args)
	case "get":
		return cmdGet(ctx, handlers, args)
	case "help":
		return cmdHelp(ctx, handlers, args)
	case "exec":
		return cmdExec(ctx, handlers, args, resolvedSession)
	case "create":
		return cmdCreate(ctx, handlers, args, resolvedSession)
	case "update":
		return cmdUpdate(ctx, handlers, args, resolvedSession)
	case "delete":
		return cmdDelete(ctx, handlers, args, resolvedSession)
	case "cp":
		return cmdCp(ctx, handlers, args, resolvedSession)
	case "ssh":
		return cmdSSH(ctx, args)
	case "batch":
		return cmdBatch(ctx, handlers, args, resolvedSession)
	case "policy":
		return cmdPolicy(ctx, args, resolvedSession)
	case "ext":
		return cmdExt(args)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n\nRun 'opsctl help' for usage.\n", verb)
		return 1
	}
}

func applyEnvironmentOverrides(dataDir, masterKey string) (string, string) {
	if dataDir == "" {
		dataDir = os.Getenv("OPSKAT_DATA_DIR")
	}
	if masterKey == "" {
		masterKey = os.Getenv("OPSKAT_MASTER_KEY")
	}
	return dataDir, masterKey
}

// stringSliceFlag 支持重复指定的字符串 flag（如 --group a --group b），
// policy 族子命令的目标解析使用。
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return fmt.Sprintf("%v", *s) }
func (s *stringSliceFlag) Set(val string) error {
	*s = append(*s, val)
	return nil
}

// isCLIUsageHelp reports whether remaining (the CLI args after global flags) means
// "print the top-level opsctl usage screen," as opposed to "opsctl help <asset>" —
// a real verb that needs the database and handler map bootstrapped (cmdHelp,
// dispatched from the switch below) and must not be swallowed by this early,
// pre-bootstrap check. Bare "help"/"-h"/"--help" (no asset argument) still means
// the usage screen.
func isCLIUsageHelp(remaining []string) bool {
	if len(remaining) == 0 {
		return false
	}
	switch remaining[0] {
	case "-h", "--help":
		return true
	case "help":
		return len(remaining) == 1
	default:
		return false
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `opsctl - CLI for managing opskat remote server assets

Usage:
  opsctl [global-flags] <command> [arguments]

Commands:
  list      List resources (assets, groups, credentials, or audit rows)
  get       Get detailed information about an asset or credential
  help      Show CLI usage, or 'opsctl help <asset>' for that asset type's command syntax
  ssh       Open an interactive SSH terminal session
  exec      Execute a command on any asset (ssh, database, redis, mongodb, etcd, kafka, k8s)
  create    Create a new resource (asset or group)
  update    Update an existing resource (asset or group)
  delete    Delete an asset or group (always requires human confirmation)
  cp        Copy files between local and remote servers (scp-style)
  batch     Execute multiple commands in parallel across assets
  policy    Manage permanent permission rules (show / allow / deny / rm, group, attach / detach)
  ext       Manage extensions (list, dev)
  version   Print version information

Note:
  Assets can be referenced by numeric ID or by name.
  Use "group/name" to disambiguate when multiple assets share a name.
  Write operations (exec, cp, create, update, delete) require approval: a prompt
  in an interactive terminal, the desktop app's dialog when it is running, or a
  structured refusal (exit code 3) when neither is available.

Approval:
  Write operations check permanent rules and saved temporary authorizations
  first; anything still unconfirmed asks a human. An interactive terminal
  prompts right there ("allow always" writes a permanent rule via the same
  path as 'opsctl policy allow'); otherwise the desktop app shows its dialog
  ("Remember" saves a 24-hour temporary authorization). With neither available,
  opsctl exits with code 3: exec/cp/batch print NEEDS AUTHORIZATION plus a
  ready-to-run 'opsctl policy allow' line; create/update/delete print
  NEEDS TTY because no rule can pre-authorize them — run those yourself in
  a terminal instead of retrying. A shell command the policy cannot split
  into sub-commands also prints NEEDS TTY, with the parse error: fix the
  command and run it again.

Global Flags (before or after the command, never after '--'):
  --data-dir <path>     Override the application data directory
                        (default: platform-specific, e.g. ~/Library/Application Support/opskat)
  --master-key <key>    Override the master encryption key for credential decryption
                        (env: OPSKAT_MASTER_KEY)
  --mfa-code <code>     Answer an SSH server's one-time-code MFA prompt
                        (env: OPSKAT_MFA_CODE, preferred: the flag is visible in
                        shell history and process lists)

SSH MFA:
  When a new SSH connection hits a keyboard-interactive MFA challenge, opsctl
  answers it with --mfa-code / OPSKAT_MFA_CODE (a single one-prompt challenge,
  once per connection); otherwise it prompts in an interactive terminal, or
  the running desktop app shows the challenge in a dialog. With none of these
  available it exits with code 3 and prints NEEDS MFA: ask a human for the
  current code (or compute it with a TOTP tool you were given) and retry with
  OPSKAT_MFA_CODE. A rejected code fails with an MFA verification error (exit
  code 1) — do not retry the same code. Each opsctl invocation connects anew,
  so run several commands on the same MFA asset as one 'opsctl batch', which
  verifies each asset once.

Run 'opsctl <command> --help' for more information on a specific command.

Examples:
  opsctl list assets                              List all server assets
  opsctl list assets --type ssh --group-id 3      List SSH assets in group 3
  opsctl list credentials --type ssh_agent        List safe SSH Agent source metadata
  opsctl get credential credential:3              Show safe credential detail and usage
  opsctl get asset web-server                     Show details by name
  opsctl get asset 1                              Show details by ID
  opsctl get asset opsctl://asset/1               Show details from a copied ref
  opsctl help web-server                          Show that asset type's command syntax
  opsctl ssh web-server                           Open interactive SSH session
  opsctl ssh production/web-01                    Disambiguate by group/name
  opsctl exec web-server -- uptime                Run command (approval prompts in your terminal)
  opsctl exec prod-db -- "SELECT * FROM users"    Query a database
  opsctl exec cache -- "GET session:abc"          Execute a Redis command
  opsctl exec cache --type redis -- "GET session:abc"  Assert the asset's type first
  opsctl create asset --type database --driver mysql --name "DB" --host db.local --username app
  opsctl create group --name "Production"         Create a new group
  opsctl delete asset old-server                  Delete an asset (asks for confirmation)
  opsctl delete group 3 --delete-assets           Delete a group and its assets
  opsctl cp ./config.yml web-server:/etc/app/     Upload a file
  opsctl cp 1:/var/log/app.log ./app.log          Download a file
  opsctl policy show web-server                   Show effective rules (read-only, no TTY)
  opsctl policy allow web-server -- 'systemctl restart *'   Pre-approve commands (terminal only)
  opsctl list audit --asset web-server --limit 50 Read stored audit rows (read-only)
  opsctl ext list                                 List installed extensions
  opsctl ext dev ../extensions/.../dist           Install a local extension build into the running app
  opsctl exec my-bucket -- list_objects --bucket=logs   Run an extension tool on its asset
`)
}

// globalFlagNames 是 opsctl 的全局 flag；它们既可写在子命令之前，也可写在子命令之后。
var globalFlagNames = []string{"data-dir", "master-key", "mfa-code"}

// globalFlagToken 判断 arg 是否是全局 flag（-x / --x / -x=v / --x=v），返回是否自带值。
func globalFlagToken(arg string) (ok, inline bool) {
	name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
	if name == arg {
		return false, false
	}
	name, _, inline = strings.Cut(name, "=")
	return slices.Contains(globalFlagNames, name), inline
}

// hoistGlobalFlags 把写在子命令之后的全局 flag 取出来交给全局 FlagSet 解析，其余参数
// 原样留给子命令。只扫描负载之前的区域："--" 之后永远是负载；exec 无 "--" 时，资产
// 之后第一个非选项 token 起是远端命令（opsctl exec 86 mysqld --data-dir /x 里的
// --data-dir 属于 mysqld）。子命令各自解析时会拒绝未知参数，这里不能静默吞掉任何东西。
func hoistGlobalFlags(verb string, args []string) (globals, rest []string, err error) {
	assetSeen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return globals, append(rest, args[i:]...), nil
		}
		if ok, inline := globalFlagToken(arg); ok {
			globals = append(globals, arg)
			if !inline {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("flag needs an argument: %s", arg)
				}
				i++
				globals = append(globals, args[i]) //nolint:gosec // guarded by the i+1 >= len(args) check above
			}
			continue
		}
		if verb == "exec" {
			switch {
			case arg == "--type" && i+1 < len(args):
				rest = append(rest, arg, args[i+1])
				i++
				continue
			case strings.HasPrefix(arg, "-"):
			case !assetSeen:
				assetSeen = true
			default:
				return globals, append(rest, args[i:]...), nil
			}
		}
		rest = append(rest, arg)
	}
	return globals, rest, nil
}
