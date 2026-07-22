package command

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
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

// TestBatchAssertPrefixType 锁住前缀"选 handler"变"类型断言"这条语义迁移的核心规则：
// exec（parseBatchArg/parseBatchInput 对无前缀条目的默认值，两者语法均未改动）永不断言，
// 否则每一条无前缀的非 ssh 命令都会在这里被拒——这与"非 ssh 且无前缀的条目必须能跑"
// 直接矛盾。sql/redis/mongo 只可能来自显式前缀，断言真实生效。
func TestBatchAssertPrefixType(t *testing.T) {
	Convey("batchAssertPrefixType", t, func() {
		sshAsset := &asset_entity.Asset{ID: 1, Name: "web", Type: asset_entity.AssetTypeSSH}
		redisAsset := &asset_entity.Asset{ID: 2, Name: "cache", Type: asset_entity.AssetTypeRedis}
		dbAsset := &asset_entity.Asset{ID: 3, Name: "db", Type: asset_entity.AssetTypeDatabase}

		Convey("exec 是无前缀默认值，不断言，任何真实类型都放行", func() {
			So(batchAssertPrefixType(sshAsset, "exec"), ShouldBeNil)
			So(batchAssertPrefixType(redisAsset, "exec"), ShouldBeNil)
			So(batchAssertPrefixType(dbAsset, "exec"), ShouldBeNil)
		})

		Convey("sql 断言真实类型是 database", func() {
			So(batchAssertPrefixType(dbAsset, "sql"), ShouldBeNil)
			So(batchAssertPrefixType(redisAsset, "sql"), ShouldNotBeNil)
		})

		Convey("redis 断言真实类型是 redis", func() {
			So(batchAssertPrefixType(redisAsset, "redis"), ShouldBeNil)
			So(batchAssertPrefixType(dbAsset, "redis"), ShouldNotBeNil)
		})
	})
}

// TestExecuteBatchItem_DefaultDispatchesByRealType 锁住 executeBatchItem 的 default
// 分支：无前缀（cmdType=="exec"）的条目现在按资产真实类型派发，而不是一律当 SSH 执行。
// 用一个 redis 资产验证——如果这条回归了，代码会试图对一个 redis 资产发起 SSH 连接
// （executeBatchExec），而不是调用这里注入的 "exec" handler。
func TestExecuteBatchItem_DefaultDispatchesByRealType(t *testing.T) {
	Convey("cmdType 为 exec（无前缀默认值）时，非 ssh 资产走统一 handler 而非 SSH 流式执行", t, func() {
		called := false
		handlers := map[string]tool.ToolHandlerFunc{
			"exec": func(_ context.Context, args map[string]any) (string, error) {
				called = true
				So(args["asset"], ShouldEqual, "7")
				So(args["command"], ShouldEqual, "PING")
				return `{"result":"PONG"}`, nil
			},
		}
		cmd := resolvedBatchCmd{
			asset:   &asset_entity.Asset{ID: 7, Name: "cache", Type: asset_entity.AssetTypeRedis},
			cmdType: "exec",
			command: "PING",
		}

		result := executeBatchItem(context.Background(), handlers, cmd)

		So(called, ShouldBeTrue)
		So(result.Error, ShouldBeEmpty)
		So(result.ExitCode, ShouldEqual, 0)
	})
}

