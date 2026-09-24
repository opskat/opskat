package extension

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParseManifest(t *testing.T) {
	Convey("ParseManifest reads the security contract and nothing else", t, func() {
		Convey("should parse a valid manifest", func() {
			data := []byte(`{
				"name": "oss",
				"version": "1.0.0",
				"minAppVersion": "1.2.0",
				"hostABI": "2.0",
				"backend": {
					"runtime": "wasm",
					"binary": "main.wasm"
				},
				"capabilities": {
					"http": { "allowlist": ["https://oss.example.com/"] },
					"credentials": "read",
					"tunnel": true
				}
			}`)

			m, err := ParseManifest(data)
			So(err, ShouldBeNil)
			So(m.Name, ShouldEqual, "oss")
			So(m.Version, ShouldEqual, "1.0.0")
			So(m.MinAppVersion, ShouldEqual, "1.2.0")
			So(m.HostABI, ShouldEqual, "2.0")
			So(m.Backend.Runtime, ShouldEqual, "wasm")
			So(m.Backend.Binary, ShouldEqual, "main.wasm")
			So(m.Capabilities.HTTP.Allowlist, ShouldResemble, []string{"https://oss.example.com/"})
			So(m.Capabilities.Credentials, ShouldEqual, CredentialAccessRead)
			So(m.Capabilities.Tunnel, ShouldBeTrue)

			Convey("and leaves the functional face to describe()", func() {
				So(m.Tools, ShouldBeEmpty)
				So(m.AssetTypes, ShouldBeEmpty)
				So(m.Policies.Type, ShouldBeEmpty)
			})
		})

		Convey("should reject a manifest that still declares what moved into describe()", func() {
			// Silently ignoring the block is what lets a stale declaration keep looking
			// authoritative; the extension has to be rebuilt, so say so.
			for _, key := range retiredManifestKeys {
				data := []byte(`{"name":"x","version":"1.0.0","hostABI":"2.0",` +
					`"backend":{"runtime":"wasm","binary":"main.wasm"},"` + key + `":{}}`)
				_, err := ParseManifest(data)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, key)
				So(err.Error(), ShouldContainSubstring, "describe()")
			}
		})

		Convey("should reject an extension built against the 1.x ABI at parse time", func() {
			// A 1.x module exports neither opskat_call nor describe(); refusing it here
			// names the fix instead of surfacing a missing export on first use.
			data := []byte(`{"name":"x","version":"1.0.0","hostABI":"1.0","backend":{"runtime":"wasm","binary":"main.wasm"}}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "hostABI")
			So(err.Error(), ShouldContainSubstring, "2.0")
		})

		Convey("should reject manifest missing required fields", func() {
			data := []byte(`{"version": "1.0.0"}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "name")
		})

		Convey("should reject invalid minAppVersion", func() {
			data := []byte(`{"name": "x", "version": "1.0.0", "minAppVersion": "invalid", "hostABI":"2.0"}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "minAppVersion")
		})

		Convey("should reject manifest missing hostABI", func() {
			data := []byte(`{"name": "x", "version": "1.0.0"}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "hostABI is required")
		})

		Convey("should reject manifest with unsupported hostABI", func() {
			data := []byte(`{"name": "x", "version": "1.0.0", "hostABI": "9.9"}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not supported")
		})

		Convey("should reject manifest with invalid name characters", func() {
			data := []byte(`{"name": "../../etc/passwd", "version": "1.0.0", "hostABI":"2.0"}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "name must match")
		})

		Convey("should reject manifest with uppercase name", func() {
			data := []byte(`{"name": "MyExt", "version": "1.0.0", "hostABI":"2.0"}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "name must match")
		})

		Convey("should accept valid name characters", func() {
			data := []byte(`{"name": "my-ext_1", "version": "1.0.0", "hostABI":"2.0", "backend":{"runtime":"wasm","binary":"main.wasm"}}`)
			_, err := ParseManifest(data)
			So(err, ShouldBeNil)
		})

		Convey("should reject a manifest that names no wasm binary", func() {
			data := []byte(`{"name": "x", "version": "1.0.0", "hostABI": "2.0"}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "backend.binary")
		})

		Convey("should reject invalid credentials capability", func() {
			data := []byte(`{
				"name": "x", "version": "1.0.0",
				"hostABI": "2.0",
				"backend": {"runtime": "wasm", "binary": "main.wasm"},
				"capabilities": { "credentials": "write" }
			}`)
			_, err := ParseManifest(data)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "credentials")
		})
	})
}

