package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// secretApprovalFn is secret get's approval entry point, variable for the same reason as
// execApprovalFn / cpApprovalFn: tests replace it so they never dial the real desktop
// approval socket.
var secretApprovalFn = requireApproval

// cmdSecret dispatches opsctl's "secret" verb (docs/specs/2026-09-28-generic-asset.md
// 「取值」: `opsctl secret get <asset> <field>`). It only has one subcommand today; the
// switch exists so a future one (there is none planned) doesn't need a new top-level verb.
func cmdSecret(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printSecretUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	switch args[0] {
	case "get":
		return cmdSecretGet(ctx, handlers, args[1:], session)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown secret subcommand %q\n\n", args[0])
		printSecretUsage()
		return 1
	}
}

// cmdSecretGet resolves the asset and field (all side-effect-free, same principle as
// cmdExec's pre-approval checks: nothing here may run before a mismatched/unknown field
// error is reported, since that's a call that is going to fail regardless of approval),
// pops the approval dialog only for a secret field, then calls the same handler the
// get_asset_secret AI tool uses (tool.AllToolDefs) so the value comes back exactly once
// and prints it raw, without callHandler's JSON-pretty-print reformat — the spec pins
// down "prints the raw value followed by a newline" literally, and a secret value that
// happens to parse as JSON (a number, a JSON blob credential) must not be reformatted.
func cmdSecretGet(ctx context.Context, handlers map[string]tool.ToolHandlerFunc, args []string, session string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printSecretGetUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "Error: expected exactly 2 arguments (<asset> <field>), got %d\n\n", len(args))
		printSecretGetUsage()
		return 1
	}
	ctx = aictx.WithAuditSource(ctx, "opsctl")

	assetRef, fieldName := args[0], args[1]
	asset, field, _, err := tool.LookupGenericSecretField(ctx, assetRef, fieldName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	handlerArgs := map[string]any{"asset": strconv.FormatInt(asset.ID, 10), "field": fieldName}
	argsJSON, marshalErr := json.Marshal(handlerArgs)
	if marshalErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", marshalErr)
		return 1
	}

	var decision *aictx.CheckResult
	if field.Secret {
		approvalResult, err := secretApprovalFn(ctx, approval.ApprovalRequest{
			Type:      permission.ApprovalTypeFor(asset_entity.AssetTypeGeneric),
			AssetID:   asset.ID,
			AssetName: asset.Name,
			Command:   permission.SecretSubjectPrefix + fieldName,
			Detail:    tool.SecretApprovalDetail(ctx),
			SessionID: session,
		})
		auditCtx := aictx.WithSessionID(ctx, approvalResult.SessionID)
		if err != nil {
			writeOpsctlAudit(auditCtx, "get_asset_secret", string(argsJSON), "", err, approvalResult.ToCheckResult())
			return writeApprovalFailure(os.Stderr, err)
		}
		ctx = permission.WithPreapproved(auditCtx)
		decision = approvalResult.ToCheckResult()
	}

	handler, ok := handlers["get_asset_secret"]
	if !ok {
		fmt.Fprintln(os.Stderr, "Internal error: unknown tool get_asset_secret")
		return 1
	}
	result, err := handler(ctx, handlerArgs)
	writeOpsctlAudit(ctx, "get_asset_secret", string(argsJSON), result, err, decision)
	if err != nil {
		return writeRemoteFailure(os.Stderr, err)
	}
	fmt.Println(result)
	return 0
}

func printSecretUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl secret get <asset> <field>

Subcommands:
  get    Read one field's value back out of a generic (custom-type) asset.

Run 'opsctl secret get --help' for details.
`)
}

func printSecretGetUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl secret get <asset> <field>

Arguments:
  asset   Generic asset name or numeric ID. Only generic (custom-type) assets are
          supported; a built-in typed asset (ssh, database, redis, ...) is rejected.
  field   The custom type's field name to read. An unknown name fails with the list
          of available field names — run 'opsctl help <asset-or-type>' to see them
          up front.

Output:
  Prints the field's raw value to stdout followed by a newline. Exits 0 on success.

Approval:
  A non-secret field is returned immediately, no approval needed — the same value
  'opsctl help <asset>' already shows.
  A secret field is checked against policy (match object "secret:<field>", no
  default allow). If it needs confirmation, the prompt discloses that the plaintext
  value will be output to the caller, and — when this call is going through the
  AI — that it enters the conversation and is sent to the model provider. "Allow
  always" saves that match object as a standing grant. The desktop app's dialog is
  used when it is running; otherwise an interactive terminal prompts directly, and
  with neither available opsctl exits with code 3 and a NEEDS AUTHORIZATION/NEEDS
  TTY marker, same as 'opsctl exec'.
`)
}
