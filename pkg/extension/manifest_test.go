package extension

import (
	"testing"

	"github.com/opskat/opskat/pkg/extmanifest"

	. "github.com/smartystreets/goconvey/convey"
)

func TestPermissionChecks(t *testing.T) {
	Convey("Permission checks", t, func() {
		extDir := "/var/ext/test-ext"

		Convey("FS read — deny by default", func() {
			m := &Manifest{Manifest: extmanifest.Manifest{Permissions: extmanifest.Permissions{FS: extmanifest.FSPermission{}}}}
			So(m.CheckFSRead("/tmp/foo.txt", extDir), ShouldNotBeNil)
		})

		Convey("FS read — allow within ${EXT_DIR}", func() {
			m := &Manifest{Manifest: extmanifest.Manifest{
				Permissions: extmanifest.Permissions{FS: extmanifest.FSPermission{Read: []string{"${EXT_DIR}/**"}}},
			}}
			So(m.CheckFSRead("/var/ext/test-ext/data/foo.txt", extDir), ShouldBeNil)
		})

		Convey("FS read — deny outside ${EXT_DIR}", func() {
			m := &Manifest{Manifest: extmanifest.Manifest{
				Permissions: extmanifest.Permissions{FS: extmanifest.FSPermission{Read: []string{"${EXT_DIR}/**"}}},
			}}
			So(m.CheckFSRead("/etc/passwd", extDir), ShouldNotBeNil)
			So(m.CheckFSRead("/var/ext/other-ext/foo.txt", extDir), ShouldNotBeNil)
		})

		Convey("FS read — allow explicit absolute path prefix", func() {
			m := &Manifest{Manifest: extmanifest.Manifest{
				Permissions: extmanifest.Permissions{FS: extmanifest.FSPermission{Read: []string{"/tmp/allowed/**"}}},
			}}
			So(m.CheckFSRead("/tmp/allowed/foo.txt", extDir), ShouldBeNil)
			So(m.CheckFSRead("/tmp/other/foo.txt", extDir), ShouldNotBeNil)
		})

		Convey("FS read — reject path traversal", func() {
			m := &Manifest{Manifest: extmanifest.Manifest{
				Permissions: extmanifest.Permissions{FS: extmanifest.FSPermission{Read: []string{"/tmp/**"}}},
			}}
			// After filepath.Abs, traversal resolves; verify it's blocked.
			err := m.CheckFSRead("/tmp/../etc/passwd", extDir)
			So(err, ShouldNotBeNil)
		})

		Convey("FS write — separate permission", func() {
			m := &Manifest{
				Manifest: extmanifest.Manifest{
					Permissions: extmanifest.Permissions{
						FS: extmanifest.FSPermission{
							Read:  []string{"${EXT_DIR}/**"},
							Write: []string{"${EXT_DIR}/data/**"},
						},
					},
				},
			}
			So(m.CheckFSWrite("/var/ext/test-ext/data/foo.txt", extDir), ShouldBeNil)
			So(m.CheckFSWrite("/var/ext/test-ext/config.json", extDir), ShouldNotBeNil) // read-only area
		})

		Convey("HTTP URL — deny by default", func() {
			m := &Manifest{}
			So(m.CheckHTTPURL("https://api.example.com/v1/foo", false), ShouldNotBeNil)
		})

		Convey("HTTP URL — allow explicit prefix", func() {
			m := &Manifest{
				Manifest: extmanifest.Manifest{
					Permissions: extmanifest.Permissions{Network: extmanifest.NetworkPermission{HTTP: []string{"https://api.example.com/"}}},
				},
			}
			So(m.CheckHTTPURL("https://api.example.com/v1/foo", false), ShouldBeNil)
			So(m.CheckHTTPURL("https://evil.example.com/", false), ShouldNotBeNil)
		})

		Convey("HTTP URL — reject RFC1918 without allowPrivate", func() {
			m := &Manifest{
				Manifest: extmanifest.Manifest{
					Permissions: extmanifest.Permissions{Network: extmanifest.NetworkPermission{HTTP: []string{"http://10.0.0.1/"}}},
				},
			}
			err := m.CheckHTTPURL("http://10.0.0.1/foo", false)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "private")
		})

		Convey("HTTP URL — reject loopback", func() {
			m := &Manifest{
				Manifest: extmanifest.Manifest{
					Permissions: extmanifest.Permissions{Network: extmanifest.NetworkPermission{HTTP: []string{"http://127.0.0.1/"}}},
				},
			}
			So(m.CheckHTTPURL("http://127.0.0.1/foo", false), ShouldNotBeNil)
			So(m.CheckHTTPURL("http://localhost/foo", false), ShouldNotBeNil)
		})

		Convey("HTTP URL — reject link-local metadata", func() {
			m := &Manifest{
				Manifest: extmanifest.Manifest{
					Permissions: extmanifest.Permissions{Network: extmanifest.NetworkPermission{HTTP: []string{"http://169.254.169.254/"}}},
				},
			}
			err := m.CheckHTTPURL("http://169.254.169.254/latest/meta-data/", false)
			So(err, ShouldNotBeNil)
		})

		Convey("HTTP URL — allow private with allowPrivate", func() {
			m := &Manifest{
				Manifest: extmanifest.Manifest{
					Permissions: extmanifest.Permissions{
						Network: extmanifest.NetworkPermission{HTTP: []string{"http://10.0.0.1/"}, AllowPrivate: true},
					},
				},
			}
			So(m.CheckHTTPURL("http://10.0.0.1/foo", true), ShouldBeNil)
		})

		Convey("HTTP URL — reject non-http scheme", func() {
			m := &Manifest{
				Manifest: extmanifest.Manifest{
					Permissions: extmanifest.Permissions{Network: extmanifest.NetworkPermission{HTTP: []string{"file:///etc/"}}},
				},
			}
			So(m.CheckHTTPURL("file:///etc/passwd", false), ShouldNotBeNil)
		})

		Convey("Credentials — deny by default", func() {
			m := &Manifest{}
			So(m.CheckCredentialRead(), ShouldNotBeNil)
		})

		Convey("Credentials — allow when declared", func() {
			m := &Manifest{Manifest: extmanifest.Manifest{Permissions: extmanifest.Permissions{Credentials: "read"}}}
			So(m.CheckCredentialRead(), ShouldBeNil)
		})
	})
}
