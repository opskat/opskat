package command

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestParseBatchArg(t *testing.T) {
	Convey("parseBatchArg", t, func() {
		Convey("asset:command defaults to exec", func() {
			cmd, err := parseBatchArg("web-01:uptime")
			So(err, ShouldBeNil)
			So(cmd.Type, ShouldEqual, "exec")
			So(cmd.Asset, ShouldEqual, "web-01")
			So(cmd.Command, ShouldEqual, "uptime")
		})

		Convey("numeric asset ID", func() {
			cmd, err := parseBatchArg("1:df -h")
			So(err, ShouldBeNil)
			So(cmd.Type, ShouldEqual, "exec")
			So(cmd.Asset, ShouldEqual, "1")
			So(cmd.Command, ShouldEqual, "df -h")
		})

		Convey("exec:asset:command", func() {
			cmd, err := parseBatchArg("exec:production/web-01:uptime")
			So(err, ShouldBeNil)
			So(cmd.Type, ShouldEqual, "exec")
			So(cmd.Asset, ShouldEqual, "production/web-01")
			So(cmd.Command, ShouldEqual, "uptime")
		})

		Convey("sql:asset:command", func() {
			cmd, err := parseBatchArg("sql:db-01:SELECT COUNT(*) FROM users")
			So(err, ShouldBeNil)
			So(cmd.Type, ShouldEqual, "sql")
			So(cmd.Asset, ShouldEqual, "db-01")
			So(cmd.Command, ShouldEqual, "SELECT COUNT(*) FROM users")
		})

		Convey("redis:asset:command", func() {
			cmd, err := parseBatchArg("redis:cache:PING")
			So(err, ShouldBeNil)
			So(cmd.Type, ShouldEqual, "redis")
			So(cmd.Asset, ShouldEqual, "cache")
			So(cmd.Command, ShouldEqual, "PING")
		})

		Convey("command with colons preserved", func() {
			cmd, err := parseBatchArg("sql:db:SELECT * FROM t WHERE ts > '2024-01-01T00:00:00'")
			So(err, ShouldBeNil)
			So(cmd.Type, ShouldEqual, "sql")
			So(cmd.Asset, ShouldEqual, "db")
			So(cmd.Command, ShouldEqual, "SELECT * FROM t WHERE ts > '2024-01-01T00:00:00'")
		})

		Convey("no colon returns error", func() {
			_, err := parseBatchArg("uptime")
			So(err, ShouldNotBeNil)
		})

		Convey("type prefix without asset:command returns error", func() {
			_, err := parseBatchArg("sql:SELECT 1")
			// "sql" is type, "SELECT 1" has no colon → error
			So(err, ShouldNotBeNil)
		})

		Convey("unknown prefix treated as asset name", func() {
			cmd, err := parseBatchArg("myserver:hostname")
			So(err, ShouldBeNil)
			So(cmd.Type, ShouldEqual, "exec")
			So(cmd.Asset, ShouldEqual, "myserver")
			So(cmd.Command, ShouldEqual, "hostname")
		})
	})
}

func TestParseBatchInput(t *testing.T) {
	Convey("parseBatchInput args mode", t, func() {
		Convey("multiple args", func() {
			cmds, err := parseBatchInput([]string{"1:uptime", "sql:2:SELECT 1", "redis:3:PING"})
			So(err, ShouldBeNil)
			So(len(cmds), ShouldEqual, 3)

			So(cmds[0].Type, ShouldEqual, "exec")
			So(cmds[0].Asset, ShouldEqual, "1")
			So(cmds[0].Command, ShouldEqual, "uptime")

			So(cmds[1].Type, ShouldEqual, "sql")
			So(cmds[1].Asset, ShouldEqual, "2")
			So(cmds[1].Command, ShouldEqual, "SELECT 1")

			So(cmds[2].Type, ShouldEqual, "redis")
			So(cmds[2].Asset, ShouldEqual, "3")
			So(cmds[2].Command, ShouldEqual, "PING")
		})

		Convey("empty args returns nil", func() {
			cmds, err := parseBatchInput([]string{})
			So(err, ShouldBeNil)
			So(cmds, ShouldBeNil)
		})

		Convey("invalid arg returns error", func() {
			_, err := parseBatchInput([]string{"no-colon"})
			So(err, ShouldNotBeNil)
		})
	})
}

