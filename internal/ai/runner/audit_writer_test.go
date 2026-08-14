package runner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cago-frame/agents/agent"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingAuditRequestTool 模拟 put_asset 这类 producer：在 Call 里写入审计 request
// 投影，但执行仍接收原始 args —— 审计中间件必须记录投影而不是原始参数。
type recordingAuditRequestTool struct {
	name      string
	projected map[string]any
}

func (r *recordingAuditRequestTool) Name() string         { return r.name }
func (r *recordingAuditRequestTool) Description() string  { return "audit-request-projection stub" }
func (r *recordingAuditRequestTool) Schema() agent.Schema { return agent.Schema{Type: "object"} }
func (r *recordingAuditRequestTool) Call(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
	// 执行数据保持原值：只有审计投影写到 slot。
	aictx.RecordAuditRequest(ctx, r.projected)
	return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}}, nil
}

func TestAuditMiddleware_UsesRecordedRequestProjection(t *testing.T) {
	mockRepo := &mockAuditRepo{}
	origRepo := audit_repo.Audit()
	audit_repo.RegisterAudit(mockRepo)
	t.Cleanup(func() { audit_repo.RegisterAudit(origRepo) })

	projected := map[string]any{
		"name":           "cache",
		"type":           "redis",
		"config":         map[string]any{"host": "redis.internal"},
		"authentication": map[string]any{"type": "password", "ref": float64(9)},
	}
	tool := &recordingAuditRequestTool{name: "put_asset", projected: projected}
	td := &agent.ToolDispatcher{
		Tools: []agent.Tool{tool},
		Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{
			{Matcher: ".*", Fn: auditMiddleware},
		},
	}
	rawInput := map[string]any{
		"name": "cache",
		"type": "redis",
		"config": map[string]any{
			"host": "redis.internal", "password": "top-secret",
		},
	}
	td.Run(context.Background(), agent.DispatchInput{
		ToolName: "put_asset", ToolUseID: "tu_proj", Input: rawInput,
	})

	waitForAudit(t, mockRepo, 1)
	entry := mockRepo.logs[0]
	assert.NotContains(t, entry.Request, "top-secret", "projected audit must omit the write-only password")
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(entry.Request), &got))
	assert.Equal(t, "cache", got["name"])
	assert.Equal(t, "redis", got["type"])
	config, ok := got["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "redis.internal", config["host"])
	_, hasPassword := config["password"]
	assert.False(t, hasPassword, "write-only field must be entirely absent, not redacted")
	assert.Contains(t, entry.Request, `"ref":9`)
}

func TestAuditMiddleware_NoOverrideKeepsRawArgs(t *testing.T) {
	mockRepo := &mockAuditRepo{}
	origRepo := audit_repo.Audit()
	audit_repo.RegisterAudit(mockRepo)
	t.Cleanup(func() { audit_repo.RegisterAudit(origRepo) })

	// recordingTool never records an audit request override: Task 7 raw-by-default holds.
	tool := &recordingTool{name: "exec"}
	td := &agent.ToolDispatcher{
		Tools: []agent.Tool{tool},
		Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{
			{Matcher: ".*", Fn: auditMiddleware},
		},
	}
	rawInput := map[string]any{"asset_id": float64(1), "command": "uptime", "extra": "raw-value"}
	td.Run(context.Background(), agent.DispatchInput{
		ToolName: "exec", ToolUseID: "tu_raw", Input: rawInput,
	})

	waitForAudit(t, mockRepo, 1)
	entry := mockRepo.logs[0]
	assert.Contains(t, entry.Request, "raw-value", "no override -> raw args as recorded")
	assert.Contains(t, entry.Request, "uptime")
}
