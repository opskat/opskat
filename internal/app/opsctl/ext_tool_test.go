package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
)

type extTestLang struct{}

func (extTestLang) Lang() string { return "en" }

type checkingExtExecutor struct {
	checkerPresent bool
}

func (e *checkingExtExecutor) ExecuteExtTool(ctx context.Context, _ int64, _ string) (string, error) {
	_, err := permission.RequireChecker(ctx)
	e.checkerPresent = err == nil
	return `{"ok":true}`, nil
}

func TestHandleExtToolExecInjectsPolicyChecker(t *testing.T) {
	executor := &checkingExtExecutor{}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	resp := o.handleExtToolExec(approval.ApprovalRequest{AssetID: 7, Command: "run"})

	require.True(t, resp.Approved)
	require.True(t, executor.checkerPresent, "delegated extension execution must receive the desktop approval checker")
}

// decidingExtExecutor stands in for the unified exec handler: it records the policy
// decision into the audit slot and, on a refusal, returns the refusal text as a
// normal result — exactly the (message, nil) shape handleExec hands the model.
type decidingExtExecutor struct {
	decision aictx.CheckResult
	output   string
}

func (e *decidingExtExecutor) ExecuteExtTool(ctx context.Context, _ int64, _ string) (string, error) {
	aictx.RecordDecision(ctx, e.decision)
	return e.output, nil
}

// A refusal is not a result. The AI gets the refusal text so it can adjust, but
// opsctl has to tell a script the command did not run — the same exit 1 + stderr a
// denied builtin exec produces — so the refusal must cross the socket as an error.
func TestHandleExtToolExecReportsPolicyDenialAsToolError(t *testing.T) {
	refusal := "command denied by policy: delete_bucket"
	executor := &decidingExtExecutor{
		decision: aictx.CheckResult{Decision: aictx.Deny, Message: refusal, DecisionSource: aictx.SourcePolicyDeny},
		output:   refusal,
	}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	resp := o.handleExtToolExec(approval.ApprovalRequest{AssetID: 7, Command: "delete_bucket --bucket=logs"})

	require.False(t, resp.Approved)
	require.Equal(t, refusal, resp.ToolError)
	require.Empty(t, resp.ToolResult, "a refused command has no output to print")
}

func TestHandleExtToolExecAllowedCommandReturnsResult(t *testing.T) {
	executor := &decidingExtExecutor{
		decision: aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow},
		output:   `{"objects":1}`,
	}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	resp := o.handleExtToolExec(approval.ApprovalRequest{AssetID: 7, Command: "list_objects"})

	require.True(t, resp.Approved)
	require.Empty(t, resp.ToolError)
	require.Equal(t, `{"objects":1}`, resp.ToolResult)
}
