package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
)

// deleteApprovalFn is opsctl's desktop-approval entry point for delete, swappable in
// tests — same pattern as execApprovalFn (exec.go) / cpApprovalFn (cp.go).
var deleteApprovalFn = requireApproval

// cmdDelete dispatches "delete asset <ref>" / "delete group <ref> [--delete-assets]"
// into the same delete_asset / delete_group tool handlers the AI uses
// (internal/ai/tool/tool_handlers_crud.go). Deletion is destructive and, by that
// handler's own design, "恒需确认，不可 grant": handleDeleteAsset/handleDeleteGroup
// call permission.RequireChecker unconditionally and always invoke its ConfirmFunc —
// there is no branch that lets a caller skip confirmation, unlike every other
// dispatch through callHandler (which only ever marks WithPreapproved, an exemption
// those two handlers do not accept).
//
// From opsctl's process there is no PolicyChecker to find: permission.WithPolicyChecker
// is only ever called from the desktop AI chat session (internal/app/ai/chat.go) —
// opsctl has no equivalent, so RequireChecker would fail closed with "permission
// checker not available" even after a user has approved via requireApproval.
// withPreapprovedDeleteChecker below closes that gap: it installs a checker whose
// ConfirmFunc auto-allows, but only ever after deleteApprovalFn has already gotten
// the user's real, interactive approval from the running desktop app. That approval
// (not the auto-allow checker) is the one and only confirmation gate — see
// task-11-report.md for the full investigation.
func cmdDelete(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printDeleteUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if len(args) < 2 {
		printDeleteUsage()
		return 1
	}
	ctx = aictx.WithAuditSource(ctx, "opsctl")

	resource := args[0]
	switch resource {
	case "asset":
		ref := args[1]
		// No side effects yet — resolve before requireApproval ever reaches the
		// desktop, so a bad reference fails fast instead of bothering the user
		// for a delete that was never going to happen.
		asset, err := resolveAsset(ctx, ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}
		// 原命令原文挂在 ctx 上，供 NEEDS TTY 结构化拒绝转述给人。
		ctx = withOriginCommand(ctx, "opsctl delete asset "+strings.Join(args[1:], " "))

		approvalResult, err := deleteApprovalFn(ctx, approval.ApprovalRequest{
			Type:      "delete",
			AssetID:   asset.ID,
			AssetName: asset.Name,
			Detail:    fmt.Sprintf("opsctl delete asset %s", ref),
			SessionID: session,
		})
		if err != nil {
			// 拒绝（含结构化拒绝）是真实决策，照常落审计；结构化拒绝退出码 3。
			writeOpsctlAudit(ctx, "delete_asset",
				fmt.Sprintf(`{"asset":%q}`, strconv.FormatInt(asset.ID, 10)), "", err,
				approvalResult.ToCheckResult())
			return writeApprovalFailure(os.Stderr, err)
		}

		ctx = withPreapprovedDeleteChecker(ctx)
		return callHandler(ctx, handlers, "delete_asset", map[string]any{
			"asset": strconv.FormatInt(asset.ID, 10),
		}, approvalResult.ToCheckResult())

	case "group":
		ref := args[1]
		fs := flag.NewFlagSet("delete group", flag.ExitOnError)
		deleteAssets := fs.Bool("delete-assets", false, "Delete the assets in this group as well (default: move them to ungrouped)")
		fs.Usage = func() { printDeleteGroupUsage() }
		_ = fs.Parse(args[2:])

		id, name, err := resolveGroup(ctx, ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return 1
		}

		// Detail is the only field the desktop OpsctlApprovalDialog renders for this
		// request: AssetName is asset-only (ApprovalRequest has no GroupName field,
		// and the single-approval event opsctl:approval never carries one either —
		// see internal/app/opsctl/approval.go), and Command must stay empty (see this
		// function's doc comment on why). Without the resolved group name and a
		// --delete-assets-aware warning here, a user approving "opsctl delete group
		// staging --delete-assets" would see only a DELETE badge and a bare command
		// echo — no group name, no hint that every asset in the group goes with it.
		// Wording mirrors handleDeleteGroup's two branches
		// (internal/ai/tool/tool_handlers_crud.go).
		detail := fmt.Sprintf("opsctl delete group %q: assets move to ungrouped, nothing else is deleted", name)
		if *deleteAssets {
			detail = fmt.Sprintf("opsctl delete group %q AND every asset in it — this cannot be undone from the app", name)
		}

		ctx = withOriginCommand(ctx, "opsctl delete group "+strings.Join(args[1:], " "))

		params := map[string]any{"id": float64(id)}
		if *deleteAssets {
			params["delete_assets"] = true
		}

		approvalResult, err := deleteApprovalFn(ctx, approval.ApprovalRequest{
			Type:      "delete",
			Detail:    detail,
			SessionID: session,
		})
		if err != nil {
			argsJSON, marshalErr := json.Marshal(params)
			if marshalErr != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", marshalErr)
				return 1
			}
			writeOpsctlAudit(ctx, "delete_group", string(argsJSON), "", err, approvalResult.ToCheckResult())
			return writeApprovalFailure(os.Stderr, err)
		}

		ctx = withPreapprovedDeleteChecker(ctx)
		return callHandler(ctx, handlers, "delete_group", params, approvalResult.ToCheckResult())

	default:
		fmt.Fprintf(os.Stderr, "Error: unknown resource %q. Supported: asset, group\n", resource)
		return 1
	}
}

// withPreapprovedDeleteChecker installs a PolicyChecker whose ConfirmFunc always
// allows. It must only be called after deleteApprovalFn has already returned Allow —
// see the cmdDelete doc comment for why this is not a confirmation bypass: the real,
// interactive confirmation already happened one line above, via the running desktop
// app. This checker exists solely so permission.RequireChecker (called unconditionally
// inside handleDeleteAsset/handleDeleteGroup) finds a real checker instead of failing
// closed, and so its ConfirmFunc call has nothing left to ask.
func withPreapprovedDeleteChecker(ctx context.Context) context.Context {
	checker := permission.NewCommandPolicyChecker(func(context.Context, string, []permission.ApprovalItem) permission.ApprovalResponse {
		return permission.ApprovalResponse{Decision: "allow"}
	})
	return permission.WithPolicyChecker(ctx, checker)
}

func printDeleteUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl delete <resource> <ref> [flags]

Resources:
  asset     Delete an asset (soft-delete; connection config is cleared)
  group     Delete a group (assets move to ungrouped unless --delete-assets)

Run 'opsctl delete group --help' for group-specific flags.

Approval:
  Always requires confirmation — this cannot be pre-approved or granted by
  any rule. An interactive terminal prompts here; otherwise the running
  desktop app is asked, and with neither available opsctl exits with code 3
  and a NEEDS TTY marker telling you to run the command yourself.

Examples:
  opsctl delete asset old-server
  opsctl delete asset 1
  opsctl delete group 3
  opsctl delete group staging --delete-assets
`)
}

func printDeleteGroupUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl delete group <ref> [flags]

Arguments:
  ref       Group name, path, or numeric ID

Flags:
  --delete-assets   Also delete every asset in this group (irreversible from
                    the app). Default: assets move to ungrouped and survive.

Approval:
  Always requires confirmation — this cannot be pre-approved or granted by
  any rule. An interactive terminal prompts here; otherwise the running
  desktop app is asked, and with neither available opsctl exits with code 3
  and a NEEDS TTY marker telling you to run the command yourself.

Examples:
  opsctl delete group 3
  opsctl delete group staging --delete-assets
`)
}
