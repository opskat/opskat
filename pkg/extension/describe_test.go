package extension

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
)

// installFixture writes the fixture extension (slim manifest + real reactor
// module) into a fresh extensions dir and returns the dir and the extension dir.
func installFixture(t *testing.T) (root, extDir string) {
	t.Helper()
	root = t.TempDir()
	extDir = filepath.Join(root, "fixture-ext")
	if err := os.MkdirAll(extDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("testdata/fixture-ext/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "manifest.json"), manifest, 0644); err != nil { //nolint:gosec // paths are built from the test's own TempDir
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "main.wasm"), fixtureWasm(t), 0644); err != nil {
		t.Fatal(err)
	}
	return root, extDir
}

func newFixtureManager(t *testing.T, root string) *Manager {
	t.Helper()
	logger := zap.NewNop()
	mgr := NewManager(root, func(string) HostProvider {
		return NewDefaultHostProvider(DefaultHostConfig{Logger: logger})
	}, logger)
	t.Cleanup(func() { mgr.Shutdown(context.Background()) })
	return mgr
}

// TestLoadExtensionReadsTheFunctionalFaceFromTheGuest is the end-to-end claim of
// this design: the fixture's manifest.json declares no tools, no asset types and no
// policy face at all, and the extension still loads, registers and answers calls.
func TestLoadExtensionReadsTheFunctionalFaceFromTheGuest(t *testing.T) {
	Convey("Given an extension whose manifest carries only capabilities", t, func() {
		ctx := context.Background()
		root, extDir := installFixture(t)
		mgr := newFixtureManager(t, root)

		onDisk, err := os.ReadFile(filepath.Join(extDir, "manifest.json")) //nolint:gosec // path built from the test's own TempDir
		So(err, ShouldBeNil)
		var raw map[string]any
		So(json.Unmarshal(onDisk, &raw), ShouldBeNil)
		So(raw, ShouldNotContainKey, "tools")
		So(raw, ShouldNotContainKey, "assetTypes")
		So(raw, ShouldNotContainKey, "policies")

		manifest, err := mgr.LoadExtension(ctx, extDir)
		So(err, ShouldBeNil)

		Convey("describe() supplies the tools, with the schemas reflected in the guest", func() {
			byName := map[string]ToolDef{}
			for _, tool := range manifest.Tools {
				byName[tool.Name] = tool
			}
			So(byName, ShouldContainKey, "echo")
			So(byName["echo"].PolicyAction, ShouldEqual, "read")
			So(byName["echo"].I18n.Description, ShouldEqual, "Echo the arguments back")
			props := byName["echo"].Parameters["properties"].(map[string]any)
			So(props["msg"], ShouldResemble, map[string]any{
				"type": "string", "description": "Message to echo back",
			})
			So(byName["write_file"].PolicyAction, ShouldEqual, "write")
		})

		Convey("and the asset type, policy face and groups the host registers on", func() {
			So(manifest.AssetTypes, ShouldHaveLength, 1)
			So(manifest.AssetTypes[0].Type, ShouldEqual, "fixture")
			So(ConfigSchemaProperties(manifest.AssetTypes[0].ConfigSchema), ShouldResemble, []string{"endpoint"})
			So(manifest.Policies.Type, ShouldEqual, "fixture")
			So(manifest.Policies.Actions, ShouldResemble, []string{"read", "write"})
			So(manifest.Policies.Default, ShouldResemble, []string{"ext:fixture:read"})
			So(manifest.Policies.Groups, ShouldHaveLength, 1)
		})

		Convey("and the tools it declared are callable", func() {
			ext := mgr.GetExtension("fixture-ext")
			So(ext, ShouldNotBeNil)
			out, err := ext.Plugin.CallTool(ctx, "echo", json.RawMessage(`{"msg":"hi"}`), nil)
			So(err, ShouldBeNil)
			So(string(out), ShouldContainSubstring, `"msg":"hi"`)
		})
	})
}

// TestLoadExtensionRefusesAModuleThatIsNotAReactor covers the other half of the
// ABI gate: the manifest may claim hostABI 2.0, but a module that cannot answer
// describe() is refused while loading rather than on the first tool call.
func TestLoadExtensionRefusesAModuleThatIsNotAReactor(t *testing.T) {
	Convey("An extension whose module is not a reactor fails to load", t, func() {
		root, extDir := installFixture(t)
		So(os.WriteFile(filepath.Join(extDir, "main.wasm"), stubWasm, 0644), ShouldBeNil)
		mgr := newFixtureManager(t, root)

		_, err := mgr.LoadExtension(context.Background(), extDir)
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "describe extension")
		So(err.Error(), ShouldContainSubstring, "reactor SDK")
		So(mgr.GetExtension("fixture-ext"), ShouldBeNil)
	})
}

// sentinelDescriptor names a tool the fixture does not have, so a manifest that
// ends up carrying it can only have come from the cache.
const sentinelDescriptor = `{"i18n":{"displayName":"cached","description":"c"},` +
	`"assetTypes":[{"type":"fixture","i18n":{"name":"n"},` +
	`"configSchema":{"type":"object","properties":{"endpoint":{"type":"string"}}}}],` +
	`"policies":{"type":"fixture"},` +
	`"tools":[{"name":"from_cache","policyAction":"read","parameters":{"type":"object","properties":{}}}]}`

func TestDescribeIsCachedByWasmHash(t *testing.T) {
	Convey("Given an extension whose descriptor is already cached", t, func() {
		ctx := context.Background()
		root, extDir := installFixture(t)

		Convey("a cache entry for this exact binary is used instead of the guest", func() {
			cache := newFakeDescribeCache(fixtureWasm(t), sentinelDescriptor)
			useDescribeCache(t, cache)
			mgr := newFixtureManager(t, root)

			manifest, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldBeNil)
			So(manifest.Tools, ShouldHaveLength, 1)
			So(manifest.Tools[0].Name, ShouldEqual, "from_cache")
			So(cache.loads, ShouldEqual, 1)
			So(cache.stored, ShouldBeEmpty) // nothing to write back: the answer was already there
		})

		Convey("a cache entry for a different binary is ignored and refreshed", func() {
			// Same payload, wrong hash: this is what a rebuilt extension looks like.
			cache := newFakeDescribeCache([]byte("some other build"), sentinelDescriptor)
			useDescribeCache(t, cache)
			mgr := newFixtureManager(t, root)

			manifest, err := mgr.LoadExtension(ctx, extDir)
			So(err, ShouldBeNil)
			names := make([]string, 0, len(manifest.Tools))
			for _, tool := range manifest.Tools {
				names = append(names, tool.Name)
			}
			So(names, ShouldContain, "echo")
			So(names, ShouldNotContain, "from_cache")
			So(cache.stored["fixture-ext"], ShouldEqual, WasmHash(fixtureWasm(t)))
		})

		Convey("a disabled extension is listed from the cache without running any WASM", func() {
			cache := newFakeDescribeCache(fixtureWasm(t), sentinelDescriptor)
			useDescribeCache(t, cache)

			info, err := LoadManifestInfo(extDir)
			So(err, ShouldBeNil)
			So(info.Manifest.I18n.DisplayName, ShouldEqual, "cached")
			So(info.Manifest.AssetTypes, ShouldHaveLength, 1)
			So(info.Manifest.Tools[0].Name, ShouldEqual, "from_cache")
		})
	})
}
