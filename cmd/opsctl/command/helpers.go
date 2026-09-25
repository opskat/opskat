package command

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// parseExecArgs 解析 opsctl exec <asset> 之后的参数：[--type <t>] [--] <command>。
// 选项只出现在命令开始之前——遇到 "--" 或第一个非选项 token 即进入命令，其后一切
// 原样属于远端命令（find / --type f 里的 --type 不是 opsctl 的）。命令之前的未知
// 选项报错，不能静默丢弃，也不能拼进远端命令。全局 flag 已由 hoistGlobalFlags 取走。
//
// literalWords 选择多词 argv 重新拼接时的引号策略，见 joinCommandWords；调用方
// （cmdExec）在解析参数前已经从资产类型拿到了这个答案（assettype.ExtensionOwnerOf），
// 这里只是把那个已知的答案传下去，不重新判断资产类型。
func parseExecArgs(args []string, literalWords bool) (declaredType, command string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			command = joinCommandWords(args[i+1:], literalWords)
		case arg == "--type":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("--type requires a value")
			}
			declaredType = args[i+1] //nolint:gosec // guarded by the i+1 >= len(args) check above
			i++
			continue
		case strings.HasPrefix(arg, "--type="):
			declaredType = strings.TrimPrefix(arg, "--type=")
			continue
		case strings.HasPrefix(arg, "-"):
			return "", "", fmt.Errorf("unknown flag %s (put the remote command after --)", arg)
		default:
			command = joinCommandWords(args[i:], literalWords)
		}
		break
	}
	if command == "" {
		return "", "", fmt.Errorf("no command given")
	}
	return declaredType, command, nil
}

// joinCommandWords rebuilds the one command string from argv the local shell has
// already split.
//
// A single word *is* that command string and is passed through untouched — it is the
// documented form for every DSL opsctl forwards to (`-- "SELECT * FROM users"`), and
// quoting it would hand the database a literal `'SELECT * FROM users'`. This holds
// regardless of literalWords: a single argv word never gets re-quoted.
//
// Two or more words are argv, and every consumer re-splits the result with a real
// shell parser (cmdline.Words underneath both the extension flag DSL and the
// k8s/etcd/kafka canonicalizers, a remote shell for ssh) — so how much of each word's
// original shape must survive that re-split depends on what the re-split feeds into:
//
//   - literalWords == false (every non-extension type, ssh included): the re-split
//     result is handed to something that itself behaves like a shell (ssh(1), or a
//     canonicalizer speaking a Unix-y command grammar), so a bare metacharacter like
//     `*` or `&` is meant to keep meaning what it means to a shell. Only words
//     containing whitespace carry a boundary the join would otherwise destroy; the
//     rest are emitted bare, so `-- ls *.log` still reaches the remote shell as a
//     glob, as ssh(1) does.
//   - literalWords == true (an extension asset): the re-split result is the
//     extension's flag DSL (internal/extreg/command.go), which has no shell and no
//     use for shell operators — a value like `--path=/x?a=1&b=2` (one argv word this
//     process already received intact) must come back out of the re-split as that
//     exact word, `&` included, not as a background operator splitting the command in
//     two. Every word is therefore quoted whenever it needs to be (cmdline.QuoteIfNeeded
//     leaves an already-safe word bare), independent of whitespace.
func joinCommandWords(words []string, literalWords bool) string {
	if len(words) == 1 {
		return words[0]
	}
	joined := make([]string, len(words))
	for i, word := range words {
		if literalWords || strings.ContainsAny(word, " \t\n") {
			joined[i] = cmdline.QuoteIfNeeded(word)
			continue
		}
		joined[i] = word
	}
	return strings.Join(joined, " ")
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

// rejectExtraArgs 报告子命令消费不了的多余参数并返回 true。静默忽略会让写错位置的
// flag（--delete-assets、--mfa-code 等）悄悄失效。
func rejectExtraArgs(extra []string) bool {
	if len(extra) == 0 {
		return false
	}
	fmt.Fprintf(os.Stderr, "Error: unexpected argument(s): %s\n", strings.Join(extra, " "))
	return true
}
