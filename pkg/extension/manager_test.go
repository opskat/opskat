package extension

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"

	cagologger "github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// stubWasm is a valid but empty WASM module: enough to compile, not a reactor.
// Tests that only exercise manager bookkeeping pair it with a canned descriptor in
// the cache, so no guest ever has to run.
var stubWasm = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// cannedDescriptor is the describe() answer the fake cache serves for stubWasm.
const cannedDescriptor = `{"i18n":{"displayName":"d","description":"x"},"contributes":{` +
	`"assetTypes":[{"type":"stub","i18n":{"name":"n"},` +
	`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
	`"policy":{"type":"stub"}}}`

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
		"hostABI": extmanifest.HostABIVersion,
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
		mgr := NewManager(dir, newHost, acceptingContributions(), logger)

		Convey("Scan with no extensions", func() {
			exts, failures, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(exts, ShouldBeEmpty)
			So(failures, ShouldBeEmpty)
		})

		Convey("Scan discovers valid extension", func() {
			writeMinimalExtension(t, filepath.Join(dir, "test-ext"), "test-ext")

			exts, failures, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(failures, ShouldBeEmpty)
			So(len(exts), ShouldEqual, 1)
			So(exts[0].Name, ShouldEqual, "test-ext")

			Convey("and its functional face is merged in from describe()", func() {
				f := faceOf(t, exts[0])
				So(f.AssetTypes, ShouldHaveLength, 1)
				So(f.AssetTypes[0].Type, ShouldEqual, "stub")
				So(f.Policy.Type, ShouldEqual, "stub")
			})
		})

		Convey("Scan skips directories without manifest", func() {
			_ = os.MkdirAll(filepath.Join(dir, "no-manifest"), 0755)

			exts, failures, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(exts, ShouldBeEmpty)
			So(failures, ShouldBeEmpty)
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

		Convey("LoadExtension fails when SKILL.md has no frontmatter", func() {
			// The description the model uses to pick the skill lives in the frontmatter,
			// so a bare Markdown file is refused rather than loaded without one.
			extDir := filepath.Join(dir, "bare-skill")
			writeMinimalExtension(t, extDir, "bare-skill")
			So(os.WriteFile(filepath.Join(extDir, "SKILL.md"), []byte("# Just a heading\n\nNo frontmatter here.\n"), 0644), ShouldBeNil)

			_, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "SKILL.md")
			So(mgr.GetExtension("bare-skill"), ShouldBeNil)
		})

		Convey("LoadExtension fails when SKILL.md has a malformed frontmatter block", func() {
			extDir := filepath.Join(dir, "bad-skill")
			writeMinimalExtension(t, extDir, "bad-skill")
			// Opens a frontmatter block but never closes it -- a real authoring
			// mistake that must fail loudly.
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

		// A locale file that does not parse used to vanish silently, leaving the
		// author staring at raw i18n keys with nothing in the logs. It is an
		// authoring mistake like a broken manifest, so the load fails and says where.
		Convey("LoadExtension fails when a locale file does not parse", func() {
			extDir := filepath.Join(dir, "bad-locale")
			writeMinimalExtension(t, extDir, "bad-locale")
			So(os.MkdirAll(filepath.Join(extDir, "locales"), 0755), ShouldBeNil)
			So(os.WriteFile(filepath.Join(extDir, "locales", "en.json"), []byte(`{"a":"b"}`), 0644), ShouldBeNil)
			So(os.WriteFile(filepath.Join(extDir, "locales", "zh-CN.json"), []byte(`{"a":`), 0644), ShouldBeNil)

			_, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "zh-CN.json")
			So(mgr.GetExtension("bad-locale"), ShouldBeNil)
		})

		// Listing (no runtime) keeps going, but the broken file is reported at
		// error level through the project logger rather than dropped silently.
		Convey("LoadManifestInfo logs a locale file that does not parse", func() {
			core, logs := observer.New(zap.ErrorLevel)
			prev := cagologger.Default()
			cagologger.SetLogger(zap.New(core))
			defer cagologger.SetLogger(prev)

			extDir := filepath.Join(dir, "bad-locale-info")
			writeMinimalExtension(t, extDir, "bad-locale-info")
			So(os.MkdirAll(filepath.Join(extDir, "locales"), 0755), ShouldBeNil)
			So(os.WriteFile(filepath.Join(extDir, "locales", "zh-CN.json"), []byte(`{"a":`), 0644), ShouldBeNil)

			_, err := LoadManifestInfo(extDir, acceptingContributions())
			So(err, ShouldBeNil)
			entries := logs.All()
			So(len(entries), ShouldEqual, 1)
			So(entries[0].ContextMap()["extension"], ShouldEqual, "bad-locale-info")
			So(entries[0].ContextMap()["error"], ShouldContainSubstring, "zh-CN.json")
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
		mgr := NewManager(dir, newHost, acceptingContributions(), logger)
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
			info, err := LoadManifestInfo(filepath.Join(dir, "hot"), acceptingContributions())
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

// The frontend names the language the way i18next does ("zh-CN") while locale
// files are keyed lowercase on load; a lookup must match regardless of case
// instead of silently falling back to English.
func TestTranslateMatchesLanguageCaseInsensitively(t *testing.T) {
	Convey("a locale file named zh-CN.json answers lookups for zh-CN and zh-cn", t, func() {
		dir := t.TempDir()
		So(os.MkdirAll(filepath.Join(dir, "locales"), 0o755), ShouldBeNil)
		So(os.WriteFile(filepath.Join(dir, "locales", "en.json"), []byte(`{"field.address":"Address"}`), 0o644), ShouldBeNil)
		So(os.WriteFile(filepath.Join(dir, "locales", "zh-CN.json"), []byte(`{"field.address":"地址"}`), 0o644), ShouldBeNil)
		locales, err := LoadLocales(dir)
		So(err, ShouldBeNil)
		ext := &Extension{Locales: locales}

		So(ext.Translate("zh-CN", "field.address"), ShouldEqual, "地址")
		So(ext.Translate("zh-cn", "field.address"), ShouldEqual, "地址")
		So(ext.Translate("en", "field.address"), ShouldEqual, "Address")
	})
}

// writeManifestOnly writes a raw manifest.json (and the stub wasm) under dir/name.
func writeManifestOnly(t *testing.T, dir, name, manifest string) {
	t.Helper()
	extDir := filepath.Join(dir, name)
	So(os.MkdirAll(extDir, 0755), ShouldBeNil)
	So(os.WriteFile(filepath.Join(extDir, "manifest.json"), []byte(manifest), 0644), ShouldBeNil)
	So(os.WriteFile(filepath.Join(extDir, "main.wasm"), stubWasm, 0644), ShouldBeNil)
}

// TestInstalledExtensionsTheHostRefuses: an extension on a retired hostABI, one
// in the predecessor shape, and one whose cached describe is refused are reported
// as load failures (not dropped), and none of them is loaded.
func TestInstalledExtensionsTheHostRefuses(t *testing.T) {
	Convey("Installed extensions the host refuses", t, func() {
		ctx := context.Background()
		dir := t.TempDir()
		writeMinimalExtension(t, filepath.Join(dir, "good"), "good")
		writeManifestOnly(t, dir, "legacy-abi",
			`{"name":"legacy-abi","version":"1.2.0","hostABI":"2.3","backend":{"runtime":"wasm","binary":"main.wasm"}}`)
		writeManifestOnly(t, dir, "old-shape",
			`{"name":"old-shape","version":"0.9.0","hostABI":"`+extmanifest.HostABIVersion+`","capabilities":["x"],"assetTypes":[{"type":"t"}],"backend":{"runtime":"wasm","binary":"main.wasm"}}`)
		useDescribeCache(t, newFakeDescribeCache(stubWasm, cannedDescriptor))
		newHost := func(string) HostProvider { return NewDefaultHostProvider(DefaultHostConfig{}) }
		mgr := NewManager(dir, newHost, acceptingContributions(), zap.NewNop())

		byDir := func(fs []LoadFailure) map[string]LoadFailure {
			out := map[string]LoadFailure{}
			for _, f := range fs {
				out[filepath.Base(f.Dir)] = f
			}
			return out
		}

		Convey("ScanInstalled lists them as failures carrying name, version and the error", func() {
			infos, failures, err := mgr.ScanInstalled()
			So(err, ShouldBeNil)
			So(infos, ShouldHaveLength, 1)
			So(infos[0].Name, ShouldEqual, "good")
			f := byDir(failures)
			So(f, ShouldHaveLength, 2)
			var inc *extmanifest.IncompatibleError
			So(errors.As(f["legacy-abi"].Err, &inc), ShouldBeTrue)
			So(inc.HostABI, ShouldEqual, "2.3")
			So(f["legacy-abi"].Name, ShouldEqual, "legacy-abi")
			So(f["legacy-abi"].Version, ShouldEqual, "1.2.0")
			So(errors.As(f["old-shape"].Err, &inc), ShouldBeFalse)
			So(f["old-shape"].Err.Error(), ShouldContainSubstring, "capabilities")
			So(f["old-shape"].Name, ShouldEqual, "old-shape")
		})

		Convey("Scan loads only the good one and reports the same failures", func() {
			exts, failures, err := mgr.Scan(ctx)
			So(err, ShouldBeNil)
			So(exts, ShouldHaveLength, 1)
			So(byDir(failures), ShouldHaveLength, 2)
			So(mgr.GetExtension("legacy-abi"), ShouldBeNil)
			So(mgr.GetExtension("old-shape"), ShouldBeNil)
		})

		Convey("a cached describe a contribution point refuses is a failure too", func() {
			reg := NewContributionRegistry(refusingPoint(extwire.KeyAssetTypes),
				passPoint(extwire.KeyTools), passPoint(extwire.KeyPolicy), passPoint(extwire.KeySnippets),
				passPoint(extwire.KeyPages), passPoint(extwire.KeyDatabaseDrivers))
			m2 := NewManager(dir, newHost, reg, zap.NewNop())
			infos, failures, err := m2.ScanInstalled()
			So(err, ShouldBeNil)
			So(infos, ShouldBeEmpty)
			f := byDir(failures)
			So(f, ShouldHaveLength, 3)
			So(f["good"].Err.Error(), ShouldContainSubstring, "refused by the test's point")
			So(f["good"].Name, ShouldEqual, "good")
		})
	})
}