// TestCmdBatch_ResolveFailureDoesNotAbortWholeBatch 锁住"一条解析失败不再拖垮整批"这条
// 行为变化：两个条目都指向不存在的资产 ID，此前第一个就会让 cmdBatch 直接 return 1、
// 第二个条目的结果永远不会出现在输出里；现在两条都应该出现在 JSON 输出的 results 里，
// 各自带上自己的 resolve 错误。
func TestCmdBatch_ResolveFailureDoesNotAbortWholeBatch(t *testing.T) {
	Convey("解析失败的条目落入 results 而不中断整批", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
		mockAsset.EXPECT().Find(gomock.Any(), int64(888)).Return(nil, errors.New("not found")).AnyTimes()
		mockAsset.EXPECT().Find(gomock.Any(), int64(999)).Return(nil, errors.New("not found")).AnyTimes()
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(mockAsset)
		defer asset_repo.RegisterAsset(origAsset)

		r, w, pipeErr := os.Pipe()
		So(pipeErr, ShouldBeNil)
		origStdout := os.Stdout
		os.Stdout = w

		code := cmdBatch(context.Background(), map[string]tool.ToolHandlerFunc{}, []string{"999:uptime", "888:hostname"}, "")

		So(w.Close(), ShouldBeNil)
		os.Stdout = origStdout
		data, readErr := io.ReadAll(r)
		So(readErr, ShouldBeNil)

		So(code, ShouldEqual, 1) // all failed

		var output batchOutput
		So(json.Unmarshal(data, &output), ShouldBeNil)
		So(len(output.Results), ShouldEqual, 2)
		So(output.Results[0].Error, ShouldContainSubstring, "resolve asset")
		So(output.Results[1].Error, ShouldContainSubstring, "resolve asset")
	})
}

// TestCmdBatch_AssertionFailureDoesNotAbortWholeBatch is the prefix-assertion mirror of
// TestCmdBatch_ResolveFailureDoesNotAbortWholeBatch above: both entries resolve fine
// (asset lookup succeeds) but declare a prefix type ("sql") that does not match the
// resolved asset's real type (redis) — batchAssertPrefixType must reject both, and,
// symmetric to the resolve-failure case, the first rejection must not stop the second
// entry from being resolved, asserted, and reported in its own results[i] slot.
func TestCmdBatch_AssertionFailureDoesNotAbortWholeBatch(t *testing.T) {
	Convey("前缀断言失败的条目落入 results 而不中断整批", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
		mockAsset.EXPECT().Find(gomock.Any(), int64(10)).
			Return(&asset_entity.Asset{ID: 10, Name: "cache-a", Type: asset_entity.AssetTypeRedis}, nil).AnyTimes()
		mockAsset.EXPECT().Find(gomock.Any(), int64(11)).
			Return(&asset_entity.Asset{ID: 11, Name: "cache-b", Type: asset_entity.AssetTypeRedis}, nil).AnyTimes()
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(mockAsset)
		defer asset_repo.RegisterAsset(origAsset)

		r, w, pipeErr := os.Pipe()
		So(pipeErr, ShouldBeNil)
		origStdout := os.Stdout
		os.Stdout = w

		code := cmdBatch(context.Background(), map[string]tool.ToolHandlerFunc{}, []string{"sql:10:PING", "sql:11:PING"}, "")

		So(w.Close(), ShouldBeNil)
		os.Stdout = origStdout
		data, readErr := io.ReadAll(r)
		So(readErr, ShouldBeNil)

		So(code, ShouldEqual, 1) // all failed

		var output batchOutput
		So(json.Unmarshal(data, &output), ShouldBeNil)
		So(len(output.Results), ShouldEqual, 2)

		// Both must have resolved (AssetID populated, no "resolve asset" error) — the
		// failure under test is the type assertion, not the lookup.
		So(output.Results[0].AssetID, ShouldEqual, 10)
		So(output.Results[0].Error, ShouldNotContainSubstring, "resolve asset")
		So(output.Results[0].Error, ShouldNotBeEmpty)
		So(output.Results[1].AssetID, ShouldEqual, 11)
		So(output.Results[1].Error, ShouldNotContainSubstring, "resolve asset")
		So(output.Results[1].Error, ShouldNotBeEmpty)
	})
}

