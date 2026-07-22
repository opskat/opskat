package command

import (
	"context"
	"fmt"
	"os"

	"github.com/opskat/opskat/internal/ai/tool"
)

// cmdHelp is the CLI counterpart of the AI "help" tool (see
// internal/ai/tool/tools_unified.go): it prints the command syntax and usage notes
// for an asset's type. It is read-only (no state change), so unlike exec/create/
// update/delete it never calls requireApproval — the "help" handler itself resolves
// the asset (accepts id or name) and reports "not found"/"no help yet" errors.
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
  opsctl help <asset>

Arguments:
  asset     Asset name or numeric ID (use 'opsctl list assets' to find them)

Prints the command syntax and usage notes for that asset's type — the same
documentation the AI "help" tool returns before it calls exec against a type
for the first time. Read-only: never asks for approval.

Examples:
  opsctl help web-server
  opsctl help prod-db
  opsctl help 1
`)
}
