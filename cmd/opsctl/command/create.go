package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/service/asset_put_svc"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

func cmdCreate(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printCreateUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}

	resource := args[0]
	switch resource {
	case "asset":
		return createAsset(ctx, args[1:], session, commandIO{
			stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, readFile: os.ReadFile,
		})
	case "group":
		fs := flag.NewFlagSet("create group", flag.ExitOnError)
		name := fs.String("name", "", "Display name for the group (required)")
		parentID := fs.Int64("parent-id", 0, "Parent group ID for nesting (0 = top-level)")
		icon := fs.String("icon", "", "Icon name")
		description := fs.String("description", "", "Optional description or notes")
		sortOrder := fs.Int("sort-order", 0, "Sort order within the parent; lower comes first")
		fs.Usage = func() { printCreateGroupUsage() }
		_ = fs.Parse(args[1:])

		if *name == "" {
			fmt.Fprintln(os.Stderr, "Error: --name is required")
			fmt.Fprintln(os.Stderr)
			printCreateGroupUsage()
			return 1
		}

		params := map[string]any{"name": *name}
		if *parentID != 0 {
			params["parent_id"] = float64(*parentID)
		}
		if *icon != "" {
			params["icon"] = *icon
		}
		if *description != "" {
			params["description"] = *description
		}
		if *sortOrder != 0 {
			params["sort_order"] = float64(*sortOrder)
		}

		if _, err := requireApproval(ctx, approval.ApprovalRequest{
			Type:      "create",
			Detail:    fmt.Sprintf("opsctl create group --name %s", *name),
			SessionID: session,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}

		return callHandler(ctx, handlers, "put_group", params)

	default:
		fmt.Fprintf(os.Stderr, "Error: unknown resource %q. Supported: asset, group\n", resource)
		return 1
	}
}

type commandIO struct {
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	readFile func(string) ([]byte, error)
}

type preparedAssetCreate interface {
	SafeApprovalDetail() map[string]any
	SafeAuditArgsForResult(*asset_put_svc.Result) map[string]any
	Commit(context.Context) (*asset_put_svc.Result, error)
}

type preparedAssetCreateAdapter struct{ *asset_put_svc.Prepared }

func (p preparedAssetCreateAdapter) Commit(ctx context.Context) (*asset_put_svc.Result, error) {
	return asset_put_svc.Commit(ctx, p.Prepared)
}

var (
	prepareAssetPut = func(ctx context.Context, request asset_put_svc.Request) (preparedAssetCreate, error) {
		prepared, err := asset_put_svc.Prepare(ctx, request)
		if err != nil {
			return nil, err
		}
		return preparedAssetCreateAdapter{Prepared: prepared}, nil
	}
	requireCreateApproval = requireApproval
	notifyAssetChanged    = notifyDesktopAssetChanged
)

func createAsset(ctx context.Context, args []string, session string, streams commandIO) int {
	ctx = aictx.WithAuditSource(ctx, "opsctl")
	if streams.stdin == nil {
		streams.stdin = os.Stdin
	}
	if streams.stdout == nil {
		streams.stdout = os.Stdout
	}
	if streams.stderr == nil {
		streams.stderr = os.Stderr
	}
	if streams.readFile == nil {
		streams.readFile = os.ReadFile
	}
	request, err := parseAssetCreate(ctx, args, assetCreateParserDeps{
		stdin: streams.stdin, stderr: streams.stderr, readFile: streams.readFile, resolveAssetID: resolveAssetID,
	})
	if err != nil {
		writeCreateAssetError(ctx, streams.stderr, err)
		return 1
	}

	prepared, err := prepareAssetPut(ctx, asset_put_svc.Request{
		Asset: request.asset, Config: request.config,
	})
	if err != nil {
		writeCreateAssetError(ctx, streams.stderr, err)
		return 1
	}
	approvalDetail, err := json.Marshal(prepared.SafeApprovalDetail())
	if err != nil {
		writeCreateAssetError(ctx, streams.stderr, fmt.Errorf("encode safe approval detail: %w", err))
		return 1
	}
	approvalResult, err := requireCreateApproval(ctx, approval.ApprovalRequest{
		Type: "create", Detail: string(approvalDetail), SessionID: session,
	})
	if err != nil {
		writeCreateAssetError(ctx, streams.stderr, err)
		return 1
	}

	result, err := prepared.Commit(ctx)
	auditArgs := prepared.SafeAuditArgsForResult(result)
	resultJSON := ""
	if err == nil {
		resultJSON, err = asset_put_svc.ResultJSON(result, "asset created successfully")
	}
	if err != nil {
		writePutAssetAudit(ctx, "put_asset", auditArgs, resultJSON, err, approvalResult.ToCheckResult())
		writeCreateAssetError(ctx, streams.stderr, err)
		return 1
	}
	writePutAssetAudit(ctx, "put_asset", auditArgs, resultJSON, nil, approvalResult.ToCheckResult())
	notifyAssetChanged()
	if _, err := fmt.Fprintln(streams.stdout, prettyJSON(resultJSON)); err != nil {
		writeCreateAssetError(ctx, streams.stderr, fmt.Errorf("write asset result: %w", err))
		return 1
	}
	return 0
}

