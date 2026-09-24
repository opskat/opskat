package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
)

// RunPageToolCall is the gate an extension's own frontend page calls a tool
// through — it must run the exact same policy/approval pipeline handleExtToolExec
// runs opsctl's delegated commands through (checkingExtExecutor / decidingExtExecutor
// are the same stubs handleExtToolExec's tests already use), tagged with audit
// source "extension_page" instead of "opsctl" so the two are told apart in
// audit_logs.

// auditSourceCapturingExecutor records the audit source the gate put in ctx, so a
// test can tell a page call from an opsctl call without reading audit_logs.
type auditSourceCapturingExecutor struct {
	source string
}

func (e *auditSourceCapturingExecutor) ExecuteExtTool(ctx context.Context, _ int64, _ string) (string, error) {
	e.source = aictx.GetAuditSource(ctx)
	aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow})
	return `{"ok":true}`, nil
}

func TestRunPageToolCallTagsAuditSourceExtensionPage(t *testing.T) {
	executor := &auditSourceCapturingExecutor{}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	result, err := o.RunPageToolCall(context.Background(), "inv-1", 7, "note_list")

	require.NoError(t, err)
	require.Equal(t, `{"ok":true}`, result)
	require.Equal(t, "extension_page", executor.source)
}

func TestRunPageToolCallInjectsPolicyChecker(t *testing.T) {
	executor := &checkingExtExecutor{}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	_, err := o.RunPageToolCall(context.Background(), "inv-2", 7, "note_list")

	require.NoError(t, err)
	require.True(t, executor.checkerPresent, "a page-initiated call must receive the desktop approval checker")
}

// A refusal — Deny or a rejected NeedConfirm — must reach the page as an error: the
// page has no model to hand a refusal string to for it to adjust and retry, unlike
// the AI's unified exec which returns refusal text as an ordinary result.
func TestRunPageToolCallReportsPolicyDenialAsError(t *testing.T) {
	refusal := "command denied by policy: note_delete"
	executor := &decidingExtExecutor{
		decision: aictx.CheckResult{Decision: aictx.Deny, Message: refusal, DecisionSource: aictx.SourcePolicyDeny},
		output:   refusal,
	}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	result, err := o.RunPageToolCall(context.Background(), "inv-3", 7, "note_delete --json='{\"key\":\"k\"}'")

	require.Error(t, err)
	require.Equal(t, refusal, err.Error())
	require.Empty(t, result)
}

func TestRunPageToolCallAllowedReturnsResult(t *testing.T) {
	executor := &decidingExtExecutor{
		decision: aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow},
		output:   `{"notes":1}`,
	}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	result, err := o.RunPageToolCall(context.Background(), "inv-4", 7, "note_list")

	require.NoError(t, err)
	require.Equal(t, `{"notes":1}`, result)
}

func TestRunPageToolCallRequiresInitializedExecutor(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}

	_, err := o.RunPageToolCall(context.Background(), "inv-5", 7, "note_list")

	require.Error(t, err)
}

// The grant session a NeedConfirm's "always allow" persists under (and later
// calls match against, via permission.HandleConfirm / MatchExtensionGrant) must
// be derived from the asset, not the per-call invocation id: invocationID is
// minted fresh by the frontend for every call (so a *future* call can cancel
// *this one*), and a grant keyed by it would only ever be found by the one call
// that created it — the opposite of what "always allow" means. Two different
// invocation ids against the same asset must land on the same session; the same
// invocation id against two different assets must not collide.
func TestRunPageToolCallSessionIsScopedToTheAssetNotTheInvocation(t *testing.T) {
	var firstSession, secondSession, thirdSession string
	one := &sessionCapturingExecutor{capture: &firstSession}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: one}
	_, err := o.RunPageToolCall(context.Background(), "invocation-a", 7, "note_list")
	require.NoError(t, err)

	two := &sessionCapturingExecutor{capture: &secondSession}
	o.extExecutor = two
	_, err = o.RunPageToolCall(context.Background(), "invocation-b", 7, "note_list")
	require.NoError(t, err)

	three := &sessionCapturingExecutor{capture: &thirdSession}
	o.extExecutor = three
	_, err = o.RunPageToolCall(context.Background(), "invocation-a", 9, "note_list")
	require.NoError(t, err)

	require.NotEmpty(t, firstSession)
	require.Equal(t, firstSession, secondSession, "same asset, different invocation ids, must share one grant session")
	require.NotEqual(t, firstSession, thirdSession, "different assets must not share a grant session")
}

// "Always allow" from a page lasts as long as the desktop run it was given in —
// the page's counterpart of an AI conversation or an opsctl session — not forever:
// every other grant session is bounded by its caller's session, and an unbounded
// per-asset one would be a permanent rule the asset's policy card never shows.
func TestRunPageToolCallSessionDoesNotOutliveTheDesktopRun(t *testing.T) {
	var thisRun, nextRun string
	first := New(context.Background(), extTestLang{}, nil)
	first.extExecutor = &sessionCapturingExecutor{capture: &thisRun}
	_, err := first.RunPageToolCall(context.Background(), "invocation-a", 7, "note_list")
	require.NoError(t, err)

	second := New(context.Background(), extTestLang{}, nil)
	second.extExecutor = &sessionCapturingExecutor{capture: &nextRun}
	_, err = second.RunPageToolCall(context.Background(), "invocation-a", 7, "note_list")
	require.NoError(t, err)

	require.NotEmpty(t, thisRun)
	require.NotEqual(t, thisRun, nextRun, "a new desktop run must not inherit the previous run's page grants")
}

type sessionCapturingExecutor struct {
	capture *string
}

func (e *sessionCapturingExecutor) ExecuteExtTool(ctx context.Context, _ int64, _ string) (string, error) {
	*e.capture = aictx.GetSessionID(ctx)
	aictx.RecordDecision(ctx, aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow})
	return "{}", nil
}

// fakeAuditWriter records the ctx and info WriteToolCall was actually called with,
// so a test can check what a caller put into the *context it wrote with* — not
// just what gateExtToolCall computed — which is the seam a caller can get wrong by
// writing with a context it never re-assigned (exactly what happened here: an
// earlier version of gateExtToolCall built a context carrying the audit source but
// returned only the result struct, so both callers audited with their original,
// unannotated context and every gated call's audit row silently lost its source).
type fakeAuditWriter struct {
	ctx  context.Context
	info audit.ToolCallInfo
}

func (w *fakeAuditWriter) WriteToolCall(ctx context.Context, info audit.ToolCallInfo) {
	w.ctx = ctx
	w.info = info
}

func withFakeAuditWriter(t *testing.T) *fakeAuditWriter {
	t.Helper()
	original := extAuditWriter
	fake := &fakeAuditWriter{}
	extAuditWriter = fake
	t.Cleanup(func() { extAuditWriter = original })
	return fake
}

func TestRunPageToolCallWritesAuditWithSourceExtensionPage(t *testing.T) {
	writer := withFakeAuditWriter(t)
	executor := &decidingExtExecutor{
		decision: aictx.CheckResult{Decision: aictx.Allow, DecisionSource: aictx.SourcePolicyAllow},
		output:   `{"notes":1}`,
	}
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: executor}

	_, err := o.RunPageToolCall(context.Background(), "inv-audit", 7, "note_list")

	require.NoError(t, err)
	require.Equal(t, "extension_page", aictx.GetAuditSource(writer.ctx),
		"the context WriteToolCall is actually called with must carry the audit source the gate set, not the caller's original context")
	require.Equal(t, "exec", writer.info.ToolName)
}
