package tool

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
)

func setupUnified(t *testing.T) *mock_asset_repo.MockAssetRepo {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	t.Cleanup(func() {
		if orig != nil {
			asset_repo.RegisterAsset(orig)
		}
	})
	return m
}

// 门禁未满足时必须返回引导文本，而不是 Go error——否则整轮会中断，模型无法自纠。
//
// Adds a FindByName mock beyond the brief's literal listing: assetref.Resolve always
// tries FindByName first regardless of whether the ref parses as numeric (see
// resolve.go and resolve_test.go's TestResolve_NumericID), so a numeric ref like "7"
// still needs it mocked or gomock fails with "unexpected call".
//
// Injects its own *DocGate via WithDocGate instead of relying on GetDocGate's old
// process-wide fallback (I2): that fallback was a package-level singleton shared by every
// caller of bare context.Background(), which made this test and
// TestHandleHelp_ReturnsDocAndMarksGate silently contaminate each other's gate state
// depending on run order (`go test -shuffle=on` failed 5 of 6 runs). GetDocGate now
// returns nil with no injection, and nil means allow — so an undocumented-gate test needs
// a real, freshly-constructed gate to observe the guidance path at all.
func TestHandleExec_UndocumentedTypeReturnsGuidance(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil)
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(
		&asset_entity.Asset{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil)

	ctx := WithDocGate(context.Background(), NewDocGate())

	out, err := handleExec(ctx, map[string]any{
		"asset": "7", "command": "GET foo",
	})
	if err != nil {
		t.Fatalf("gate must not return a Go error, got %v", err)
	}
	if !strings.Contains(out, "help") {
		t.Fatalf("guidance should tell the model to call help, got %q", out)
	}
	// 引导语必须点出解析出的类型（spec §4.6）。
	if !strings.Contains(out, "redis") {
		t.Fatalf("guidance should name the resolved asset type, got %q", out)
	}
}

// help 返回文档，并把该类型标记为已知用法。
//
// Injects its own *DocGate for the same reason as TestHandleExec_UndocumentedTypeReturnsGuidance
// (I2) — a shared process-wide default made gate state leak between tests run out of order.
func TestHandleHelp_ReturnsDocAndMarksGate(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil)
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(
		&asset_entity.Asset{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil)

	ctx := WithDocGate(context.Background(), NewDocGate())

	out, err := handleHelp(ctx, map[string]any{"asset": "7"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Command syntax") {
		t.Fatalf("help should return the SKILL.md body, got %q", out)
	}
	// 输出以类型开头（spec §4.6 第 2 条）。
	if !strings.Contains(out, "redis") {
		t.Fatalf("help should lead with the resolved type, got %q", out)
	}
	if !GetDocGate(ctx).IsDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeRedis) {
		t.Fatal("help must mark the resolved type as documented on the injected gate")
	}
}

// 未注册执行器的类型（Plan A 尚未支持 mongodb）应给出明确错误，而不是撞上门禁的引导文本。
//
// I3: executor lookup must run before the doc gate, so this must be reachable regardless
// of gate state. Injects a real undocumented gate so that, if executor lookup were moved
// back after the gate, this test would receive the "call help" guidance instead of the
// unsupported-type error and fail. The old assertion only checked that the output
// contained "mongodb", which both the guidance text ("call help(asset=\"m1\")...") and
// the unsupported-type error name — so it passed even when the gate fired first and
// returned guidance instead of the real error. This tightens it to the unsupported-type
// message's distinguishing wording and explicitly rules out the guidance text.
func TestHandleExec_UnsupportedTypeIsExplicit(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "5").Return(nil, nil)
	m.EXPECT().Find(gomock.Any(), int64(5)).Return(
		&asset_entity.Asset{ID: 5, Name: "m1", Type: asset_entity.AssetTypeMongoDB}, nil)

	ctx := WithDocGate(context.Background(), NewDocGate())

	out, err := handleExec(ctx, map[string]any{
		"asset": "5", "command": "find app.users {}",
	})
	if err == nil {
		t.Fatalf("expected an explicit unsupported-type error, got out=%q err=nil", out)
	}
	if !strings.Contains(err.Error(), "has no exec support yet") {
		t.Fatalf("expected the unsupported-type message, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "call help") {
		t.Fatalf("got the doc-gate guidance text instead of the unsupported-type error: %q", err.Error())
	}
}

// 同名歧义必须冒泡成错误，不能静默取第一个。
//
// Deviates from the task-6 brief's literal mock setup: the brief mocks List(), but
// assetref.Resolve (already implemented and tested in internal/ai/assetref) resolves
// non-numeric refs via FindByName, not List — see resolve_test.go's identical
// TestResolve_AmbiguousNameIsError. Mocking List here would make gomock fail the test
// with "unexpected call to FindByName" before ever reaching the ambiguity check, so this
// mocks FindByName to match Resolve's real behavior while preserving the same intent.
func TestHandleExec_AmbiguousNameErrors(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "db").Return([]*asset_entity.Asset{
		{ID: 1, Name: "db", Type: asset_entity.AssetTypeDatabase},
		{ID: 2, Name: "db", Type: asset_entity.AssetTypeDatabase},
	}, nil)

	if _, err := handleExec(context.Background(), map[string]any{
		"asset": "db", "command": "SELECT 1",
	}); err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
}

