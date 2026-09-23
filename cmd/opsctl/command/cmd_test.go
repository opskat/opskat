package command

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestParseRemotePath(t *testing.T) {
	Convey("parseRemotePath", t, func() {
		Convey("should parse valid remote paths", func() {
			id, path := parseRemotePath("1:/etc/hosts")
			So(id, ShouldEqual, 1)
			So(path, ShouldEqual, "/etc/hosts")

			id, path = parseRemotePath("42:/var/log/app.log")
			So(id, ShouldEqual, 42)
			So(path, ShouldEqual, "/var/log/app.log")

			id, path = parseRemotePath("100:/tmp/file with spaces.txt")
			So(id, ShouldEqual, 100)
			So(path, ShouldEqual, "/tmp/file with spaces.txt")
		})

		Convey("should return 0 for local paths", func() {
			id, path := parseRemotePath("./local-file.txt")
			So(id, ShouldEqual, 0)
			So(path, ShouldEqual, "./local-file.txt")

			id, path = parseRemotePath("/absolute/path")
			So(id, ShouldEqual, 0)
			So(path, ShouldEqual, "/absolute/path")

			id, path = parseRemotePath("relative.txt")
			So(id, ShouldEqual, 0)
			So(path, ShouldEqual, "relative.txt")
		})

		Convey("should handle edge cases", func() {
			// Colon at start (no ID)
			id, path := parseRemotePath(":/path")
			So(id, ShouldEqual, 0)
			So(path, ShouldEqual, ":/path")

			// Non-numeric before colon
			id, path = parseRemotePath("abc:/path")
			So(id, ShouldEqual, 0)
			So(path, ShouldEqual, "abc:/path")

			// Empty string
			id, path = parseRemotePath("")
			So(id, ShouldEqual, 0)
			So(path, ShouldEqual, "")

			// Windows-like path (C:\path) should not parse as remote
			id, path = parseRemotePath("C:\\Users\\file")
			So(id, ShouldEqual, 0)
			So(path, ShouldEqual, "C:\\Users\\file")
		})
	})
}

func TestCpPathParsing(t *testing.T) {
	Convey("cp path classification", t, func() {
		Convey("upload: local -> remote", func() {
			srcID, _ := parseRemotePath("./file.txt")
			dstID, dstPath := parseRemotePath("1:/tmp/file.txt")
			So(srcID, ShouldEqual, 0)
			So(dstID, ShouldEqual, 1)
			So(dstPath, ShouldEqual, "/tmp/file.txt")
		})

		Convey("download: remote -> local", func() {
			srcID, srcPath := parseRemotePath("1:/tmp/file.txt")
			dstID, _ := parseRemotePath("./file.txt")
			So(srcID, ShouldEqual, 1)
			So(srcPath, ShouldEqual, "/tmp/file.txt")
			So(dstID, ShouldEqual, 0)
		})

		Convey("asset-to-asset: remote -> remote", func() {
			srcID, srcPath := parseRemotePath("1:/etc/config")
			dstID, dstPath := parseRemotePath("2:/tmp/config")
			So(srcID, ShouldEqual, 1)
			So(srcPath, ShouldEqual, "/etc/config")
			So(dstID, ShouldEqual, 2)
			So(dstPath, ShouldEqual, "/tmp/config")
		})

		Convey("error: local -> local", func() {
			srcID, _ := parseRemotePath("./a.txt")
			dstID, _ := parseRemotePath("./b.txt")
			So(srcID, ShouldEqual, 0)
			So(dstID, ShouldEqual, 0)
			// Both are local → should error in cmdCp
		})
	})
}
