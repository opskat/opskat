package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	aitool "github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/pkg/extension"
)

type oracleExtensionExecutor struct {
	ext      *extension.Extension
	def      extension.ToolDef
	callArgs []byte
}

func (e *oracleExtensionExecutor) FindExtensionByTool(_, _ string) *extension.Extension {
	return e.ext
}

func (e *oracleExtensionExecutor) FindToolDef(_, _ string) (extension.ToolDef, bool) {
	return e.def, true
}

func (e *oracleExtensionExecutor) GetExtensionPolicyGroups(string, string, int64) []string {
	return nil
}

func (e *oracleExtensionExecutor) CheckToolPolicy(context.Context, string, string, []byte) (string, string, error) {
	return "", "", nil
}

func (e *oracleExtensionExecutor) CallTool(_ context.Context, _, _ string, args []byte) ([]byte, error) {
	e.callArgs = append([]byte(nil), args...)
	return []byte(`{"ok":true}`), nil
}

func waitForExtensionAudit(t *testing.T, repo *mockAuditRepo) *audit_entity.AuditLog {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo.mu.Lock()
		for _, entry := range repo.logs {
			if entry.ToolName == "ext_exec" {
				repo.mu.Unlock()
				return entry
			}
		}
		repo.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("extension audit not found")
	return nil
}

func TestExtensionApprovalAndPluginUseRawArgumentsAndAuditStoresRaw(t *testing.T) {
	redactionSentinel := strings.Repeat("review", 2) + "-extension-value"
	executor := &oracleExtensionExecutor{
		ext: &extension.Extension{Name: "oss", Manifest: &extension.Manifest{Name: "oss"}},
		def: extension.ToolDef{Name: "delete_objects", Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bucket": map[string]any{"type": "string"},
				"keys":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"token":  map[string]any{"type": "string"},
			},
			"required": []any{"bucket", "keys"},
		}},
	}
	aitool.SetExecToolExecutor(executor)
	t.Cleanup(func() { aitool.SetExecToolExecutor(nil) })

	repo := registerMockAuditRepo(t)
	var approvalKind, approvalCommand string
	checker := permission.NewCommandPolicyChecker(func(_ context.Context, kind string, items []permission.ApprovalItem) permission.ApprovalResponse {
		approvalKind = kind
		approvalCommand = items[0].Command
		return permission.ApprovalResponse{Decision: "allow"}
	})
	ctx := permission.WithPolicyChecker(context.Background(), checker)
	ctx = aictx.WithAuditSource(ctx, "ai")

	td := &agent.ToolDispatcher{
		Tools: []agent.Tool{findExecTool(t, "ext_exec")},
		Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{
			{Matcher: ".*", Fn: auditMiddleware},
		},
	}
	result := td.Run(ctx, agent.DispatchInput{
		ToolName:  "ext_exec",
		ToolUseID: "extension-audit-oracle",
		Input: map[string]any{
			"command": "oss delete_objects --token=" + redactionSentinel + " --keys=logs/a,logs/b --bucket=production-target",
		},
	})
	require.NotNil(t, result.Output)
	require.False(t, result.Output.IsError)
	require.Equal(t, permission.ApprovalKindExtension, approvalKind)
	require.Contains(t, approvalCommand, "production-target")
	require.Contains(t, approvalCommand, "logs/a")
	require.Contains(t, approvalCommand, redactionSentinel)

	var pluginArgs map[string]any
	require.NoError(t, json.Unmarshal(executor.callArgs, &pluginArgs))
	require.Equal(t, "production-target", pluginArgs["bucket"])
	require.Equal(t, redactionSentinel, pluginArgs["token"], "redaction must not corrupt the live plugin invocation")

	entry := waitForExtensionAudit(t, repo)
	require.Equal(t, "ai", entry.Source)
	// 默认 writer 原样落库：审计 command/request 与审批者看到的实际主体、执行器实际
	// 收到的参数一致，不再生成 canonical 脱敏副本。
	require.Equal(t, approvalCommand, entry.Command)
	require.Contains(t, entry.Command, redactionSentinel)
	require.Contains(t, entry.Request, "production-target")
	require.Contains(t, entry.Request, "logs/a")
	require.Contains(t, entry.Request, redactionSentinel)
	require.Contains(t, entry.Request, "--token="+redactionSentinel)
}
