package ai

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
)

func TestContext_AuditSource(t *testing.T) {
	convey.Convey("审计来源 context", t, func() {
		convey.Convey("默认返回空字符串", func() {
			ctx := context.Background()
			assert.Equal(t, "", aictx.GetAuditSource(ctx))
		})

		convey.Convey("设置后可以获取", func() {
			ctx := aictx.WithAuditSource(context.Background(), "ai")
			assert.Equal(t, "ai", aictx.GetAuditSource(ctx))
		})
	})
}

func TestContext_ConversationID(t *testing.T) {
	convey.Convey("会话 ID context", t, func() {
		convey.Convey("默认返回 0", func() {
			ctx := context.Background()
			assert.Equal(t, int64(0), aictx.GetConversationID(ctx))
		})

		convey.Convey("设置后可以获取", func() {
			ctx := aictx.WithConversationID(context.Background(), 42)
			assert.Equal(t, int64(42), aictx.GetConversationID(ctx))
		})
	})
}

func TestContext_GrantSessionID(t *testing.T) {
	convey.Convey("授权会话 ID context", t, func() {
		convey.Convey("默认返回空字符串", func() {
			ctx := context.Background()
			assert.Equal(t, "", aictx.GetGrantSessionID(ctx))
		})

		convey.Convey("设置后可以获取", func() {
			ctx := aictx.WithGrantSessionID(context.Background(), "grant-abc-123")
			assert.Equal(t, "grant-abc-123", aictx.GetGrantSessionID(ctx))
		})
	})
}

func TestCheckResult_DecisionString(t *testing.T) {
	convey.Convey("aictx.CheckResult.DecisionString", t, func() {
		convey.Convey("aictx.Allow 返回 allow", func() {
			r := aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow}
			assert.Equal(t, "allow", r.DecisionString())
		})

		convey.Convey("aictx.Deny 返回 deny", func() {
			r := aictx.CheckResult{Decision: aictx.Deny, DecisionSource: aictx.SourceUserDeny}
			assert.Equal(t, "deny", r.DecisionString())
		})

		convey.Convey("aictx.NeedConfirm 返回空字符串", func() {
			r := aictx.CheckResult{Decision: aictx.NeedConfirm}
			assert.Equal(t, "", r.DecisionString())
		})
	})
}

func TestCheckResult_Context(t *testing.T) {
	convey.Convey("aictx.CheckResult 跨 middleware 共享（ctx slot）", t, func() {
		convey.Convey("auditMiddleware 挂的 slot 能被 aictx.RecordDecision 写入", func() {
			slot := &aictx.CheckResult{}
			ctx := aictx.WithCheckResultSlot(context.Background(), slot)
			aictx.RecordDecision(ctx, aictx.CheckResult{
				Decision:       aictx.Allow,
				DecisionSource: aictx.SourceGrantAllow,
				MatchedPattern: "cat *",
			})
			assert.Equal(t, aictx.Allow, slot.Decision)
			assert.Equal(t, aictx.SourceGrantAllow, slot.DecisionSource)
			assert.Equal(t, "cat *", slot.MatchedPattern)
		})

		convey.Convey("无 slot 时 aictx.RecordDecision 是 no-op", func() {
			ctx := context.Background()
			assert.NotPanics(t, func() {
				aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Allow})
			})
		})

		convey.Convey("slot 是 nil 指针时 aictx.RecordDecision 不 panic", func() {
			var nilSlot *aictx.CheckResult
			ctx := aictx.WithCheckResultSlot(context.Background(), nilSlot)
			assert.NotPanics(t, func() {
				aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Allow})
			})
		})
	})
}

func TestExtractCommandForAudit(t *testing.T) {
	convey.Convey("从工具参数提取命令", t, func() {
		convey.Convey("run_command 提取 command 字段", func() {
			cmd := audit.ExtractCommandForAudit("run_command", map[string]any{
				"asset_id": float64(1),
				"command":  "uptime",
			})
			assert.Equal(t, "uptime", cmd)
		})

		convey.Convey("upload_file 生成上传描述", func() {
			cmd := audit.ExtractCommandForAudit("upload_file", map[string]any{
				"asset_id":    float64(1),
				"local_path":  "/tmp/config.yml",
				"remote_path": "/etc/app/config.yml",
			})
			assert.Equal(t, "upload /tmp/config.yml → /etc/app/config.yml", cmd)
		})

		convey.Convey("download_file 生成下载描述", func() {
			cmd := audit.ExtractCommandForAudit("download_file", map[string]any{
				"asset_id":    float64(1),
				"remote_path": "/var/log/app.log",
				"local_path":  "./app.log",
			})
			assert.Equal(t, "download /var/log/app.log → ./app.log", cmd)
		})

		convey.Convey("exec (opsctl) 提取 command 字段", func() {
			cmd := audit.ExtractCommandForAudit("exec", map[string]any{
				"asset_id": float64(1),
				"command":  "df -h",
			})
			assert.Equal(t, "df -h", cmd)
		})

		convey.Convey("exec_sql 提取 sql 字段", func() {
			cmd := audit.ExtractCommandForAudit("exec_sql", map[string]any{
				"asset_id": float64(1),
				"sql":      "SELECT * FROM users LIMIT 10",
			})
			assert.Equal(t, "SELECT * FROM users LIMIT 10", cmd)
		})

		convey.Convey("exec_redis 提取 command 字段", func() {
			cmd := audit.ExtractCommandForAudit("exec_redis", map[string]any{
				"asset_id": float64(1),
				"command":  "GET mykey",
			})
			assert.Equal(t, "GET mykey", cmd)
		})

		convey.Convey("exec_k8s 规范化 kubectl 命令", func() {
			cmd := audit.ExtractCommandForAudit("exec_k8s", map[string]any{
				"asset_id": float64(1),
				"command":  "get pods -A",
			})
			assert.Equal(t, "kubectl get pods -A", cmd)
		})

		convey.Convey("其他工具返回空字符串", func() {
			cmd := audit.ExtractCommandForAudit("list_assets", map[string]any{})
			assert.Equal(t, "", cmd)

			cmd = audit.ExtractCommandForAudit("add_asset", map[string]any{
				"name": "web-01",
				"host": "10.0.0.1",
			})
			assert.Equal(t, "", cmd)
		})
	})
}
