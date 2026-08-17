package command

import (
	"context"
	"flag"
	"fmt"
	"os"
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

	// Parse global flags before the verb
	globalFlags := flag.NewFlagSet("opsctl", flag.ContinueOnError)
	dataDir := globalFlags.String("data-dir", "", "Override the application data directory")
	masterKey := globalFlags.String("master-key", "", "Override the master encryption key (env: OPSKAT_MASTER_KEY)")

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

	*dataDir, *masterKey = applyEnvironmentOverrides(*dataDir, *masterKey)

	remaining := os.Args[verbIdx:]
	if len(remaining) == 0 {
		printUsage()
		return 1
	}

	verb := remaining[0]
	args := remaining[1:]

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
	case "grant":
		return cmdGrant(ctx, args, resolvedSession)
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
  list      List resources (assets, groups, or credentials)
  get       Get detailed information about an asset or credential
  help      Show CLI usage, or 'opsctl help <asset>' for that asset type's command syntax
  ssh       Open an interactive SSH terminal session
  exec      Execute a command on any asset (ssh, database, redis, mongodb, etcd, kafka, k8s)
  create    Create a new resource (asset or group)
  update    Update an existing resource (asset or group)
  delete    Delete an asset or group (always asks for desktop confirmation)
  cp        Copy files between local and remote servers (scp-style)
  batch     Execute multiple commands in parallel across assets
  policy    Manage permanent permission rules (show / allow / deny / rm)
  grant     Submit a batch grant for approval
  ext       Manage and execute extension tools (list, exec)
  version   Print version information

Note:
  Assets can be referenced by numeric ID or by name.
  Use "group/name" to disambiguate when multiple assets share a name.
  Write operations (exec, cp, create, update, delete) require desktop app approval.

Approval:
  Write operations require approval from the running desktop app. When the user
  approves with "Remember", that command pattern is saved, so later matching
  operations skip approval and expire after 24 hours.

Global Flags:
  --data-dir <path>     Override the application data directory
                        (default: platform-specific, e.g. ~/Library/Application Support/opskat)
  --master-key <key>    Override the master encryption key for credential decryption
                        (env: OPSKAT_MASTER_KEY)

Run 'opsctl <command> --help' for more information on a specific command.

Examples:
  opsctl list assets                              List all server assets
  opsctl list assets --type ssh --group-id 3      List SSH assets in group 3
  opsctl list credentials --type ssh_agent        List safe SSH Agent source metadata
  opsctl get credential credential:3              Show safe credential detail and usage
  opsctl get asset web-server                     Show details by name
  opsctl get asset 1                              Show details by ID
  opsctl help web-server                          Show that asset type's command syntax
  opsctl ssh web-server                           Open interactive SSH session
  opsctl ssh production/web-01                    Disambiguate by group/name
  opsctl exec web-server -- uptime                Run command (auto-creates session)
  opsctl exec prod-db -- "SELECT * FROM users"    Query a database
  opsctl exec cache -- "GET session:abc"          Execute a Redis command
  opsctl exec cache --type redis -- "GET session:abc"  Assert the asset's type first
  opsctl create asset --type database --driver mysql --name "DB" --host db.local --username app
  opsctl create group --name "Production"         Create a new group
  opsctl delete asset old-server                  Delete an asset (asks for confirmation)
  opsctl delete group 3 --delete-assets           Delete a group and its assets
  opsctl cp ./config.yml web-server:/etc/app/     Upload a file
  opsctl cp 1:/var/log/app.log ./app.log          Download a file
  opsctl ext list                                   List installed extensions
  opsctl ext exec oss list_buckets --args '{}'       Execute extension tool
`)
}
