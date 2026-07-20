package ai

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/runner"
)

func TestBuiltinAssetTypeSkillsForTabs(t *testing.T) {
	t.Run("known built-in type is included with its skills.Description", func(t *testing.T) {
		got := builtinAssetTypeSkillsForTabs([]runner.TabInfo{
			{Type: "redis", AssetID: 1, AssetName: "cache"},
		})
		desc, ok := got["redis"]
		if !ok {
			t.Fatalf("expected redis to be included, got %v", got)
		}
		if desc == "" {
			t.Fatal("expected non-empty description for redis")
		}
	})

	t.Run("unknown / extension-only type is not included", func(t *testing.T) {
		got := builtinAssetTypeSkillsForTabs([]runner.TabInfo{
			{Type: "mongodb", AssetID: 1, AssetName: "docs"},
		})
		if _, ok := got["mongodb"]; ok {
			t.Fatalf("mongodb has no built-in skills.Description, must not be included, got %v", got)
		}
	})

	t.Run("duplicate tab types are deduped", func(t *testing.T) {
		got := builtinAssetTypeSkillsForTabs([]runner.TabInfo{
			{Type: "ssh", AssetID: 1, AssetName: "a"},
			{Type: "ssh", AssetID: 2, AssetName: "b"},
		})
		if len(got) != 1 {
			t.Fatalf("expected 1 entry after dedup, got %d: %v", len(got), got)
		}
	})

	t.Run("no open tabs returns empty map", func(t *testing.T) {
		got := builtinAssetTypeSkillsForTabs(nil)
		if len(got) != 0 {
			t.Fatalf("expected empty map, got %v", got)
		}
	})
}
