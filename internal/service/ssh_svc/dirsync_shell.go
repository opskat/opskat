package ssh_svc

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

func detectRemoteShell(client *ssh.Client) (string, string) {
	session, err := client.NewSession()
	if err != nil {
		return "/bin/sh", shellTypeUnsupported
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil && closeErr != io.EOF {
			logger.Default().Warn("close shell probe session", zap.Error(closeErr))
		}
	}()

	var out bytes.Buffer
	session.Stdout = &out
	session.Stderr = io.Discard
	if err := session.Run(`sh -lc 'printf "%s" "${SHELL:-/bin/sh}"'`); err != nil {
		return "/bin/sh", shellTypeUnsupported
	}

	shellPath := strings.TrimSpace(out.String())
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	return shellPath, normalizeShellType(shellPath)
}

func normalizeShellType(shellPath string) string {
	switch path.Base(shellPath) {
	case "bash":
		return shellTypeBash
	case "zsh":
		return shellTypeZsh
	case "ksh":
		return shellTypeKsh
	case "mksh":
		return shellTypeMksh
	default:
		return shellTypeUnsupported
	}
}

func generateSyncToken() (string, error) {
	buf := make([]byte, syncSequenceTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func buildEnableSyncScript(shellType, syncToken, promptNonce string) string {
	switch shellType {
	case shellTypeBash:
		return fmt.Sprintf(
			`opskat_next_prompt_nonce(){ local n r;n=$(date +%%s%%N 2>/dev/null||date +%%s 2>/dev/null||printf 0);r=${RANDOM:-0};printf '%%s-%%s-%%s' "$$" "$r" "$n";}
`+
				`opskat_prompt_proof(){ local p c x;c=${OPSKAT_PROMPT_NONCE:-};[ -n "$c" ]||return;x=$(opskat_next_prompt_nonce);p=$(builtin pwd -P 2>/dev/null||builtin pwd 2>/dev/null||printf '');printf '\033]1337;opskat:%s:prompt:%%s:%%s:%%s\007' "$c" "$x" "$p";OPSKAT_PROMPT_NONCE=$x;}
`+
				`OPSKAT_PROMPT_NONCE=%s
`+
				`PROMPT_COMMAND="opskat_prompt_proof${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
`+
				`printf '\033]1337;opskat:%s:init:pid:%%s\007' "$$"
`,
			syncToken, shellQuote(promptNonce), syncToken)
	case shellTypeZsh:
		return fmt.Sprintf(
			`opskat_next_prompt_nonce(){ local n r;n=$(date +%%s%%N 2>/dev/null||date +%%s 2>/dev/null||printf 0);r=${RANDOM:-0};printf '%%s-%%s-%%s' "$$" "$r" "$n";}
`+
				`opskat_prompt_proof(){ local p c x;c=${OPSKAT_PROMPT_NONCE:-};[[ -n "$c" ]]||return;x=$(opskat_next_prompt_nonce);p=$(pwd -P 2>/dev/null||pwd 2>/dev/null||printf '');printf '\033]1337;opskat:%s:prompt:%%s:%%s:%%s\007' "$c" "$x" "$p";OPSKAT_PROMPT_NONCE=$x;}
`+
				`OPSKAT_PROMPT_NONCE=%s
`+
				`autoload -Uz add-zsh-hook
`+
				`add-zsh-hook precmd opskat_prompt_proof
`+
				`printf '\033]1337;opskat:%s:init:pid:%%s\007' "$$"
`,
			syncToken, shellQuote(promptNonce), syncToken)
	case shellTypeKsh, shellTypeMksh:
		return fmt.Sprintf(
			`opskat_next_prompt_nonce(){ OPSKAT_NOW=$(date +%%s%%N 2>/dev/null||date +%%s 2>/dev/null||printf 0);OPSKAT_RAND=${RANDOM:-0};printf '%%s-%%s-%%s' "$$" "$OPSKAT_RAND" "$OPSKAT_NOW";}
`+
				`opskat_prompt_proof(){ OPSKAT_CURRENT=${OPSKAT_PROMPT_NONCE:-};[ -n "$OPSKAT_CURRENT" ]||return;OPSKAT_NEXT=$(opskat_next_prompt_nonce);OPSKAT_PWD=$(pwd -P 2>/dev/null||pwd 2>/dev/null||printf '');printf '\033]1337;opskat:%s:prompt:%%s:%%s:%%s\007' "$OPSKAT_CURRENT" "$OPSKAT_NEXT" "$OPSKAT_PWD";OPSKAT_PROMPT_NONCE=$OPSKAT_NEXT;}
`+
				`OPSKAT_PROMPT_NONCE=%s
`+
				`OPSKAT_ORIG_PS1=${OPSKAT_ORIG_PS1:-$PS1}
`+
				`PS1='$(opskat_prompt_proof)'"$OPSKAT_ORIG_PS1"
`+
				`printf '\033]1337;opskat:%s:init:pid:%%s\007' "$$"
`,
			syncToken, shellQuote(promptNonce), syncToken)
	default:
		return ""
	}
}

// syncInjectionWrapper is the short, shell-agnostic command line that installs
// the directory-sync hook. It disables echo, reads the base64 hook from the
// NEXT input line via `read` (so that long payload is consumed with echo off
// and is never echoed — no wrapping, nothing to suppress), evals it in the
// current shell, then restores echo. Only this one short line echoes, and the
// byte-level suppressor hides it. Sent as a bare command (no heredoc) so the
// shell emits no PS2 continuation prompts.
const syncInjectionWrapper = `stty -echo 2>/dev/null;IFS= read -r OPSKAT_SYNC_B;` +
	`eval "$(printf %s "$OPSKAT_SYNC_B"|base64 -d)" 2>/dev/null;` +
	`stty echo 2>/dev/null;unset OPSKAT_SYNC_B` + "\r"

// buildEnableSyncInjection returns the two lines to write to install the hook:
// the wrapper command line, then the base64 hook payload the wrapper's `read`
// consumes. Returns ("","") for unsupported shells. The caller must write the
// wrapper first, let it reach the `read`, then write the payload (see EnableSync).
func buildEnableSyncInjection(shellType, syncToken, promptNonce string) (wrapper, payload string) {
	script := buildEnableSyncScript(shellType, syncToken, promptNonce)
	if script == "" {
		return "", ""
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	return syncInjectionWrapper, encoded + "\r"
}

// buildDisableSyncCommand returns a one-line statement that removes the hook
// and unsets helper functions. Safe to send even if EnableSync was never run.
func buildDisableSyncCommand(shellType string) string {
	switch shellType {
	case shellTypeBash:
		return `PROMPT_COMMAND=${PROMPT_COMMAND#opskat_prompt_proof};PROMPT_COMMAND=${PROMPT_COMMAND#;};unset -f opskat_prompt_proof opskat_next_prompt_nonce 2>/dev/null;unset OPSKAT_PROMPT_NONCE` + "\r"
	case shellTypeZsh:
		return `add-zsh-hook -d precmd opskat_prompt_proof 2>/dev/null;unset -f opskat_prompt_proof opskat_next_prompt_nonce 2>/dev/null;unset OPSKAT_PROMPT_NONCE` + "\r"
	case shellTypeKsh, shellTypeMksh:
		return `[ -n "$OPSKAT_ORIG_PS1" ] && PS1=$OPSKAT_ORIG_PS1;unset -f opskat_prompt_proof opskat_next_prompt_nonce 2>/dev/null;unset OPSKAT_PROMPT_NONCE OPSKAT_ORIG_PS1` + "\r"
	default:
		return ""
	}
}

func buildDirectoryChangeCommand(targetPath string) string {
	return fmt.Sprintf("builtin cd -- %s\r", shellQuote(targetPath))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
