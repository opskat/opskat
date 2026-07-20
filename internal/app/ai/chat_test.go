package ai

import (
	"testing"
)

// TestAllBuiltinAssetTypeSkills covers the listing that feeds PromptBuilder's per-type
// discovery section. It is deliberately independent of openTabs: the listing exists so
// the model learns that help(asset) exists and which types exec covers, which a session
// with no matching tab open needs just as much. (It does not satisfy the exec doc gate —
// only an explicit help call does; see internal/ai/tool.DocGate.)
func TestAllBuiltinAssetTypeSkills(t *testing.T) {
	t.Run("every built-in type is included, with no tabs involved", func(t *testing.T) {
		got := allBuiltinAssetTypeSkills()
		for _, want := range []string{"ssh", "serial", "database", "redis", "k8s"} {
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
		// mongodb is reached through an extension, whose SKILL.md rides the separate
		// extension-injection path — it has no internal/ai/skills entry.
		got := allBuiltinAssetTypeSkills()
		if _, ok := got["mongodb"]; ok {
			t.Fatalf("mongodb has no built-in skills.Description, must not be included, got %v", got)
		}
	})
}