// TestCmdBatch_OrderingAndCanonicalizeGateMatchesCmdExec locks batch.go's I2 + I1 fix:
// cmdBatch used to only assert --type (batchAssertPrefixType) before Step 3/4 — executor
// lookup, canonicalize, and precheck all lived inside the dispatched handler, past both
// the Step 3 policy pre-check and the Step 4 desktop batch-approval popup. And Step 3's
// CheckPermission was called with the entry's *raw* command, not canonicalize's output —
// same bug as cmdExec's I1, on the "default" (bare, unprefixed) dispatch path Task 10
// newly opened up to non-ssh, non-legacy-prefixed types (etcd/kafka/k8s/...).
//
// Four bare entries, each independently terminal at Step 2 or Step 3 — none of them ever
// reach Step 4 (requireBatchApproval, which would dial the real desktop socket), so this
// stays a safe, fast unit test:
//   - kafka: canonicalizes "topic delete orders" -> "topic.delete orders" and must be
//     denied by the built-in dangerous-op policy (the most important case — I1).
//   - rdp: doc-only type, no executor at all — must fail at Step 2, never even entering
//     Step 3's permission bucket.
//   - serial: has an executor but no active session — PrecheckFor must fail at Step 2.
//   - etcd: syntactically malformed ("put" with no value) — CanonicalizeFor must reject
//     it at Step 2.
func TestCmdBatch_OrderingAndCanonicalizeGateMatchesCmdExec(t *testing.T) {
	Convey("kafka/rdp/serial/etcd 四条裸目都必须在 Step 4 之前失败，且策略检查用规范化命令", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
		assets := []*asset_entity.Asset{
			{ID: 201, Name: "kafka-1", Type: asset_entity.AssetTypeKafka},
			{ID: 202, Name: "rdp-1", Type: asset_entity.AssetTypeRDP},
			{ID: 203, Name: "serial-1", Type: asset_entity.AssetTypeSerial},
			{ID: 204, Name: "etcd-1", Type: asset_entity.AssetTypeEtcd},
		}
		mockAsset.EXPECT().List(gomock.Any(), gomock.Any()).Return(assets, nil).AnyTimes()
		for _, a := range assets {
			mockAsset.EXPECT().Find(gomock.Any(), a.ID).Return(a, nil).AnyTimes()
		}
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(mockAsset)
		defer asset_repo.RegisterAsset(origAsset)

		ctx := helper.WithSerialManager(context.Background(), noSessionSerialManager{})

		r, w, pipeErr := os.Pipe()
		So(pipeErr, ShouldBeNil)
		origStdout := os.Stdout
		os.Stdout = w

		code := cmdBatch(ctx, map[string]tool.ToolHandlerFunc{}, []string{
			"kafka-1:topic delete orders",
			"rdp-1:foo",
			"serial-1:ls",
			"etcd-1:put /only-key",
		}, "")

		So(w.Close(), ShouldBeNil)
		os.Stdout = origStdout
		data, readErr := io.ReadAll(r)
		So(readErr, ShouldBeNil)

		So(code, ShouldEqual, 1) // all four entries fail

		var output batchOutput
		So(json.Unmarshal(data, &output), ShouldBeNil)
		So(len(output.Results), ShouldEqual, 4)

		// kafka: must be denied by the canonical-command policy check (Step 3), not by
		// a downstream approval/execution failure — "denied by policy" only appears on
		// that exact path.
		So(output.Results[0].AssetID, ShouldEqual, 201)
		So(output.Results[0].Error, ShouldContainSubstring, "denied by policy")

		// rdp: no executor at all — Step 2's prepareExecCommand gate.
		So(output.Results[1].AssetID, ShouldEqual, 202)
		So(output.Results[1].Error, ShouldContainSubstring, "has no exec support yet")

		// serial: precheck fails (no active session) — Step 2's prepareExecCommand gate.
		So(output.Results[2].AssetID, ShouldEqual, 203)
		So(output.Results[2].Error, ShouldContainSubstring, "no active serial session")

		// etcd: malformed command rejected by canonicalize — Step 2's
		// prepareExecCommand gate.
		So(output.Results[3].AssetID, ShouldEqual, 204)
		So(output.Results[3].Error, ShouldContainSubstring, "put requires key and value")
	})
}