func writeCreateAssetError(ctx context.Context, stderr io.Writer, err error) {
	if _, writeErr := fmt.Fprintf(stderr, "Error: %v\n", err); writeErr != nil {
		logger.Ctx(ctx).Error("write create asset error",
			zap.NamedError("commandError", err), zap.NamedError("writeError", writeErr))
	}
}

func prettyJSON(value string) string {
	var object any
	if json.Unmarshal([]byte(value), &object) != nil {
		return value
	}
	encoded, err := json.MarshalIndent(object, "", "  ")
	if err != nil {
		return value
	}
	return string(encoded)
}

// writePutAssetAudit 把 put_asset 的 producer 审计投影写入 opsctl 审计。
//
// 它只服务 put_asset：auditArgs 来自 asset_put_svc.Prepared.SafeAuditArgsForResult ——
// producer 自己的字段白名单（普通 config + 资产身份 + typed authentication ref，write-only
// 字段整体缺席），不是通用脱敏。其他 opsctl 命令继续走 handler.go 的 writeOpsctlAudit
// （Task 7 raw-by-default）。
func writePutAssetAudit(ctx context.Context, toolName string, auditArgs map[string]any, result string, execErr error, decision *aictx.CheckResult) {
	argsJSON, err := json.Marshal(auditArgs)
	if err != nil {
		writeOpsctlAudit(ctx, toolName, `{}`, "", fmt.Errorf("encode put_asset audit args: %w", err), decision)
		return
	}
	writeOpsctlAudit(ctx, toolName, string(argsJSON), result, execErr, decision)
}

func cmdUpdate(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printUpdateUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if len(args) < 2 {
		printUpdateUsage()
		return 1
	}

	resource := args[0]
	switch resource {
	case "asset":
		id, err := resolveAssetID(ctx, args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}

		fs := flag.NewFlagSet("update asset", flag.ExitOnError)
		name := fs.String("name", "", "New display name")
		host := fs.String("host", "", "New hostname or IP address")
		port := fs.Int("port", 0, "New SSH port number (0 = unchanged)")
		username := fs.String("username", "", "New SSH login username")
		description := fs.String("description", "", "New description")
		groupID := fs.Int64("group-id", -1, "New group ID (-1 = unchanged, 0 = ungrouped)")
		icon := fs.String("icon", "", "New icon name (e.g. server, kubernetes, docker)")
		fs.Usage = func() { printUpdateAssetUsage() }
		_ = fs.Parse(args[2:])

		// handlePutAsset resolves the target via assetref.Resolve, which accepts numeric
		// id strings — so the "asset" key takes the same id already resolved above, just
		// formatted as a string. Connection fields go under "config" (see cmdCreate).
		config := map[string]any{}
		if *host != "" {
			config["host"] = *host
		}
		if *port != 0 {
			config["port"] = float64(*port)
		}
		if *username != "" {
			config["username"] = *username
		}

		params := map[string]any{
			"asset": strconv.FormatInt(id, 10),
		}
		if len(config) > 0 {
			params["config"] = config
		}
		if *name != "" {
			params["name"] = *name
		}
		if *description != "" {
			params["description"] = *description
		}
		if *groupID >= 0 {
			params["group_id"] = float64(*groupID)
		}
		if *icon != "" {
			params["icon"] = *icon
		}
		// Require approval
		if _, err := requireApproval(ctx, approval.ApprovalRequest{
			Type:      "update",
			AssetID:   id,
			Detail:    fmt.Sprintf("opsctl update asset %s", args[1]),
			SessionID: session,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}

		return callHandler(ctx, handlers, "put_asset", params)

	case "group":
		id, _, err := resolveGroup(ctx, args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}

		fs := flag.NewFlagSet("update group", flag.ExitOnError)
		name := fs.String("name", "", "New display name")
		parentID := fs.Int64("parent-id", -1, "New parent group ID (-1 = unchanged, 0 = top-level)")
		icon := fs.String("icon", "", "New icon name")
		description := fs.String("description", "", "New description")
		sortOrder := fs.Int("sort-order", -1, "New sort order (-1 = unchanged)")
		fs.Usage = func() { printUpdateGroupUsage() }
		_ = fs.Parse(args[2:])

		params := map[string]any{"id": float64(id)}
		if *name != "" {
			params["name"] = *name
		}
		if *parentID >= 0 {
			params["parent_id"] = float64(*parentID)
		}
		if *icon != "" {
			params["icon"] = *icon
		}
		if *description != "" {
			params["description"] = *description
		}
		if *sortOrder >= 0 {
			params["sort_order"] = float64(*sortOrder)
		}

		if _, err := requireApproval(ctx, approval.ApprovalRequest{
			Type:      "update",
			Detail:    fmt.Sprintf("opsctl update group %s", args[1]),
			SessionID: session,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}

		return callHandler(ctx, handlers, "put_group", params)

	default:
		fmt.Fprintf(os.Stderr, "Error: unknown resource %q. Supported: asset, group\n", resource)
		return 1
	}
}

func printCreateUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl create <resource> [flags]

Resources:
  asset     Create any registered built-in asset type through generic config
  group     Create a new asset group

Run 'opsctl create asset --help' or 'opsctl create group --help' for details.
`)
}

func printCreateGroupUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl [--session <id>] create group [flags]

Required Flags:
  --name <string>         Display name for the group

Optional Flags:
  --parent-id <int>       Parent group ID for nesting (0 = top-level)
  --icon <string>         Icon name
  --description <string>  Optional description or notes
  --sort-order <int>      Sort order within the parent; lower comes first

Approval:
  Requires desktop app approval. Session auto-created if not specified.

Examples:
  opsctl create group --name "Production"
  opsctl create group --name "Web Tier" --parent-id 3
`)
}

