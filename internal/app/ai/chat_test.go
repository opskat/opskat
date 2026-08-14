package ai

import (
	"errors"
	"strings"
	"testing"
)

// TestAllBuiltinAssetTypeSkills covers the listing that feeds PromptBuilder's per-type
// discovery section. It is deliberately independent of openTabs: the listing exists so
// the model learns that help(asset) exists and which types exec covers, which a session
// with no matching tab open needs just as much. (It does not satisfy the exec doc gate —
// only an explicit help call does; see internal/ai/tool.DocGate.)
// TestSafeOutwardError locks the app chat outward-error sink to raw semantics (spec task
// 1, "面向用户的 chat error 也保持原文"): the error events chat.go emits to the frontend
// carry provider/tool error text verbatim, byte-for-byte identical to the source error.
// Synthetic PEM / Authorization / signature forms must survive unchanged.
func TestSafeOutwardError(t *testing.T) {
	secretPem := "out-\n-----BEGIN PRIVATE KEY-----\nout-key-body\n-----END PRIVATE KEY-----"
	raw := "request failed: " + secretPem + "; Authorization: Bearer out-token"
	out := safeOutwardError(errors.New(raw))
	for _, secret := range []string{"out-key-body", "out-token"} {
		if !strings.Contains(out, secret) {
			t.Fatalf("outward error lost raw text %q: %s", secret, out)
		}
	}
	if !strings.Contains(out, "request failed") {
		t.Fatalf("outward error lost safe context: %s", out)
	}
	if out != raw {
		t.Fatalf("outward error must be byte-for-byte identical to the source, got %q", out)
	}

	if plain := safeOutwardError(errors.New("connection timeout")); plain != "connection timeout" {
		t.Fatalf("non-secret error must pass through unchanged, got %q", plain)
	}
}

func TestSafeOutwardFailurePreservesRawTextOnEventAndReturnedWailsError(t *testing.T) {
	err := errors.New("Authorization: Basic wails-return-secret")
	message, returned := safeOutwardFailure("send to LLM", err)
	for name, text := range map[string]string{"event": message, "returned error": returned.Error()} {
		if !strings.Contains(text, "wails-return-secret") {
			t.Fatalf("%s lost the raw provider error: %s", name, text)
		}
	}
	if !strings.Contains(returned.Error(), "send to LLM") {
		t.Fatalf("returned Wails error lost its operation context: %s", returned)
	}
	if returned.Error() != "send to LLM: "+err.Error() {
		t.Fatalf("returned Wails error must be prefix + raw error text: %s", returned)
	}
}

func TestAllBuiltinAssetTypeSkills(t *testing.T) {
	t.Run("every built-in type is included, with no tabs involved", func(t *testing.T) {
		got := allBuiltinAssetTypeSkills()
		// The 8 exec types (with "## Command syntax") plus the 4 doc-only types
		// (rdp/vnc/oss/local, registered via permission.RegisterHelpDoc — config-only,
		// no command surface) all have a skills.Description and must be discoverable
		// here: the listing's job is "the model learns help(asset) exists", which is
		// just as true for a doc-only type as for one with an executor.
		for _, want := range []string{
			"ssh", "serial", "database", "redis", "k8s", "etcd", "mongodb", "kafka",
			"rdp", "vnc", "oss", "local",
		} {
			desc, ok := got[want]
			if !ok {
				t.Fatalf("expected %q to be included, got %v", want, got)
			}
			if desc == "" {
				t.Fatalf("expected non-empty description for %q", want)
			}
		}
	})

	t.Run("a type with no embedded SKILL.md is not included", func(t *testing.T) {
		// "bogus" is not a registered asset type and never will be, so it has no
		// internal/ai/skills entry and must not appear in the listing — the listing is
		// derived from the embedded SKILL.md set, not from the asset-type registry.
		// (vnc used to stand in here; it now has a doc-only SKILL.md — see
		// internal/ai/skills/vnc — so it moved to the "included" case above.)
		got := allBuiltinAssetTypeSkills()
		if _, ok := got["bogus"]; ok {
			t.Fatalf("bogus is not a real asset type; must not be included, got %v", got)
		}
	})
}
