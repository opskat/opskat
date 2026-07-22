package command

import (
	"context"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/tool"
	. "github.com/smartystreets/goconvey/convey"
)

// mockAuditWriter 捕获审计日志写入
type mockAuditWriter struct {
	mu    sync.Mutex
	calls []audit.ToolCallInfo
}

func (m *mockAuditWriter) WriteToolCall(_ context.Context, info audit.ToolCallInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, info)
}

func (m *mockAuditWriter) lastCall() audit.ToolCallInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[len(m.calls)-1]
}

func TestCallHandler_Decision(t *testing.T) {
	Convey("callHandler 审计日志决策信息", t, func() {
		mock := &mockAuditWriter{}
		origWriter := opsctlAuditWriter
		opsctlAuditWriter = mock
		defer func() { opsctlAuditWriter = origWriter }()

		handlers := map[string]tool.ToolHandlerFunc{
			"exec": func(_ context.Context, args map[string]any) (string, error) {
				return `{"rows":[]}`, nil
			},
		}

		Convey("传入 decision 时审计日志包含决策信息", func() {
			decision := &aictx.CheckResult{
				Decision:       aictx.Allow,
				DecisionSource: aictx.SourcePolicyAllow,
				MatchedPattern: "SELECT *",
			}
			exitCode := callHandler(context.Background(), handlers, "exec", map[string]any{
				"asset":   "1",
				"command": "SELECT 1",
			}, decision)

			So(exitCode, ShouldEqual, 0)
			So(len(mock.calls), ShouldEqual, 1)

			info := mock.lastCall()
			So(info.ToolName, ShouldEqual, "exec")
			So(info.Decision, ShouldNotBeNil)
			So(info.Decision.Decision, ShouldEqual, aictx.Allow)
			So(info.Decision.DecisionSource, ShouldEqual, aictx.SourcePolicyAllow)
			So(info.Decision.MatchedPattern, ShouldEqual, "SELECT *")
		})

		Convey("SourceUserAllow 决策同样写进审计日志", func() {
			decision := &aictx.CheckResult{
				Decision:       aictx.Allow,
				DecisionSource: aictx.SourceUserAllow,
			}
			exitCode := callHandler(context.Background(), handlers, "exec", map[string]any{
				"asset":   "1",
				"command": "PING",
			}, decision)

			So(exitCode, ShouldEqual, 0)
			So(len(mock.calls), ShouldEqual, 1)

			info := mock.lastCall()
			So(info.Decision, ShouldNotBeNil)
			So(info.Decision.Decision, ShouldEqual, aictx.Allow)
			So(info.Decision.DecisionSource, ShouldEqual, aictx.SourceUserAllow)
		})

		Convey("不传 decision 时审计日志 Decision 为 nil", func() {
			exitCode := callHandler(context.Background(), handlers, "exec", map[string]any{
				"asset":   "1",
				"command": "SELECT 1",
			})

			So(exitCode, ShouldEqual, 0)
			So(len(mock.calls), ShouldEqual, 1)

			info := mock.lastCall()
			So(info.Decision, ShouldBeNil)
		})
	})
}

// TestBuildHandlerMap_HasEveryToolOpsctlLooksUp 锁住 opsctl 按名字查表这条**运行时**
// 依赖。cmdSQL / cmdRedisCmd / cmdMongo / cmdBatch / cmdCreate / cmdUpdate 都把工具名当
// 字符串传给 callHandler，名字不在 tool.AllToolDefs() 里只会在用户真的敲那条命令时打印
// "Internal error: unknown tool"——编译不报错，别的单测也照样过。
//
// 按类型区分的旧工具（exec_sql / exec_redis / exec_mongo）删除时，这几条路径统一
// 改查 "exec"；如果那次只删了注册项而没把这里加上，opsctl 的 sql/redis/mongo/batch
// 会整体变成运行时错误。help 一并断言：它和 exec 是同一次补进 AllToolDefs 的。put_asset /
// put_group 同理锁住 create/update：这份清单此前不含 add_asset/update_asset，四个 opsctl
// 调用点改名的那次改动本可以在这里毫无察觉地漏改一处（详见 create.go 的 callHandler 调用）。
func TestBuildHandlerMap_HasEveryToolOpsctlLooksUp(t *testing.T) {
	Convey("opsctl 派发表覆盖所有按名字查找的工具", t, func() {
		handlers := buildHandlerMap()
		for _, name := range []string{"exec", "help", "ext_exec", "upload_file", "download_file", "put_asset", "put_group"} {
			So(handlers, ShouldContainKey, name)
		}
	})
}