func TestCapabilityChecks(t *testing.T) {
	Convey("Capability checks", t, func() {
		extDir := "/var/ext/test-ext"

		Convey("FS read — deny by default", func() {
			m := &Manifest{Capabilities: Capabilities{FS: FSCapability{}}}
			So(m.CheckFSRead("/tmp/foo.txt", extDir), ShouldNotBeNil)
		})

		Convey("FS read — allow within ${EXT_DIR}", func() {
			m := &Manifest{Capabilities: Capabilities{FS: FSCapability{Read: []string{"${EXT_DIR}/**"}}}}
			So(m.CheckFSRead("/var/ext/test-ext/data/foo.txt", extDir), ShouldBeNil)
		})

		Convey("FS read — deny outside ${EXT_DIR}", func() {
			m := &Manifest{Capabilities: Capabilities{FS: FSCapability{Read: []string{"${EXT_DIR}/**"}}}}
			So(m.CheckFSRead("/etc/passwd", extDir), ShouldNotBeNil)
			So(m.CheckFSRead("/var/ext/other-ext/foo.txt", extDir), ShouldNotBeNil)
		})

		Convey("FS read — allow explicit absolute path prefix", func() {
			m := &Manifest{Capabilities: Capabilities{FS: FSCapability{Read: []string{"/tmp/allowed/**"}}}}
			So(m.CheckFSRead("/tmp/allowed/foo.txt", extDir), ShouldBeNil)
			So(m.CheckFSRead("/tmp/other/foo.txt", extDir), ShouldNotBeNil)
		})

		Convey("FS read — reject path traversal", func() {
			m := &Manifest{Capabilities: Capabilities{FS: FSCapability{Read: []string{"/tmp/**"}}}}
			// After filepath.Abs, traversal resolves; verify it's blocked.
			err := m.CheckFSRead("/tmp/../etc/passwd", extDir)
			So(err, ShouldNotBeNil)
		})

		Convey("FS write — separate capability", func() {
			m := &Manifest{Capabilities: Capabilities{FS: FSCapability{
				Read:  []string{"${EXT_DIR}/**"},
				Write: []string{"${EXT_DIR}/data/**"},
			}}}
			So(m.CheckFSWrite("/var/ext/test-ext/data/foo.txt", extDir), ShouldBeNil)
			So(m.CheckFSWrite("/var/ext/test-ext/config.json", extDir), ShouldNotBeNil) // read-only area
		})

		Convey("HTTP URL — deny by default", func() {
			m := &Manifest{}
			So(m.CheckHTTPURL("https://api.example.com/v1/foo", false), ShouldNotBeNil)
		})

		Convey("HTTP URL — allow explicit prefix", func() {
			m := &Manifest{Capabilities: Capabilities{HTTP: HTTPCapability{
				Allowlist: []string{"https://api.example.com/"},
			}}}
			So(m.CheckHTTPURL("https://api.example.com/v1/foo", false), ShouldBeNil)
			So(m.CheckHTTPURL("https://evil.example.com/", false), ShouldNotBeNil)
		})

		Convey("HTTP URL — reject RFC1918 without tunnel", func() {
			m := &Manifest{Capabilities: Capabilities{HTTP: HTTPCapability{
				Allowlist: []string{"http://10.0.0.1/"},
			}}}
			err := m.CheckHTTPURL("http://10.0.0.1/foo", false)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "private")
		})

		Convey("HTTP URL — reject loopback", func() {
			m := &Manifest{Capabilities: Capabilities{HTTP: HTTPCapability{
				Allowlist: []string{"http://127.0.0.1/"},
			}}}
			So(m.CheckHTTPURL("http://127.0.0.1/foo", false), ShouldNotBeNil)
			So(m.CheckHTTPURL("http://localhost/foo", false), ShouldNotBeNil)
		})

		Convey("HTTP URL — reject link-local metadata", func() {
			m := &Manifest{Capabilities: Capabilities{HTTP: HTTPCapability{
				Allowlist: []string{"http://169.254.169.254/"},
			}}}
			err := m.CheckHTTPURL("http://169.254.169.254/latest/meta-data/", false)
			So(err, ShouldNotBeNil)
		})

		Convey("HTTP URL — allow private when tunnel enabled", func() {
			m := &Manifest{Capabilities: Capabilities{
				HTTP:   HTTPCapability{Allowlist: []string{"http://10.0.0.1/"}},
				Tunnel: true,
			}}
			So(m.CheckHTTPURL("http://10.0.0.1/foo", true), ShouldBeNil)
		})

		Convey("HTTP URL — reject non-http scheme", func() {
			m := &Manifest{Capabilities: Capabilities{HTTP: HTTPCapability{
				Allowlist: []string{"file:///etc/"},
			}}}
			So(m.CheckHTTPURL("file:///etc/passwd", false), ShouldNotBeNil)
		})

		Convey("Credentials — deny by default", func() {
			m := &Manifest{}
			So(m.CheckCredentialRead(), ShouldNotBeNil)
		})

		Convey("Credentials — allow when declared", func() {
			m := &Manifest{Capabilities: Capabilities{Credentials: "read"}}
			So(m.CheckCredentialRead(), ShouldBeNil)
		})

		Convey("Tunnel — deny by default", func() {
			m := &Manifest{}
			So(m.CheckTunnel(), ShouldNotBeNil)
		})

		Convey("Tunnel — allow when declared", func() {
			m := &Manifest{Capabilities: Capabilities{Tunnel: true}}
			So(m.CheckTunnel(), ShouldBeNil)
		})
	})
}
