package ssh_svc

import (
	"encoding/base64"
	"strings"
	"testing"
)

func assertInjection(t *testing.T, wrapper, payload string) {
	t.Helper()
	if strings.ContainsAny(wrapper, "\n") {
		t.Fatalf("wrapper must be single-line (no newline -> no PS2): %q", wrapper)
	}
	if !strings.HasSuffix(wrapper, "\r") {
		t.Fatalf("wrapper must end with a carriage return: %q", wrapper)
	}
	// The readable hook source must never appear in the wrapper; it rides in the
	// base64 payload (which the shell reads with echo off). Regression: "sftp
	// open terminal echo noise".
	if strings.Contains(wrapper, "opskat_prompt_proof") {
		t.Fatalf("hook source leaked into visible wrapper: %q", wrapper)
	}
	if !strings.Contains(wrapper, "read -r OPSKAT_SYNC_B") || !strings.Contains(wrapper, "stty -echo") {
		t.Fatalf("expected read-with-echo-off injection wrapper: %q", wrapper)
	}
	if !strings.HasSuffix(payload, "\r") {
		t.Fatalf("payload must end with a carriage return: %q", payload)
	}
}

func decodeInjectionHook(t *testing.T, payload string) string {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(payload, "\r"))
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	return string(decoded)
}

func TestBuildEnableSyncInjectionBash(t *testing.T) {
	wrapper, payload := buildEnableSyncInjection(shellTypeBash, "TOK", "NONCE")
	assertInjection(t, wrapper, payload)
	hook := decodeInjectionHook(t, payload)
	if !strings.Contains(hook, `PROMPT_COMMAND="opskat_prompt_proof`) {
		t.Fatalf("missing PROMPT_COMMAND wiring in hook: %s", hook)
	}
	if !strings.Contains(hook, "OPSKAT_PROMPT_NONCE='NONCE'") {
		t.Fatalf("missing nonce assignment (shellQuote'd) in hook: %s", hook)
	}
	if !strings.Contains(hook, `1337;opskat:TOK:init:pid:`) {
		t.Fatalf("missing init marker in hook: %s", hook)
	}
}

func TestBuildEnableSyncInjectionZsh(t *testing.T) {
	wrapper, payload := buildEnableSyncInjection(shellTypeZsh, "TOK", "NONCE")
	assertInjection(t, wrapper, payload)
	hook := decodeInjectionHook(t, payload)
	if !strings.Contains(hook, "add-zsh-hook precmd opskat_prompt_proof") {
		t.Fatalf("missing add-zsh-hook in hook: %s", hook)
	}
}

func TestBuildEnableSyncInjectionKsh(t *testing.T) {
	wrapper, payload := buildEnableSyncInjection(shellTypeKsh, "TOK", "NONCE")
	assertInjection(t, wrapper, payload)
	hook := decodeInjectionHook(t, payload)
	if !strings.Contains(hook, `PS1='$(opskat_prompt_proof)'`) {
		t.Fatalf("missing PS1 wiring in hook: %s", hook)
	}
}

func TestBuildEnableSyncInjectionUnsupported(t *testing.T) {
	if w, p := buildEnableSyncInjection(shellTypeUnsupported, "T", "N"); w != "" || p != "" {
		t.Fatalf("unsupported shell must return empty, got: %q %q", w, p)
	}
}

func TestBuildDisableSyncCommand(t *testing.T) {
	for _, sh := range []string{shellTypeBash, shellTypeZsh, shellTypeKsh, shellTypeMksh} {
		got := buildDisableSyncCommand(sh)
		if got == "" || !strings.HasSuffix(got, "\r") {
			t.Fatalf("disable cmd for %s should be non-empty and end in \\r, got %q", sh, got)
		}
		if strings.ContainsAny(got, "\n") {
			t.Fatalf("must be single line: %q", got)
		}
	}
	if buildDisableSyncCommand(shellTypeUnsupported) != "" {
		t.Fatal("unsupported should return empty")
	}
}
