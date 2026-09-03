package command

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// extractCommand rebuilds the one command string from the argv the user's shell has
// already split. Everything after the first "--" is the command; with no "--" the
// whole arg list is.
//
// A single word *is* that command string and is passed through untouched — it is the
// documented form for every DSL opsctl forwards to (`-- "SELECT * FROM users"`,
// `-- 'GET session:abc'`), and quoting it would hand the database a literal
// `'SELECT * FROM users'`.
//
// Two or more words are argv, and joining them with a bare space drops exactly the
// quoting the shell just removed: `-- grep "foo bar" file` would arrive downstream as
// four words. Every consumer re-splits this string with a real shell parser — the
// extension flag DSL and the k8s/etcd/kafka canonicalizers through cmdline.Words, a
// remote shell for ssh — so the join must be that parser's inverse. cmdline.QuoteIfNeeded
// is that inverse and is already what cmdline.Command.Render uses; words that need no
// quoting are still emitted bare, so the common `-- ls -la /var/log` is unchanged.
func extractCommand(args []string) string {
	parts := args
	for i, arg := range args {
		if arg == "--" {
			parts = args[i+1:]
			break
		}
	}
	if len(parts) < 2 {
		if len(parts) == 0 {
			return ""
		}
		return parts[0]
	}
	joined := make([]string, len(parts))
	for i, part := range parts {
		// Re-encode only what the join would destroy: the boundary inside a word the
		// local shell already unquoted. A word without whitespace survives the round
		// trip as itself, so quoting it would not preserve anything — it would change
		// meaning, turning `-- ls *.log` (globbed by the remote shell, as ssh(1) does)
		// into a literal.
		if strings.ContainsAny(part, " \t\n") {
			joined[i] = cmdline.QuoteIfNeeded(part)
			continue
		}
		joined[i] = part
	}
	return strings.Join(joined, " ")
}

// extractTypeFlag pulls an optional "--type <value>" (or "--type=<value>") token out of
// args and returns (declaredType, remaining args). It only recognizes the flag before the
// "--" command separator — after "--" every token belongs to the command, never to opsctl
// itself (matching extractCommand's contract that everything past "--" is opaque payload).
// Absent, it returns ("", args) unchanged so extractCommand keeps working on the same list.
func extractTypeFlag(args []string) (string, []string) {
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--type" {
			valueIdx := i + 1
			if valueIdx >= len(args) {
				// "--type" with nothing after it: leave it for extractCommand/validation
				// to deal with rather than silently swallowing a malformed flag.
				return "", args
			}
			value := args[valueIdx] //nolint:gosec // guarded by the valueIdx >= len(args) check above
			rest := make([]string, 0, len(args)-2)
			rest = append(rest, args[:i]...)
			rest = append(rest, args[valueIdx+1:]...)
			return value, rest
		}
		if value, ok := strings.CutPrefix(arg, "--type="); ok {
			rest := make([]string, 0, len(args)-1)
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+1:]...)
			return value, rest
		}
	}
	return "", args
}

// parseRemotePath parses numeric assetID:path strings without repository lookup.
func parseRemotePath(s string) (int64, string) {
	idx := strings.Index(s, ":")
	if idx <= 0 {
		return 0, s
	}
	id, err := strconv.ParseInt(s[:idx], 10, 64)
	if err != nil {
		return 0, s
	}
	return id, s[idx+1:]
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// prepareExecCommand runs the side-effect-free gates a command must clear before any
// approval step touches it — executor lookup, canonicalize, precheck, in that order.
// It mirrors internal/ai/tool/tool_handlers_unified.go's handleExec steps 4/6/7 (see that
// function's doc comment for the full ordering rationale): opsctl used to run only the
// --type assertion before requireApproval, so an asset type with no executor at all
// (rdp/vnc/oss/local), a malformed command that canonicalize would reject (kafka/etcd
// DSL parse errors, k8s with no kubeconfig), or an unmet precondition (serial: no active
// session) all surfaced only *after* the user had already been shown — and had to answer
// — a desktop approval dialog for a command that could never have run.
//
// Returns the command to use for policy matching AND approval display: the canonical
// form if the asset's type registered a CanonicalizeFunc, the original command
// otherwise. The caller must still dispatch execution with the original, raw command —
// see cmdExec's doc comment (step 9) for why feeding the canonical form to the executor
// is lossy.
func prepareExecCommand(ctx context.Context, asset *asset_entity.Asset, command string) (string, error) {
	if _, ok := permission.ExecutorFor(asset.Type); !ok {
		return "", unsupportedExecTypeError(asset)
	}

	checkCommand := command
	if canonicalize, ok := permission.CanonicalizeFor(asset.Type); ok {
		canonical, err := canonicalize(asset, command)
		if err != nil {
			return "", err
		}
		checkCommand = canonical
	}

	if precheck, ok := permission.PrecheckFor(asset.Type); ok {
		if err := precheck(ctx, asset); err != nil {
			return "", err
		}
	}

	return checkCommand, nil
}

// unsupportedExecTypeError mirrors internal/ai/tool/tool_handlers_unified.go's
// unsupportedTypeError verbatim, so the same asset type reports the same message
// whether the command came in through opsctl or the AI exec tool.
func unsupportedExecTypeError(asset *asset_entity.Asset) error {
	return fmt.Errorf("asset %q (type=%s) has no exec support yet; supported types: %s",
		asset.Name, asset.Type, strings.Join(permission.RegisteredExecTypes(), ", "))
}
