package command

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/opskat/opskat/internal/ai/tool"
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

	default:
		fmt.Fprintf(os.Stderr, "Error: unknown resource %q. Supported: assets, groups, credentials\n", resource)
		return 1
	}
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

Run 'opsctl list assets --help' or 'opsctl list credentials --help' for resource-specific flags.

Examples:
  opsctl list assets
  opsctl list assets --type ssh --group-id 3
  opsctl list groups
  opsctl list credentials --type ssh_agent
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
