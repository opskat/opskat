package command

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/cmdline"

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

func TestExtractCommand(t *testing.T) {
	Convey("extractCommand", t, func() {
		Convey("should extract command after --", func() {
			cmd := extractCommand([]string{"--", "uptime"})
			So(cmd, ShouldEqual, "uptime")

			cmd = extractCommand([]string{"--", "ls", "-la", "/var/log"})
			So(cmd, ShouldEqual, "ls -la /var/log")

			cmd = extractCommand([]string{"--", "cat", "/etc/hosts"})
			So(cmd, ShouldEqual, "cat /etc/hosts")
		})

		Convey("should join all args without --", func() {
			cmd := extractCommand([]string{"uptime"})
			So(cmd, ShouldEqual, "uptime")

			cmd = extractCommand([]string{"ls", "-la"})
			So(cmd, ShouldEqual, "ls -la")
		})

		Convey("should return empty for no command", func() {
			cmd := extractCommand([]string{})
			So(cmd, ShouldEqual, "")

			cmd = extractCommand([]string{"--"})
			So(cmd, ShouldEqual, "")
		})

		Convey("should use first -- only", func() {
			cmd := extractCommand([]string{"--", "echo", "--", "hello"})
			So(cmd, ShouldEqual, "echo -- hello")
		})

		Convey("should survive a re-split by the shell parser downstream", func() {
			// The user's shell already split the command into argv; every consumer of
			// the joined string re-splits it with a real shell parser (the extension
			// flag DSL and the k8s/etcd/kafka canonicalizers via cmdline.Words, a
			// remote shell for ssh). A plain join loses the quoting the shell removed,
			// so a value with a space silently becomes two words.
			cmd := extractCommand([]string{"--", "grep", "foo bar", "file"})
			words, err := cmdline.Words(cmd)
			So(err, ShouldBeNil)
			So(words, ShouldResemble, []string{"grep", "foo bar", "file"})

			cmd = extractCommand([]string{"--", "note_put", "--content=restart via systemctl"})
			words, err = cmdline.Words(cmd)
			So(err, ShouldBeNil)
			So(words, ShouldResemble, []string{"note_put", "--content=restart via systemctl"})
		})

		Convey("should leave shell metacharacters alone when the word has no whitespace", func() {
			// Only the word boundaries the local shell consumed need re-encoding.
			// A glob carries no lost boundary, and ssh(1) itself lets it reach the
			// remote shell — quoting it here would silently change what a working
			// `opsctl exec host -- ls *.log` does.
			So(extractCommand([]string{"--", "ls", "*.log"}), ShouldEqual, "ls *.log")
			So(extractCommand([]string{"--", "grep", "foo bar", "*.log"}), ShouldEqual, "grep 'foo bar' *.log")
		})

		Convey("should keep a lone word verbatim", func() {
			// One word after "--" *is* the command string — the documented form for
			// every DSL opsctl forwards to (`opsctl exec prod-db -- "SELECT * FROM t"`).
			// Quoting it would hand the database a literal `'SELECT * FROM t'`.
			So(extractCommand([]string{"--", "SELECT * FROM users"}), ShouldEqual, "SELECT * FROM users")
			So(extractCommand([]string{"ls | wc -l"}), ShouldEqual, "ls | wc -l")
		})
	})
}

func TestExtractTypeFlag(t *testing.T) {
	Convey("extractTypeFlag", t, func() {
		Convey("should extract --type <value> before --", func() {
			declared, rest := extractTypeFlag([]string{"--type", "database", "--", "PING"})
			So(declared, ShouldEqual, "database")
			So(rest, ShouldResemble, []string{"--", "PING"})
		})

		Convey("should extract --type=<value> before --", func() {
			declared, rest := extractTypeFlag([]string{"--type=redis", "--", "GET", "k"})
			So(declared, ShouldEqual, "redis")
			So(rest, ShouldResemble, []string{"--", "GET", "k"})
		})

		Convey("should return empty declared type and unchanged args when absent", func() {
			declared, rest := extractTypeFlag([]string{"--", "uptime"})
			So(declared, ShouldEqual, "")
			So(rest, ShouldResemble, []string{"--", "uptime"})
		})

		Convey("should not treat --type after -- as the flag", func() {
			// Everything past "--" belongs to the command, never to opsctl itself —
			// same contract as extractCommand.
			declared, rest := extractTypeFlag([]string{"--", "ls", "--type"})
			So(declared, ShouldEqual, "")
			So(rest, ShouldResemble, []string{"--", "ls", "--type"})
		})

		Convey("should leave a dangling --type (no value) for downstream handling", func() {
			declared, rest := extractTypeFlag([]string{"--type"})
			So(declared, ShouldEqual, "")
			So(rest, ShouldResemble, []string{"--type"})
		})

		Convey("should return empty declared type and unchanged args for empty input", func() {
			declared, rest := extractTypeFlag([]string{})
			So(declared, ShouldEqual, "")
			So(rest, ShouldResemble, []string{})
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