func printCreateAssetUsage() {
	fmt.Fprintf(os.Stderr, `Usage:
  opsctl [--session <id>] create asset --name <name> [flags]

Generic config:
  --type <type>           Registered built-in asset type (default: ssh)
  --config '<JSON>'       Type-owned JSON object
  --config-file <path>    File containing a type-owned JSON object (mutually exclusive with --config)

Registered built-in types: %s
Run 'opsctl help <type>' for that type's exact accepted/required config fields.

Authentication:
  --credential-id <id>       Reuse an existing managed credential
  --password-stdin           Read plaintext from stdin without a prompt or echo (recommended)
  --password <value>         Unsafe argv plaintext path
  --agent-source-id <id>     SSH Agent source ID
  --agent-key-fingerprint <SHA256 fingerprint>

Warning: --password and plaintext inline --config values may be visible in shell history,
process listings, and CI/automation logs. Prefer --password-stdin or --credential-id.
For plaintext --config-file input, use restrictive file permissions, avoid committing it,
and remove it when no longer needed.

Compatibility convenience flags (only explicitly supplied flags override --config):
  --host, --port, --username, --auth-type, --driver, --database, --read-only
  --ssh-asset, --kubeconfig, --kubeconfig-file, --namespace, --context
  --group-id, --description, --icon

The selected type handler owns required fields, legal combinations, unknown-field rejection,
and default ports. --kubeconfig-file remains the K8s raw-file convenience input.

Approval:
  Validation and reference checks run before desktop approval. The asset is written only
  after approval; plaintext stays encrypted in that asset and never creates a credential.

Examples:
  opsctl create asset --name "Web Server" --host 10.0.0.1 --username root --password-stdin
  opsctl create asset --type database --name "Prod DB" --config '{"driver":"mysql","host":"db.internal","username":"app"}' --credential-id 4
  opsctl create asset --type k8s --name "Prod Cluster" --kubeconfig-file ~/.kube/config --context prod
`, strings.Join(assettype.RegisteredTypes(), ", "))
}

func printUpdateUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl update <resource> <ref> [flags]

Resources:
  asset     Update an existing asset
  group     Update an existing asset group

Run 'opsctl update asset <asset> --help' or 'opsctl update group <group> --help' for details.
`)
}

func printUpdateGroupUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl [--session <id>] update group <ref> [flags]

Arguments:
  ref       Group name, path, or numeric ID

Flags (only provided fields are updated, others remain unchanged):
  --name <string>         New display name
  --parent-id <int>       New parent group ID (-1 = unchanged, 0 = top-level)
  --icon <string>         New icon name
  --description <string>  New description
  --sort-order <int>      New sort order (-1 = unchanged)

Approval:
  Requires desktop app approval. Session auto-created if not specified.

Examples:
  opsctl update group 3 --name "Production"
  opsctl update group staging --parent-id 1
`)
}

func printUpdateAssetUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl [--session <id>] update asset <asset> [flags]

Arguments:
  asset     Asset name or numeric ID

Flags (only provided fields are updated, others remain unchanged):
  --name <string>         New display name
  --host <string>         New hostname or IP address
  --port <int>            New SSH port number (0 = unchanged)
  --username <string>     New SSH login username
  --description <string>  New description
  --group-id <int>        New group ID (-1 = unchanged, 0 = ungrouped)
  --icon <string>         New icon name (see 'opsctl create asset --help' for list)

Approval:
  Requires desktop app approval. Session auto-created if not specified.

Examples:
  opsctl update asset web-server --name "New Name"
  opsctl update asset 1 --host 192.168.1.100 --port 2222
  opsctl update asset web-server --group-id 3
`)
}
