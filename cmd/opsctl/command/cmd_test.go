package command

import (
	"encoding/json"
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

func TestGrantSessionFlagParsing(t *testing.T) {
	Convey("--grant-session flag 解析", t, func() {
		Convey("有 --grant-session 时正确提取", func() {
			args := []string{"--grant-session", "abc-123", "web-server", "--", "uptime"}
			var grantSession string
			remaining := args
			if len(remaining) >= 2 && remaining[0] == "--grant-session" {
				grantSession = remaining[1]
				remaining = remaining[2:]
			}
			So(grantSession, ShouldEqual, "abc-123")
			So(remaining, ShouldResemble, []string{"web-server", "--", "uptime"})
		})

		Convey("无 --grant-session 时不影响解析", func() {
			args := []string{"web-server", "--", "uptime"}
			var grantSession string
			remaining := args
			if len(remaining) >= 2 && remaining[0] == "--grant-session" {
				grantSession = remaining[1]
				remaining = remaining[2:]
			}
			So(grantSession, ShouldEqual, "")
			So(remaining, ShouldResemble, []string{"web-server", "--", "uptime"})
		})
	})
}

func TestGrantInputParsing(t *testing.T) {
	Convey("grant JSON 输入解析", t, func() {
		Convey("有效 JSON", func() {
			input := `{"description":"test grant","items":[{"type":"exec","asset":"web-01","command":"uptime"}]}`
			var grant grantInput
			err := json.Unmarshal([]byte(input), &grant)
			So(err, ShouldBeNil)
			So(grant.Description, ShouldEqual, "test grant")
			So(len(grant.Items), ShouldEqual, 1)
			So(grant.Items[0].Type, ShouldEqual, "exec")
			So(grant.Items[0].Asset, ShouldEqual, "web-01")
			So(grant.Items[0].Command, ShouldEqual, "uptime")
		})

		Convey("多项授权", func() {
			input := `{"description":"deploy","items":[
				{"type":"exec","asset":"web-01","command":"systemctl stop app"},
				{"type":"cp","asset":"web-01","detail":"upload config"},
				{"type":"exec","asset":"web-01","command":"systemctl start app"}
			]}`
			var grant grantInput
			err := json.Unmarshal([]byte(input), &grant)
			So(err, ShouldBeNil)
			So(len(grant.Items), ShouldEqual, 3)
			So(grant.Items[0].Type, ShouldEqual, "exec")
			So(grant.Items[1].Type, ShouldEqual, "cp")
			So(grant.Items[2].Command, ShouldEqual, "systemctl start app")
		})

		Convey("空 items", func() {
			input := `{"description":"empty","items":[]}`
			var grant grantInput
			err := json.Unmarshal([]byte(input), &grant)
			So(err, ShouldBeNil)
			So(len(grant.Items), ShouldEqual, 0)
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
