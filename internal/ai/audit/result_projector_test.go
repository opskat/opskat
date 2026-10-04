package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetResultProjectorsForTest clears the package-level registry around a test so
// RegisterResultProjector panics on a genuine duplicate, not on a previous test's
// leftover registration for the same tool name.
func resetResultProjectorsForTest(t *testing.T) {
	t.Helper()
	resultProjectorsMu.Lock()
	orig := resultProjectors
	resultProjectors = map[string]func(ToolCallInfo) string{}
	resultProjectorsMu.Unlock()
	t.Cleanup(func() {
		resultProjectorsMu.Lock()
		resultProjectors = orig
		resultProjectorsMu.Unlock()
	})
}

// TestWriteToolCall_RegisteredProjectorReplacesResult locks the get_asset_secret seam
// (docs/specs/2026-09-28-generic-asset.md「取值」"审计：只记录资产、字段名和判定结果，
// 不记录值"): a tool that registers a result projector must never have its raw Result
// (here standing in for the plaintext secret value) reach audit_logs.result.
func TestWriteToolCall_RegisteredProjectorReplacesResult(t *testing.T) {
	resetResultProjectorsForTest(t)
	repo := setupAuditRepo(t)

	RegisterResultProjector("get_asset_secret", func(info ToolCallInfo) string {
		return "field=token decision=allow"
	})

	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "get_asset_secret",
		ArgsJSON: `{"asset":"1","field":"token"}`,
		Result:   "sk-plaintext-secret-value",
	})

	require.Len(t, repo.logs, 1)
	assert.Equal(t, "field=token decision=allow", repo.logs[0].Result)
	assert.NotContains(t, repo.logs[0].Result, "sk-plaintext-secret-value")
}

// TestWriteToolCall_UnregisteredToolKeepsRawResult proves the projector registry is
// opt-in: a tool that never registers one keeps the existing raw-by-default contract
// (see TestWriteToolCall_PreservesAllColumnsVerbatimIncludingLiteralRedacted).
func TestWriteToolCall_UnregisteredToolKeepsRawResult(t *testing.T) {
	resetResultProjectorsForTest(t)
	repo := setupAuditRepo(t)

	NewDefaultAuditWriter().WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"1","command":"uptime"}`,
		Result:   "up 3 days",
	})

	require.Len(t, repo.logs, 1)
	assert.Equal(t, "up 3 days", repo.logs[0].Result)
}

func TestRegisterResultProjector_DuplicatePanics(t *testing.T) {
	resetResultProjectorsForTest(t)
	fn := func(ToolCallInfo) string { return "" }
	RegisterResultProjector("dup_tool", fn)
	assert.Panics(t, func() { RegisterResultProjector("dup_tool", fn) })
}

func TestRegisterResultProjector_InvalidPanics(t *testing.T) {
	resetResultProjectorsForTest(t)
	assert.Panics(t, func() { RegisterResultProjector("", func(ToolCallInfo) string { return "" }) })
	assert.Panics(t, func() { RegisterResultProjector("x", nil) })
}

// TestProjectResult_ReceivesDecisionAndError proves the projector function gets enough
// context (Decision, Error) to describe the outcome without ever needing info.Result's
// own content — the field this mechanism exists to keep out of the projection.
func TestProjectResult_ReceivesDecisionAndError(t *testing.T) {
	resetResultProjectorsForTest(t)
	var seenErr error
	RegisterResultProjector("probe_tool", func(info ToolCallInfo) string {
		seenErr = info.Error
		return "projected"
	})
	got := projectResult(ToolCallInfo{ToolName: "probe_tool", Result: "raw", Error: errors.New("boom")})
	assert.Equal(t, "projected", got)
	assert.EqualError(t, seenErr, "boom")
}
