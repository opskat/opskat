package command

import (
	"context"
	"fmt"
	"os"

	"github.com/opskat/opskat/internal/ai/tool"
)

// cmdHelp is the CLI counterpart of the AI "help" tool (see
// internal/ai/tool/tools_unified.go): it prints the command syntax and usage notes
// for an asset's type (or for a type name directly). It is read-only (no state change), so unlike exec/create/
// update/delete it never calls requireApproval — the "help" handler itself resolves
// the asset (accepts id or name) and falls back to a canonical type name.
func cmdHelp(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printHelpUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}

	return callHandler(ctx, handlers, "help", map[string]any{
		"asset": args[0],
	})
}

func printHelpUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl help <asset-or-type>

Arguments:
  asset-or-type     Asset name/numeric ID, or a canonical asset type

Prints the config contract, command syntax, and usage notes for that type — the
same documentation the AI "help" tool uses before put_asset or exec. Passing a
type name works even when no asset of that type exists yet. Read-only: never
asks for approval.

Examples:
  opsctl help web-server
  opsctl help prod-db
  opsctl help 1
  opsctl help kafka
`)
}
