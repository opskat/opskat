package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

func cmdList(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printListUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}

	resource := args[0]
	switch resource {
	case "assets":
		fs := flag.NewFlagSet("list assets", flag.ExitOnError)
		assetType := fs.String("type", "", "Filter by asset type (e.g. \"ssh\")")
		groupID := fs.Int64("group-id", 0, "Filter by group ID (0 = all groups)")
		fs.Usage = func() { printListAssetsUsage() }
		_ = fs.Parse(args[1:])

		params := map[string]any{}
		if *assetType != "" {
			params["asset_type"] = *assetType
		}
		if *groupID != 0 {
			params["group_id"] = float64(*groupID)
		}
		return callHandler(ctx, handlers, "list_assets", params)

	case "groups":
		return callHandler(ctx, handlers, "list_groups", nil)

	case "credentials":
		fs := flag.NewFlagSet("list credentials", flag.ExitOnError)
		credentialType := fs.String("type", "", "Filter by credential type: password, ssh_key, or ssh_agent")
		fs.Usage = func() { printListCredentialsUsage() }
		_ = fs.Parse(args[1:])
		return cmdListCredentials(ctx, handlers, *credentialType)

	case "audit":
		fs := flag.NewFlagSet("list audit", flag.ExitOnError)
		asset := fs.String("asset", "", "Filter by asset (name, group/name, or numeric ID)")
		limit := fs.Int("limit", 0, fmt.Sprintf("Maximum rows to show (default %d)", defaultAuditListLimit))
		fs.Usage = func() { printListAuditUsage() }
		_ = fs.Parse(args[1:])
		return cmdListAudit(ctx, *asset, *limit)

	default:
		fmt.Fprintf(os.Stderr, "Error: unknown resource %q. Supported: assets, groups, credentials, audit\n", resource)
		return 1
	}
}

// defaultAuditListLimit 是 --limit 的默认值，与桌面端审计视图的默认页大小一致，
// 以免一次倒出全表。
const defaultAuditListLimit = 20

// auditCommandSummaryWidth 是表格里命令摘要的最大长度，复用 truncateStr 截断。
const auditCommandSummaryWidth = 60

// cmdListAudit 只读列出 audit_logs 中已存储的行，不需要 TTY。行内容原样呈现：
// 不解密、不脱敏、不补充字段、不引入占位近似值（哪些字段该进库由各 producer
// 写入侧的字段白名单决定）。排序沿用 repo 的默认（时间倒序）。
func cmdListAudit(ctx context.Context, asset string, limit int) int {
	if limit <= 0 {
		limit = defaultAuditListLimit
	}
	opts := audit_repo.ListOptions{Limit: limit}
	if asset != "" {
		id, err := resolveAssetID(ctx, asset)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		opts.AssetID = id
	}

	logs, _, err := audit_repo.Audit().List(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	var sb strings.Builder
	w := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, policy.PolicyMsg(ctx, //nolint:errcheck // 终端呈现尽力而为
		"TIME\tSOURCE\tASSET\tTOOL\tCOMMAND\tDECISION SOURCE",
		"时间\t来源\t资产\t工具\t命令摘要\t决策来源"))
	for _, log := range logs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck // 终端呈现尽力而为
			time.Unix(log.Createtime, 0).Format("2006-01-02 15:04:05"),
			log.Source,
			auditAssetCell(log),
			log.ToolName,
			nonEmpty(truncateStr(log.Command, auditCommandSummaryWidth)),
			nonEmpty(log.DecisionSource),
		)
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Print(sb.String())
	return 0
}

// auditAssetCell 资产列：存量字段（AssetName / AssetID）的展示组合。
func auditAssetCell(log *audit_entity.AuditLog) string {
	if log.AssetName != "" {
		if log.AssetID > 0 {
			return fmt.Sprintf("%s (%d)", log.AssetName, log.AssetID)
		}
		return log.AssetName
	}
	if log.AssetID > 0 {
		return fmt.Sprintf("#%d", log.AssetID)
	}
	return "-"
}

func nonEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdGet(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printGetUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if len(args) < 2 {
		printGetUsage()
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
		return callHandler(ctx, handlers, "get_asset", map[string]any{
			"id": float64(id),
		})
	case "credential":
		return cmdGetCredential(ctx, handlers, args[1])
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown resource %q. Supported: asset, credential\n", resource)
		return 1
	}
}

func printListUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl list <resource> [flags]

Resources:
  assets       List server assets
  groups       List asset groups
  credentials  List safe credential and SSH Agent metadata
  audit        List stored audit log rows (read-only, no TTY required)

Run 'opsctl list assets --help' or 'opsctl list credentials --help' for resource-specific flags.

Examples:
  opsctl list assets
  opsctl list assets --type ssh --group-id 3
  opsctl list groups
  opsctl list credentials --type ssh_agent
  opsctl list audit --asset web-01 --limit 50
`)
}

func printListAssetsUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl list assets [flags]

Flags:
  --type <string>       Filter by asset type (e.g. "ssh"). Omit to list all types.
  --group-id <int>      Filter by group ID. 0 or omit to list across all groups.

Examples:
  opsctl list assets
  opsctl list assets --type ssh
  opsctl list assets --group-id 3
`)
}

func printListCredentialsUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl list credentials [flags]

Flags:
  --type <string>       Filter by password, ssh_key, or ssh_agent. Omit to list all.

Examples:
  opsctl list credentials
  opsctl list credentials --type ssh_key
`)
}

func printListAuditUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl list audit [flags]

Read-only listing of stored audit log rows (time, source, asset, tool, command
summary, decision source), newest first. No TTY required. Rows are presented
exactly as stored.

Flags:
  --asset <asset>       Filter by asset (name, group/name, or numeric ID)
  --limit <int>         Maximum rows to show. Default 20.

Examples:
  opsctl list audit
  opsctl list audit --asset web-01 --limit 50
`)
}

func printGetUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl get <resource> <identifier>

Resources:
  asset       Get detailed asset information including safe connection metadata
  credential  Get safe credential detail and usage by typed ref

Arguments:
  asset       Asset name or numeric ID (use 'opsctl list assets' to find them)
  credential  credential:<id> or agent-source:<id>; bare numeric IDs are rejected

Examples:
  opsctl get asset web-server
  opsctl get asset 1
  opsctl get asset production/web-01
  opsctl get credential credential:3
  opsctl get credential agent-source:2
`)
}