func TestBatchInputJSON(t *testing.T) {
	Convey("batchInput JSON deserialization", t, func() {
		Convey("full input", func() {
			data := `{"commands":[
				{"asset":"web-01","type":"exec","command":"uptime"},
				{"asset":"db-01","type":"sql","command":"SELECT 1"},
				{"asset":"cache","type":"redis","command":"PING"}
			]}`
			var input batchInput
			err := json.Unmarshal([]byte(data), &input)
			So(err, ShouldBeNil)
			So(len(input.Commands), ShouldEqual, 3)
			So(input.Commands[0].Type, ShouldEqual, "exec")
			So(input.Commands[1].Type, ShouldEqual, "sql")
			So(input.Commands[2].Type, ShouldEqual, "redis")
		})

		Convey("type defaults to empty (caller fills exec)", func() {
			data := `{"commands":[{"asset":"1","command":"uptime"}]}`
			var input batchInput
			err := json.Unmarshal([]byte(data), &input)
			So(err, ShouldBeNil)
			So(input.Commands[0].Type, ShouldEqual, "")
		})

		Convey("empty commands", func() {
			data := `{"commands":[]}`
			var input batchInput
			err := json.Unmarshal([]byte(data), &input)
			So(err, ShouldBeNil)
			So(len(input.Commands), ShouldEqual, 0)
		})
	})
}

func TestBatchOutputJSON(t *testing.T) {
	Convey("batchOutput JSON serialization", t, func() {
		output := batchOutput{
			Results: []batchResult{
				{AssetID: 1, AssetName: "web-01", Type: "exec", Command: "uptime", ExitCode: 0, Stdout: "up 30 days"},
				{AssetID: 2, AssetName: "db-01", Type: "sql", Command: "SELECT 1", ExitCode: 1, Error: "connection refused"},
			},
		}
		data, err := json.Marshal(output)
		So(err, ShouldBeNil)

		var decoded batchOutput
		err = json.Unmarshal(data, &decoded)
		So(err, ShouldBeNil)
		So(len(decoded.Results), ShouldEqual, 2)
		So(decoded.Results[0].ExitCode, ShouldEqual, 0)
		So(decoded.Results[0].Stdout, ShouldEqual, "up 30 days")
		So(decoded.Results[1].ExitCode, ShouldEqual, 1)
		So(decoded.Results[1].Error, ShouldEqual, "connection refused")
	})
}

// TestBatchAuditToolIsDispatchable 锁住 batch 每一项落到的工具名**在派发表里查得到**。
// batchAuditTool 曾经是个 cmdType→名字的映射函数，现在只剩一个名字；但无论几个，
// executeBatchHandler 都拿它去 buildHandlerMap() 里查 handler，查不到只在运行时打印
// "Internal error: unknown tool"。这条断言把那次运行时失败提前到编译-测试阶段。
func TestBatchAuditToolIsDispatchable(t *testing.T) {
	Convey("batchAuditTool 必须能在 AllToolDefs 派发表里查到", t, func() {
		So(batchAuditTool, ShouldEqual, "exec")
		So(buildHandlerMap(), ShouldContainKey, batchAuditTool)
	})
}

// TestExecuteBatchHandlerMarksPreapproved 锁住 batch 派发时声明了"已预检"。
//
// batch 不经过 callHandler（那里统一标记），而是自己查 handler 直接调。工具侧的权限
// 检查是 fail-closed 的：opsctl 的 context 里没有 PolicyChecker，不声明就会在
// permission.RequireCheckerOrPreapproved 上直接失败，`opsctl batch --type sql` 整个不可用。
// batch 有资格拿这个豁免——Step 3 已经对每条命令跑过 permission.CheckPermission。
func TestExecuteBatchHandlerMarksPreapproved(t *testing.T) {
	Convey("executeBatchHandler 传给 handler 的 ctx 必须通得过 fail-closed 检查", t, func() {
		var checkErr error
		handlers := map[string]tool.ToolHandlerFunc{
			batchAuditTool: func(ctx context.Context, _ map[string]any) (string, error) {
				_, checkErr = permission.RequireCheckerOrPreapproved(ctx)
				return "ok", nil
			},
		}

		result := executeBatchHandler(context.Background(), handlers, batchAuditTool,
			resolvedBatchCmd{asset: &asset_entity.Asset{ID: 1, Name: "db-1"}, cmdType: "sql", command: "SELECT 1"},
			map[string]any{"asset": "1", "command": "SELECT 1"})

		So(checkErr, ShouldBeNil)
		So(result.ExitCode, ShouldEqual, 0)
	})
}

func TestValidBatchTypes(t *testing.T) {
	Convey("validBatchTypes", t, func() {
		So(validBatchTypes["exec"], ShouldBeTrue)
		So(validBatchTypes["sql"], ShouldBeTrue)
		So(validBatchTypes["redis"], ShouldBeTrue)
		So(validBatchTypes["cp"], ShouldBeFalse)
		So(validBatchTypes[""], ShouldBeFalse)
	})
}
