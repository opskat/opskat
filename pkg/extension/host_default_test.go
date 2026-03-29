// pkg/extension/host_default_test.go
package extension

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
)

func TestDefaultHostProvider(t *testing.T) {
	Convey("DefaultHostProvider", t, func() {
		logger, _ := zap.NewDevelopment()
		host := NewDefaultHostProvider(DefaultHostConfig{
			Logger: logger,
		})
		defer host.CloseAll()

		Convey("IOOpen file read", func() {
			dir := t.TempDir()
			path := filepath.Join(dir, "test.txt")
			os.WriteFile(path, []byte("content"), 0644)

			id, meta, err := host.IOOpen(IOOpenParams{Type: "file", Path: path, Mode: "read"})
			So(err, ShouldBeNil)
			So(id, ShouldBeGreaterThan, 0)
			So(meta.Size, ShouldEqual, 7)

			data, err := host.IORead(id, 100)
			So(err, ShouldBeNil)
			So(string(data), ShouldEqual, "content")

			So(host.IOClose(id), ShouldBeNil)
		})

		Convey("IOOpen file write", func() {
			dir := t.TempDir()
			path := filepath.Join(dir, "out.txt")

			id, _, err := host.IOOpen(IOOpenParams{Type: "file", Path: path, Mode: "write"})
			So(err, ShouldBeNil)

			n, err := host.IOWrite(id, []byte("output"))
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 6)

			So(host.IOClose(id), ShouldBeNil)
			data, _ := os.ReadFile(path)
			So(string(data), ShouldEqual, "output")
		})

		Convey("IOOpen unknown type returns error", func() {
			_, _, err := host.IOOpen(IOOpenParams{Type: "unknown"})
			So(err, ShouldNotBeNil)
		})

		Convey("Log does not panic", func() {
			host.Log("info", "test message")
			host.Log("error", "test error")
		})

		Convey("unconfigured services return errors", func() {
			_, err := host.GetCredential(1)
			So(err, ShouldNotBeNil)

			_, err = host.GetAssetConfig(1)
			So(err, ShouldNotBeNil)

			_, err = host.KVGet("key")
			So(err, ShouldNotBeNil)
		})
	})
}
