package extension

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// writeMinimalExtension writes a manifest.json + minimal valid WASM module for
// extName under extDir, without any SKILL.md.
func writeMinimalExtension(t *testing.T, extDir, extName string) {
	t.Helper()
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatalf("mkdir extension dir: %v", err)
	}
	manifest := map[string]any{
		"name":    extName,
		"version": "1.0.0",
		"hostABI": "1.0",
		"backend": map[string]any{"runtime": "wasm", "binary": "main.wasm"},
		// assetTypes + policies.type 是加载期强制项（Manifest.validateAssetScope）：
		// 扩展工具只能经资产上的 exec 抵达，没有资产类型就没有入口。
		"assetTypes": []any{map[string]any{
			"type": extName, "i18n": map[string]any{"name": "n"},
			"configSchema": map[string]any{"type": "object", "properties": map[string]any{"endpoint": map[string]any{"type": "string"}}},
		}},
		"policies": map[string]any{"type": extName},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "main.wasm"),
		[]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, 0644); err != nil {
		t.Fatalf("write main.wasm: %v", err)
	}
}

func TestManager(t *testing.T) {
	Convey("Manager", t, func() {
		ctx := context.Background()
		dir := t.TempDir()
		logger := zap.NewNop()

		newHost := func(extName string) HostProvider {
			return NewDefaultHostProvider(DefaultHostConfig{Logger: logger})
		}

		mgr := NewManager(dir, newHost, logger)

		Convey("Scan with no extensions", func() {
			exts, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(exts, ShouldBeEmpty)
		})

		Convey("Scan discovers valid extension", func() {
			extDir := filepath.Join(dir, "test-ext")
			_ = os.MkdirAll(extDir, 0755)

			manifest := map[string]any{
				"name":    "test-ext",
				"version": "1.0.0",
				"hostABI": "1.0",
				"backend": map[string]any{"runtime": "wasm", "binary": "main.wasm"},
				"assetTypes": []any{map[string]any{
					"type": "test-ext", "i18n": map[string]any{"name": "n"},
					"configSchema": map[string]any{"type": "object", "properties": map[string]any{"endpoint": map[string]any{"type": "string"}}},
				}},
				"policies": map[string]any{"type": "test-ext"},
			}
			data, _ := json.Marshal(manifest)
			So(os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644), ShouldBeNil)

			// Minimal valid WASM module
			So(os.WriteFile(filepath.Join(extDir, "main.wasm"),
				[]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, 0644), ShouldBeNil)

			exts, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(len(exts), ShouldEqual, 1)
			So(exts[0].Name, ShouldEqual, "test-ext")
		})

		Convey("Scan skips directories without manifest", func() {
			_ = os.MkdirAll(filepath.Join(dir, "no-manifest"), 0755)

			exts, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(exts, ShouldBeEmpty)
		})

		Convey("GetExtension returns nil for unknown", func() {
			ext := mgr.GetExtension("nonexistent")
			So(ext, ShouldBeNil)
		})

		Convey("LoadExtension parses a compliant SKILL.md into body + description", func() {
			extDir := filepath.Join(dir, "with-skill")
			writeMinimalExtension(t, extDir, "with-skill")
			raw := "---\nname: with-skill\ndescription: \"Test extension with a skill doc.\"\n---\n\n# With Skill\n\nSome body content.\n"
			So(os.WriteFile(filepath.Join(extDir, "SKILL.md"), []byte(raw), 0644), ShouldBeNil)

			_, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldBeNil)

			ext := mgr.GetExtension("with-skill")
			So(ext, ShouldNotBeNil)
			So(ext.SkillMD, ShouldEqual, "# With Skill\n\nSome body content.\n")
			So(ext.SkillDescription, ShouldEqual, "Test extension with a skill doc.")
		})

		Convey("LoadExtension succeeds when SKILL.md is absent", func() {
			extDir := filepath.Join(dir, "no-skill")
			writeMinimalExtension(t, extDir, "no-skill")

			_, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldBeNil)

			ext := mgr.GetExtension("no-skill")
			So(ext, ShouldNotBeNil)
			So(ext.SkillMD, ShouldEqual, "")
			So(ext.SkillDescription, ShouldEqual, "")
		})

		Convey("LoadExtension tolerates a SKILL.md with no frontmatter at all", func() {
			// Extension SKILL.md predates the frontmatter convention -- the published
			// extensions/oss/SKILL.md is exactly this shape (starts with
			// "# OSS Object Storage", no frontmatter block). We cannot retroactively
			// edit that separate repo, so this boundary must keep accepting bare
			// Markdown rather than hard-failing the load.
			extDir := filepath.Join(dir, "bare-skill")
			writeMinimalExtension(t, extDir, "bare-skill")
			raw := "# Just a heading\n\nNo frontmatter here.\n"
			So(os.WriteFile(filepath.Join(extDir, "SKILL.md"), []byte(raw), 0644), ShouldBeNil)

			_, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldBeNil)

			ext := mgr.GetExtension("bare-skill")
			So(ext, ShouldNotBeNil)
			So(ext.SkillMD, ShouldEqual, raw)
			So(ext.SkillDescription, ShouldEqual, "")
		})

		Convey("LoadExtension warns when SKILL.md has no frontmatter", func() {
			// The tolerate branch above degrades silently on success (err == nil,
			// empty SkillDescription) -- there was previously no way to tell from the
			// logs that a given extension's SKILL.md fell back to raw body text.
			// ScanManifests logs a Warn on its sibling silent-failure path
			// ("skip extension manifest"); this asserts the same for LoadExtension's
			// degrade branch.
			core, logs := observer.New(zap.WarnLevel)
			obsMgr := NewManager(dir, newHost, zap.New(core))

			extDir := filepath.Join(dir, "bare-skill-warn")
			writeMinimalExtension(t, extDir, "bare-skill-warn")
			raw := "# Just a heading\n\nNo frontmatter here.\n"
			So(os.WriteFile(filepath.Join(extDir, "SKILL.md"), []byte(raw), 0644), ShouldBeNil)

			_, err := obsMgr.LoadExtension(ctx, extDir)
			So(err, ShouldBeNil)

			entries := logs.FilterMessageSnippet("SKILL.md").All()
			So(len(entries), ShouldEqual, 1)
			So(entries[0].Level, ShouldEqual, zap.WarnLevel)
			So(entries[0].ContextMap()["extension"], ShouldEqual, "bare-skill-warn")
		})

		Convey("LoadExtension fails when SKILL.md has a malformed frontmatter block", func() {
			extDir := filepath.Join(dir, "bad-skill")
			writeMinimalExtension(t, extDir, "bad-skill")
			// Opens a frontmatter block but never closes it -- a real authoring
			// mistake (as opposed to no frontmatter at all) that must still fail loudly.
			So(os.WriteFile(filepath.Join(extDir, "SKILL.md"), []byte("---\nname: bad-skill\n"), 0644), ShouldBeNil)

			_, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldNotBeNil)
			So(mgr.GetExtension("bad-skill"), ShouldBeNil)
		})

		Convey("LoadExtension accepts a SKILL.md larger than the old 4 KiB cap", func() {
			extDir := filepath.Join(dir, "big-skill")
			writeMinimalExtension(t, extDir, "big-skill")
			body := strings.Repeat("x", 6*1024)
			raw := "---\nname: big-skill\ndescription: \"A big skill doc.\"\n---\n\n" + body + "\n"
			So(os.WriteFile(filepath.Join(extDir, "SKILL.md"), []byte(raw), 0644), ShouldBeNil)

			_, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldBeNil)

			ext := mgr.GetExtension("big-skill")
			So(ext, ShouldNotBeNil)
			So(len(ext.SkillMD), ShouldBeGreaterThan, 4*1024)
		})

		Reset(func() {
			mgr.Close(ctx)
		})
	})
}