// TestHandleExec_K8sCanonicalizesBeforePermissionCheck is the regression lock for the
// policy-matching risk this task's canonicalization hook exists to close: handleExecK8s
// (tool_handler_k8s.go) checks policy against plan.EffectiveCommand — the command after
// --context/--namespace injection, which is also what approval dialogs and audit logs
// show today. If the unified exec checked the raw command instead, every existing policy
// or grant written against the effective form would silently stop matching.
//
// With no CmdPolicy configured on the asset, the k8s permission check falls through to
// aictx.NeedConfirm, which routes through CommandPolicyChecker.HandleConfirm and calls the
// confirm callback with the exact command string CheckForAsset received. That lets this
// test observe the string directly instead of asserting on execution side effects.
func TestHandleExec_K8sCanonicalizesBeforePermissionCheck(t *testing.T) {
	m := setupUnified(t)

	asset := &asset_entity.Asset{ID: 9, Name: "k8s-1", Type: asset_entity.AssetTypeK8s}
	if err := asset.SetK8sConfig(&asset_entity.K8sConfig{
		Kubeconfig: "enc-kubeconfig",
		Context:    "prod-ctx",
		Namespace:  "prod-ns",
	}); err != nil {
		t.Fatalf("set k8s config: %v", err)
	}

	m.EXPECT().FindByName(gomock.Any(), "9").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(9)).Return(asset, nil).AnyTimes()

	var gotCommand string
	confirm := func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		if len(items) > 0 {
			gotCommand = items[0].Command
		}
		return permission.ApprovalResponse{Decision: "deny"}
	}
	checker := permission.NewCommandPolicyChecker(confirm)

	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeK8s)

	if _, err := handleExec(ctx, map[string]any{
		"asset": "9", "command": "apply -f deploy.yaml",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "kubectl --context prod-ctx --namespace prod-ns apply -f deploy.yaml"
	if gotCommand != want {
		t.Fatalf("CheckForAsset saw %q, want the effective command %q", gotCommand, want)
	}
}

// TestHandleExec_ExecutorReceivesRawCommand is the regression lock for C1: the
// canonicalized command exists only to make the permission check match the form approval
// dialogs/audit logs show (see TestHandleExec_K8sCanonicalizesBeforePermissionCheck above)
// — it must never replace what's actually executed. That k8s test denies, so it never
// observes what reaches the executor; this test's checker ALLOWS instead, so execution
// actually happens and the executor's input can be asserted.
//
// It can't reuse the real k8s executor (helper.ExecK8sOnAsset) to observe this: that
// function shells out to a real kubectl/SSH session, and — this is the load-bearing part —
// it re-parses whatever string it's given via BuildK8sCommandPlan. Before the C1 fix,
// handleExec overwrote `command` with the canonicalized EffectiveCommand and passed that
// to the executor; EffectiveCommand is an unquoted display string ("kubectl " +
// strings.Join(args, " ")), so a command like `sh -c "echo hello world"` re-parses into
// argv `sh -c echo hello world` — the quoting is gone and the remote command silently
// changes. This registers a temporary fake asset type with its own executor and a
// deliberately lossy canonicalizer (same shape as k8s's) so the test can assert directly,
// with no real process/network involved, that the executor receives the untouched raw
// command while the permission check sees the canonicalized one.
func TestHandleExec_ExecutorReceivesRawCommand(t *testing.T) {
	m := setupUnified(t)

	const fakeType = "test-exec-raw-command"
	var gotExecCommand string
	permission.RegisterExecutor(fakeType,
		func(_ context.Context, _ *asset_entity.Asset, command, _ string) (string, error) {
			gotExecCommand = command
			return "ok", nil
		},
		"fake help doc for "+fakeType,
		func(_ *asset_entity.Asset, command string) (string, error) {
			// Deliberately lossy, like k8s's EffectiveCommand: a display-form rewrite that
			// would betray itself immediately if it ever reached the executor instead of
			// the permission check.
			return "CANONICAL(" + command + ")", nil
		})
	t.Cleanup(func() { permission.UnregisterExecutorForTest(fakeType) })

	asset := &asset_entity.Asset{ID: 42, Name: "fake-asset", Type: fakeType}
	m.EXPECT().FindByName(gomock.Any(), "42").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(42)).Return(asset, nil).AnyTimes()

	var gotCheckCommand string
	confirm := func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
		if len(items) > 0 {
			gotCheckCommand = items[0].Command
		}
		return permission.ApprovalResponse{Decision: "allow"}
	}
	checker := permission.NewCommandPolicyChecker(confirm)

	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), fakeType)

	rawCommand := `sh -c "echo hello world"`
	out, err := handleExec(ctx, map[string]any{
		"asset": "42", "command": rawCommand,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "ok" {
		t.Fatalf("got %q, want the executor's return value %q", out, "ok")
	}

	if wantCheck := "CANONICAL(" + rawCommand + ")"; gotCheckCommand != wantCheck {
		t.Fatalf("permission check saw %q, want the canonicalized command %q", gotCheckCommand, wantCheck)
	}
	if gotExecCommand != rawCommand {
		t.Fatalf("executor saw %q, want the raw command %q", gotExecCommand, rawCommand)
	}
}
