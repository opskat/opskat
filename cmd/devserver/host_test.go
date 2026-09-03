// cmd/devserver/host_test.go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/pkg/extension"
	. "github.com/smartystreets/goconvey/convey"
)

func TestDevServerHostProvider(t *testing.T) {
	Convey("DevServerHostProvider", t, func() {
		dir := t.TempDir()

		Convey("GetAssetConfig reads from config file", func() {
			cfgFile := filepath.Join(dir, "config.json")
			_ = os.WriteFile(cfgFile, []byte(`{"endpoint":"https://oss.example.com"}`), 0644)

			h := NewDevServerHost(dir)

			cfg, err := h.GetAssetConfig(0)
			So(err, ShouldBeNil)

			var out map[string]string
			_ = json.Unmarshal(cfg, &out)
			So(out["endpoint"], ShouldEqual, "https://oss.example.com")
		})

		Convey("KVGet/KVSet round-trips", func() {
			h := NewDevServerHost(dir)

			err := h.KVSet("test-key", []byte("test-value"))
			So(err, ShouldBeNil)

			val, err := h.KVGet("test-key")
			So(err, ShouldBeNil)
			So(string(val), ShouldEqual, "test-value")
		})

	})
}

// TestDevServerHostEnforcesCapabilities pins the thing that made devserver
// misleading: it used to hand the raw host to LoadPlugin, so an extension could
// read anything during development and only hit "permission denied" once it was
// installed into the real app — the one place the failure is expensive.
func TestDevServerHostEnforcesCapabilities(t *testing.T) {
	Convey("Given a manifest whose fs.read is scoped to the extension directory", t, func() {
		extDir := t.TempDir()
		dataDir := t.TempDir()
		outsideDir := t.TempDir()

		manifest, err := extension.ParseManifest([]byte(`{
			"name": "demo",
			"version": "1.0.0",
			"hostABI": "1.0",
			"capabilities": {"fs": {"read": ["${EXT_DIR}/**"]}},
			"backend": {"runtime": "wasm", "binary": "main.wasm"},
			"assetTypes": [{"type": "demo", "i18n": {"name": "Demo"}, "configSchema": {"type": "object", "properties": {"endpoint": {"type": "string"}}}}],
			"policies": {"type": "demo"}
		}`))
		So(err, ShouldBeNil)

		_, host := newExtensionHost(manifest, extDir, dataDir)

		Convey("a read inside the extension directory is allowed", func() {
			path := filepath.Join(extDir, "ok.txt")
			So(os.WriteFile(path, []byte("x"), 0o600), ShouldBeNil)
			res, err := host.OpenIO(extension.IOOpenParams{Type: "file", Path: path, Mode: "read"})
			So(err, ShouldBeNil)
			So(res.Closer.Close(), ShouldBeNil)
		})

		Convey("a read outside it is denied, same as in the packaged app", func() {
			path := filepath.Join(outsideDir, "secret.txt")
			So(os.WriteFile(path, []byte("x"), 0o600), ShouldBeNil)
			_, err := host.OpenIO(extension.IOOpenParams{Type: "file", Path: path, Mode: "read"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "fs read denied")
		})
	})
}
