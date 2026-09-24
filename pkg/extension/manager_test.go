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

// stubWasm is a valid but empty WASM module: enough to compile, not a reactor.
// Tests that only exercise manager bookkeeping pair it with a canned descriptor in
// the cache, so no guest ever has to run.
var stubWasm = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// cannedDescriptor is the describe() answer the fake cache serves for stubWasm.
const cannedDescriptor = `{"i18n":{"displayName":"d","description":"x"},` +
	`"assetTypes":[{"type":"stub","i18n":{"name":"n"},` +
	`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
	`"policies":{"type":"stub"}}`

// fakeDescribeCache answers for every extension with the same canned descriptor,
// keyed to the hash of whatever wasm bytes it was built with.
type fakeDescribeCache struct {
	hash    string
	payload string
	stored  map[string]string // extension name → hash written back
	loads   int
}

func newFakeDescribeCache(wasmBytes []byte, payload string) *fakeDescribeCache {
	return &fakeDescribeCache{hash: WasmHash(wasmBytes), payload: payload, stored: map[string]string{}}
}

func (c *fakeDescribeCache) LoadDescriptor(string) (string, []byte, error) {
	c.loads++
	return c.hash, []byte(c.payload), nil
}

func (c *fakeDescribeCache) StoreDescriptor(name, hash string, _ []byte) error {
	c.stored[name] = hash
	return nil
}

func (c *fakeDescribeCache) DeleteDescriptor(name string) error {
	delete(c.stored, name)
	return nil
}

// useDescribeCache installs a descriptor cache for the duration of one test.
func useDescribeCache(t *testing.T, c DescribeCache) {
	t.Helper()
	SetDescribeCache(c)
	t.Cleanup(func() { SetDescribeCache(nil) })
}

// writeMinimalExtension writes a manifest.json + a stub WASM module for extName
// under extDir, without any SKILL.md.
func writeMinimalExtension(t *testing.T, extDir, extName string) {
	t.Helper()
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatalf("mkdir extension dir: %v", err)
	}
	// The manifest is the security contract only: asset types, tools and the policy
	// face come from describe() (here: the cache).
	manifest := map[string]any{
		"name":    extName,
		"version": "1.0.0",
		"hostABI": HostABIVersion,
		"backend": map[string]any{"runtime": "wasm", "binary": "main.wasm"},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "main.wasm"), stubWasm, 0644); err != nil {
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

		useDescribeCache(t, newFakeDescribeCache(stubWasm, cannedDescriptor))
		mgr := NewManager(dir, newHost, logger)

		Convey("Scan with no extensions", func() {
			exts, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(exts, ShouldBeEmpty)
		})

		Convey("Scan discovers valid extension", func() {
			writeMinimalExtension(t, filepath.Join(dir, "test-ext"), "test-ext")

			exts, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(len(exts), ShouldEqual, 1)
			So(exts[0].Name, ShouldEqual, "test-ext")

			Convey("and its functional face is merged in from describe()", func() {
				So(exts[0].AssetTypes, ShouldHaveLength, 1)
				So(exts[0].AssetTypes[0].Type, ShouldEqual, "stub")
				So(exts[0].Policies.Type, ShouldEqual, "stub")
			})
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

// extensionsDirEntries lists what the manager's extensions directory holds besides
// the compilation cache — staging leftovers would show up here.
func extensionsDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read extensions dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name() == ".cache" {
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

// Installing over an installed extension is how every rebuild during `opsctl ext
// dev` lands. A new build that does not load must leave the running version exactly
// where it was: installed, loaded and callable.
func TestManagerInstall(t *testing.T) {
	Convey("Manager.Install", t, func() {
		ctx := context.Background()
		dir := t.TempDir()
		logger := zap.NewNop()
		newHost := func(string) HostProvider {
			return NewDefaultHostProvider(DefaultHostConfig{Logger: logger})
		}
		useDescribeCache(t, newFakeDescribeCache(stubWasm, cannedDescriptor))
		mgr := NewManager(dir, newHost, logger)
		Reset(func() { mgr.Close(ctx) })

		src := t.TempDir()
		v1 := filepath.Join(src, "v1")
		writeMinimalExtension(t, v1, "hot")
		_, err := mgr.Install(ctx, v1)
		So(err, ShouldBeNil)
		old := mgr.GetExtension("hot")
		So(old, ShouldNotBeNil)

		Convey("a new build that fails to load keeps the old version installed and loaded", func() {
			broken := filepath.Join(src, "broken")
			writeMinimalExtension(t, broken, "hot")
			So(os.WriteFile(filepath.Join(broken, "main.wasm"), []byte("not wasm"), 0644), ShouldBeNil)

			_, err := mgr.Install(ctx, broken)
			So(err, ShouldNotBeNil)

			So(mgr.GetExtension("hot") == old, ShouldBeTrue)
			So(old.Plugin.closed.Load(), ShouldBeFalse)
			onDisk, err := os.ReadFile(filepath.Join(dir, "hot", "main.wasm")) //nolint:gosec // test TempDir
			So(err, ShouldBeNil)
			So(onDisk, ShouldResemble, stubWasm)
			So(extensionsDirEntries(t, dir), ShouldResemble, []string{"hot"})
		})

		Convey("a successful upgrade replaces the old version and closes it", func() {
			v2 := filepath.Join(src, "v2")
			writeMinimalExtension(t, v2, "hot")
			manifest, err := os.ReadFile(filepath.Join(v2, "manifest.json")) //nolint:gosec // test TempDir
			So(err, ShouldBeNil)
			So(os.WriteFile(filepath.Join(v2, "manifest.json"), //nolint:gosec // test TempDir
				[]byte(strings.Replace(string(manifest), `"1.0.0"`, `"2.0.0"`, 1)), 0644), ShouldBeNil)

			m, err := mgr.Install(ctx, v2)
			So(err, ShouldBeNil)
			So(m.Version, ShouldEqual, "2.0.0")

			cur := mgr.GetExtension("hot")
			So(cur != old, ShouldBeTrue)
			So(cur.Manifest.Version, ShouldEqual, "2.0.0")
			So(cur.Dir, ShouldEqual, filepath.Join(dir, "hot"))
			So(old.Plugin.closed.Load(), ShouldBeTrue)
			So(cur.Plugin.closed.Load(), ShouldBeFalse)
			info, err := LoadManifestInfo(filepath.Join(dir, "hot"))
			So(err, ShouldBeNil)
			So(info.Manifest.Version, ShouldEqual, "2.0.0")
			So(extensionsDirEntries(t, dir), ShouldResemble, []string{"hot"})
		})

		Convey("a fresh install that fails to load leaves nothing behind", func() {
			broken := filepath.Join(src, "fresh")
			writeMinimalExtension(t, broken, "fresh")
			So(os.WriteFile(filepath.Join(broken, "main.wasm"), []byte("not wasm"), 0644), ShouldBeNil)

			_, err := mgr.Install(ctx, broken)
			So(err, ShouldNotBeNil)
			So(mgr.GetExtension("fresh"), ShouldBeNil)
			So(extensionsDirEntries(t, dir), ShouldResemble, []string{"hot"})
		})
	})
}
