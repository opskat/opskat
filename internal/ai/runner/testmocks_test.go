package runner

import (
	"context"
	"sync"

	"github.com/cago-frame/agents/agent"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

// mockAuditRepo 是审计中间件测试用的 in-memory 仓库。
type mockAuditRepo struct {
	mu   sync.Mutex
	logs []*audit_entity.AuditLog
}

func (m *mockAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, log)
	return nil
}

func (m *mockAuditRepo) List(_ context.Context, _ audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.logs, int64(len(m.logs)), nil
}

func (m *mockAuditRepo) ListSessions(_ context.Context, _ int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

// stubLocalTool 是测试用 local 工具桩,复刻自 tool/local_tool_gate_test.go。
type stubLocalTool struct {
	name   string
	called *bool
}

func (s stubLocalTool) Name() string         { return s.name }
func (s stubLocalTool) Description() string  { return "stub" }
func (s stubLocalTool) Schema() agent.Schema { return agent.Schema{Type: "object"} }
func (s stubLocalTool) Call(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
	if s.called != nil {
		*s.called = true
	}
	return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "ran"}}}, nil
}
