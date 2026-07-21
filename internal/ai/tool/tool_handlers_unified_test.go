package tool

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/serial_svc"
)

// noSessionSerialManager 是最小的 serial_svc.CommandManager 假实现，永远报告
// "没有活跃会话"——用来驱动下面 TestHandleExec_SerialPrecheckBlocksApprovalDialog。
type noSessionSerialManager struct{}

func (noSessionSerialManager) GetSessionByAssetID(_ int64) (serial_svc.CommandSession, bool) {
	return nil, false
}

// newRecordingChecker builds a real *permission.CommandPolicyChecker whose confirm
// callback flips the returned flag. It is how these tests observe "CheckForAsset was
// invoked" without a mockable seam: CommandPolicyChecker is a concrete struct, and its
// confirm callback is the only injection point — which is also exactly the side effect
// the ordering invariant exists to prevent (the callback IS the approval dialog; on
// "allow all" it persists a standing grant via SaveGrantPattern).
//
// For an asset type that has no CmdPolicy configured — every type used by the ordering
// tests below — CheckPermission falls through to aictx.NeedConfirm, which routes into
// HandleConfirm and calls this callback. So "callback never fired" is a faithful stand-in
// for "CheckForAsset never ran", and hoisting the permission check above the branch under
// test makes it fire (verified by mutation for each of the three tests).
func newRecordingChecker() (*permission.CommandPolicyChecker, *bool) {
	called := false
	confirm := func(_ context.Context, _ string, _ []permission.ApprovalItem) permission.ApprovalResponse {
		called = true
		return permission.ApprovalResponse{Decision: "allow"}
	}
	return permission.NewCommandPolicyChecker(confirm), &called
}

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
	// AnyTimes: the point of *checkCalled below is to catch a permission check hoisted
	// above the gate. With exact-arity mocks the hoisted CheckForAsset would blow up on
	// "unexpected call to Find" (its policy path re-reads the asset) before reaching the
	// assertion — the test would still fail, but for the wrong reason, and would keep
	// failing if the assertion were deleted. AnyTimes lets the assertion be the thing
	// that catches the regression.
	m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(
		&asset_entity.Asset{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil).AnyTimes()

	checker, checkCalled := newRecordingChecker()
	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)

	// A write command on purpose: the default Redis policy is a read-only allowlist, so a
	// read like `GET foo` resolves to Allow without ever consulting the user. That would
	// make the *checkCalled assertion below vacuous — it would hold even with the
	// permission check hoisted above the gate. `SET` falls outside the allowlist and so
	// reaches HandleConfirm, which is the side effect the ordering invariant forbids.
	out, err := handleExec(ctx, map[string]any{
		"asset": "7", "command": "SET foo bar",
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
	// 门禁必须排在权限检查之前——见 handleExec 的顺序注释。只断言"返回了引导文本"
	// 挡不住有人把权限检查上提：那样用户会先被弹一次审批，批准（甚至 allow all
	// 落一条常驻 grant）之后才拿到"请先调 help"的引导，命令根本没执行。
	if *checkCalled {
		t.Fatal("CheckForAsset ran on the gate-blocked path — an approval dialog must " +
			"never appear for a type whose usage doc the model hasn't seen")
	}
}

// TestHandleExec_SerialPrecheckBlocksApprovalDialog is the regression lock for the finding
// that the unified exec fired an approval dialog for a session-less serial asset and THEN
// failed with "no active serial session" — the exact double-failure
// HandleRunSerialCommand's ordering (session check before permission check, see
// internal/ai/helper/serial_helper.go) was written to avoid. Serial registers no
// CanonicalizeFunc (nothing to rewrite), so before this fix nothing hoisted the session
// check ahead of CheckForAsset for the unified exec path.
//
// The doc gate is pre-marked documented so this test isolates the precheck: without that,
// the call would fail at the gate before ever reaching either the precheck or the
// permission checker, and this test wouldn't prove anything about ordering between the two.
func TestHandleExec_SerialPrecheckBlocksApprovalDialog(t *testing.T) {
	m := setupUnified(t)
	asset := &asset_entity.Asset{ID: 11, Name: "console-1", Type: asset_entity.AssetTypeSerial}
	m.EXPECT().FindByName(gomock.Any(), "11").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(11)).Return(asset, nil).AnyTimes()

	checker, checkCalled := newRecordingChecker()

	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeSerial)
	ctx = helper.WithSerialManager(ctx, noSessionSerialManager{})

	_, err := handleExec(ctx, map[string]any{
		"asset": "11", "command": "display version",
	})
	if err == nil {
		t.Fatal("expected an error for a serial asset with no active session")
	}
	if !strings.Contains(err.Error(), "no active serial session") {
		t.Fatalf("got %q, want the no-active-session error", err.Error())
	}
	if *checkCalled {
		t.Fatal("CheckForAsset ran before the precheck failed — " +
			"an approval dialog must never appear for a session-less serial asset")
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

// 未注册执行器的类型（vnc 这类没有命令执行语义的远程桌面）应给出明确错误，
// 而不是撞上门禁的引导文本。
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
	// AnyTimes for the same reason as TestHandleExec_UndocumentedTypeReturnsGuidance: a
	// permission check hoisted above the executor lookup must be caught by *checkCalled
	// below, not by gomock complaining about an extra Find.
	m.EXPECT().FindByName(gomock.Any(), "5").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(5)).Return(
		&asset_entity.Asset{ID: 5, Name: "m1", Type: asset_entity.AssetTypeVNC}, nil).AnyTimes()

	checker, checkCalled := newRecordingChecker()
	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)

	// vnc has no registered permission type either, so CheckPermission returns NeedConfirm
	// and a check hoisted above the executor lookup would reach HandleConfirm — i.e. the
	// *checkCalled assertion below is not vacuous. (kafka used to stand in here; once it
	// joined the unified exec it stopped being an unsupported type.)
	out, err := handleExec(ctx, map[string]any{
		"asset": "5", "command": "whoami",
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
	// 执行器查找同样必须排在权限检查之前：对一个压根没有执行器的类型，用户不该先被
	// 弹一次审批、批准之后才被告知"这个类型还不支持"。
	if *checkCalled {
		t.Fatal("CheckForAsset ran for an asset type that has no executor at all — " +
			"an approval dialog must never appear for a command that cannot run")
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

// TestHandleExec_EtcdMalformedCommandNeverReachesPermissionCheck locks the ordering
// invariant this task's etcd wiring depends on: etcd_svc.ParseCommand (invoked through
// helper.CanonicalizeEtcdCommand, registered as etcd's CanonicalizeFunc) must reject a
// syntactically invalid command before the unified exec's permission check ever runs.
// A malformed command always fails execution, so if the permission check ran first the
// user would be shown an approval dialog (and, on "allow all", get a persisted grant)
// for a command that was going to fail regardless — exactly the double-failure the
// ordering comment on handleExec (tool_handlers_unified.go) exists to prevent.
//
// "put" without a value is used because it is malformed in a way ParseCommand rejects
// (etcd_svc.command.go's opRequiresKey handling for "put") while still naming a
// mutating op — a read like `get /k` would resolve via the default read-only allowlist
// without ever reaching HandleConfirm, which would make the *checkCalled assertion below
// vacuous (see newRecordingChecker's doc comment on this exact trap, and the note in
// TestHandleExec_UndocumentedTypeReturnsGuidance).
func TestHandleExec_EtcdMalformedCommandNeverReachesPermissionCheck(t *testing.T) {
	m := setupUnified(t)
	asset := &asset_entity.Asset{ID: 21, Name: "etcd-1", Type: asset_entity.AssetTypeEtcd}
	m.EXPECT().FindByName(gomock.Any(), "21").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(21)).Return(asset, nil).AnyTimes()

	checker, checkCalled := newRecordingChecker()
	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeEtcd)

	_, err := handleExec(ctx, map[string]any{
		"asset": "21", "command": "put /only-key",
	})
	if err == nil {
		t.Fatal("expected a parse error for a malformed etcd command (put with no value), got nil")
	}
	if !strings.Contains(err.Error(), "put requires key and value") {
		t.Fatalf("got %q, want the ParseCommand error surfaced verbatim", err.Error())
	}
	if *checkCalled {
		t.Fatal("CheckForAsset ran for a malformed etcd command — an approval dialog must " +
			"never appear before ParseCommand has validated the syntax")
	}
}

// TestHandleExec_EtcdLeaseCommandsMissingRequiredArgNeverReachesPermissionCheck extends
// the ordering guard above to "lease grant"/"lease revoke" without their required
// argument. Unlike "put /only-key", these are syntactically well-formed as far as the
// generic key/value positional parsing goes — ParseCommand used to accept them and let
// them fail later, at dispatch time (etcd_svc/ops.go's dispatchLeaseGrant/
// dispatchLeaseRevoke), which is after the unified exec's permission check. That let an
// approval dialog pop (and, on "allow all", persist a grant) for a command that was
// always going to fail — the exact bug this test locks shut. ParseCommand now rejects
// both directly (etcd_svc/command.go, next to opRequiresKey), so
// CanonicalizeEtcdCommand rejects them before handleExec ever reaches the permission
// check.
//
// Both commands are mutating ops outside the default read-only allowlist, so — per the
// trap documented on newRecordingChecker — CheckForAsset actually runs (and this test
// would actually fail) if the ordering regresses; a read-only command would make
// *checkCalled vacuously false regardless of ordering.
func TestHandleExec_EtcdLeaseCommandsMissingRequiredArgNeverReachesPermissionCheck(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantErr string
	}{
		{"lease grant without --ttl", "lease grant", "lease_grant requires positive ttl"},
		{"lease revoke without --lease", "lease revoke", "lease_revoke requires lease id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := setupUnified(t)
			asset := &asset_entity.Asset{ID: 21, Name: "etcd-1", Type: asset_entity.AssetTypeEtcd}
			m.EXPECT().FindByName(gomock.Any(), "21").Return(nil, nil).AnyTimes()
			m.EXPECT().Find(gomock.Any(), int64(21)).Return(asset, nil).AnyTimes()

			checker, checkCalled := newRecordingChecker()
			ctx := WithDocGate(context.Background(), NewDocGate())
			ctx = permission.WithPolicyChecker(ctx, checker)
			GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeEtcd)

			_, err := handleExec(ctx, map[string]any{
				"asset": "21", "command": tc.command,
			})
			if err == nil {
				t.Fatalf("expected a parse error for %q, got nil", tc.command)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %q, want it to contain %q", err.Error(), tc.wantErr)
			}
			if *checkCalled {
				t.Fatalf("CheckForAsset ran for %q — an approval dialog must never appear for a "+
					"command that ParseCommand can already prove will fail", tc.command)
			}
		})
	}
}

// TestHandleExec_CanonicalizeFailureRecordsAuditDecision locks the audit gap that
// motivated extending recordShortCircuit to the canonicalize path.
//
// The three other pre-permission short circuits (unsupported type / doc gate /
// precheck) already write a decision=deny audit row. Canonicalize failures did not:
// the tool call landed in audit_logs with the command filled in but decision empty,
// which in the existing semantics means "this call never involved a permission check
// at all" — indistinguishable from list_assets. That is worst exactly where it matters
// most: mongo's dropDatabase / dropCollection sit in the builtin deny group yet are not
// executable operations at all (they are absent from mongoOps), so every model attempt
// at them fails in canonicalize and would have left no deny trace whatsoever.
func TestHandleExec_CanonicalizeFailureRecordsAuditDecision(t *testing.T) {
	m := setupUnified(t)
	asset := &asset_entity.Asset{ID: 31, Name: "mongo-1", Type: asset_entity.AssetTypeMongoDB}
	if err := asset.SetMongoDBConfig(&asset_entity.MongoDBConfig{Host: "127.0.0.1", Port: 27017}); err != nil {
		t.Fatalf("SetMongoDBConfig: %v", err)
	}
	m.EXPECT().FindByName(gomock.Any(), "31").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(31)).Return(asset, nil).AnyTimes()

	checker, checkCalled := newRecordingChecker()
	slot := &aictx.CheckResult{}
	ctx := aictx.WithCheckResultSlot(context.Background(), slot)
	ctx = WithDocGate(ctx, NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeMongoDB)

	_, err := handleExec(ctx, map[string]any{
		"asset": "31", "command": "dropCollection users",
	})
	if err == nil {
		t.Fatal("expected dropCollection to be rejected as an unsupported mongo operation, got nil")
	}
	if slot.Decision != aictx.Deny {
		t.Fatalf("audit decision = %v, want %v (a canonicalize failure must not audit as an un-checked call)",
			slot.Decision, aictx.Deny)
	}
	if slot.DecisionSource != aictx.SourceExecCanonicalizeError {
		t.Fatalf("audit decision source = %q, want %q", slot.DecisionSource, aictx.SourceExecCanonicalizeError)
	}
	if *checkCalled {
		t.Fatal("CheckForAsset ran for a command that cannot be canonicalized — an approval " +
			"dialog must never appear for a command that is going to fail regardless")
	}
}

// TestHandleExec_MongoChecksCanonicalCommand locks what the mongo permission check
// must see: the canonicalized command — asset default database injected, whitespace
// collapsed, flags ordered — not the model's raw string and not a bare operation token.
//
// Both halves matter. Checking a bare op (the shape exec_mongo still passes) would show
// the user an approval dialog reading just "deleteMany", which cannot distinguish
// `deleteMany logs --query=...` from `deleteMany logs`, and the latter empties the whole
// collection. Checking the raw string would let a policy or grant written against the
// effective form (with --db) silently stop matching. The executor still gets the raw
// command; that asymmetry is locked type-agnostically by
// TestHandleExec_ExecutorReceivesRawCommand above.
//
// The confirm callback denies, so nothing is ever executed and no MongoDB connection is
// attempted — same technique as TestHandleExec_K8sCanonicalizesBeforePermissionCheck.
// `deleteMany` is deliberately outside the default read-only allowlist so the check
// actually reaches the callback (see newRecordingChecker's note on that trap).
func TestHandleExec_MongoChecksCanonicalCommand(t *testing.T) {
	m := setupUnified(t)
	asset := &asset_entity.Asset{ID: 32, Name: "mongo-2", Type: asset_entity.AssetTypeMongoDB}
	if err := asset.SetMongoDBConfig(&asset_entity.MongoDBConfig{
		Host: "127.0.0.1", Port: 27017, Database: "appdb",
	}); err != nil {
		t.Fatalf("SetMongoDBConfig: %v", err)
	}
	m.EXPECT().FindByName(gomock.Any(), "32").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(32)).Return(asset, nil).AnyTimes()

	var gotCheckCommand string
	checker := permission.NewCommandPolicyChecker(
		func(_ context.Context, _ string, items []permission.ApprovalItem) permission.ApprovalResponse {
			if len(items) > 0 {
				gotCheckCommand = items[0].Command
			}
			return permission.ApprovalResponse{Decision: "deny"}
		})
	ctx := WithDocGate(context.Background(), NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeMongoDB)

	const raw = `deleteMany    logs   --query='{"filter":{"level":"debug"}}'`
	const want = `deleteMany logs --db=appdb --query='{"filter":{"level":"debug"}}'`
	if _, err := handleExec(ctx, map[string]any{"asset": "32", "command": raw}); err != nil {
		t.Fatalf("handleExec: %v", err)
	}
	if gotCheckCommand != want {
		t.Fatalf("permission check saw %q, want the canonicalized command %q", gotCheckCommand, want)
	}
}

// TestHandleExec_MongoLegacyToolMatchesUnifiedPolicyDecision is the regression lock for
// the finding that exec_mongo (the legacy tool, helper.HandleExecMongo) bypassed
// collection-narrowed deny rules: it passed the bare operation token ("deleteMany") to
// CheckForAsset instead of a rich command string, so a deny rule written to protect one
// collection (`deleteMany users`) matched the unified exec's canonicalized command
// (`deleteMany users`) but never matched exec_mongo's bare op — the identical logical
// delete on the identical collection was denied on one path and silently allowed on the
// other. HandleExecMongo now builds a MongoCommand from its structured args and renders
// it the same way CanonicalizeMongoCommand does (see mongodb_helper.go), so both paths
// must present policy with the same string and reach the same decision.
//
// Both invocations target the same asset/operation/collection so the only thing that
// differs is which tool entry point is used: handleExec's "command" string vs.
// HandleExecMongo's structured operation/collection args.
func TestHandleExec_MongoLegacyToolMatchesUnifiedPolicyDecision(t *testing.T) {
	m := setupUnified(t)
	asset := &asset_entity.Asset{ID: 34, Name: "mongo-legacy", Type: asset_entity.AssetTypeMongoDB}
	if err := asset.SetMongoDBConfig(&asset_entity.MongoDBConfig{Host: "127.0.0.1", Port: 27017, Database: "prod"}); err != nil {
		t.Fatalf("SetMongoDBConfig: %v", err)
	}
	if err := asset.SetMongoPolicy(&asset_entity.MongoPolicy{
		AllowTypes: []string{"find", "deleteMany"},
		DenyTypes:  []string{"deleteMany users"},
	}); err != nil {
		t.Fatalf("SetMongoPolicy: %v", err)
	}
	m.EXPECT().FindByName(gomock.Any(), "34").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(34)).Return(asset, nil).AnyTimes()

	checker, _ := newRecordingChecker()

	// Unified exec path: a command string naming the "users" collection.
	unifiedSlot := &aictx.CheckResult{}
	unifiedCtx := aictx.WithCheckResultSlot(context.Background(), unifiedSlot)
	unifiedCtx = WithDocGate(unifiedCtx, NewDocGate())
	unifiedCtx = permission.WithPolicyChecker(unifiedCtx, checker)
	GetDocGate(unifiedCtx).MarkDocumented(aictx.GetConversationID(unifiedCtx), asset_entity.AssetTypeMongoDB)
	if _, err := handleExec(unifiedCtx, map[string]any{
		"asset": "34", "command": "deleteMany users",
	}); err != nil {
		t.Fatalf("unified exec: unexpected error: %v", err)
	}

	// Legacy exec_mongo path: the identical logical operation via structured args instead
	// of a command string.
	legacySlot := &aictx.CheckResult{}
	legacyCtx := aictx.WithCheckResultSlot(context.Background(), legacySlot)
	legacyCtx = permission.WithPolicyChecker(legacyCtx, checker)
	if _, err := helper.HandleExecMongo(legacyCtx, map[string]any{
		"asset_id": int64(34), "operation": "deleteMany", "collection": "users",
	}); err != nil {
		t.Fatalf("exec_mongo: unexpected error: %v", err)
	}

	if unifiedSlot.Decision != aictx.Deny {
		t.Fatalf("unified exec decision = %v, want %v", unifiedSlot.Decision, aictx.Deny)
	}
	if legacySlot.Decision != unifiedSlot.Decision {
		t.Fatalf("exec_mongo decision = %v, want it to match unified exec's %v — a collection-narrowed "+
			"deny rule must not be bypassable via the legacy tool", legacySlot.Decision, unifiedSlot.Decision)
	}
}

// TestHandleExec_MongoMissingDatabaseNeverReachesPermissionCheck is the regression lock
// for the finding that a write operation with no resolvable database (asset has no
// default database configured and the model's command doesn't pass --db) popped an
// approval dialog and only then failed at execution time — interrupting the user for a
// command that was always going to fail. resolveMongoCommand (mongo_exec.go) now rejects
// this before the unified exec's permission check ever runs, matching the ordering
// invariant already enforced for etcd's malformed commands above.
//
// "deleteMany" is a write op outside the default read-only allowlist, so — per the trap
// documented on newRecordingChecker — CheckForAsset would actually reach the confirm
// callback (and this test would actually fail) if the guard regressed; a read-only
// command would make *checkCalled vacuously false regardless of ordering.
func TestHandleExec_MongoMissingDatabaseNeverReachesPermissionCheck(t *testing.T) {
	m := setupUnified(t)
	asset := &asset_entity.Asset{ID: 35, Name: "mongo-nodefaultdb", Type: asset_entity.AssetTypeMongoDB}
	if err := asset.SetMongoDBConfig(&asset_entity.MongoDBConfig{Host: "127.0.0.1", Port: 27017}); err != nil {
		t.Fatalf("SetMongoDBConfig: %v", err)
	}
	m.EXPECT().FindByName(gomock.Any(), "35").Return(nil, nil).AnyTimes()
	m.EXPECT().Find(gomock.Any(), int64(35)).Return(asset, nil).AnyTimes()

	checker, checkCalled := newRecordingChecker()
	slot := &aictx.CheckResult{}
	ctx := aictx.WithCheckResultSlot(context.Background(), slot)
	ctx = WithDocGate(ctx, NewDocGate())
	ctx = permission.WithPolicyChecker(ctx, checker)
	GetDocGate(ctx).MarkDocumented(aictx.GetConversationID(ctx), asset_entity.AssetTypeMongoDB)

	_, err := handleExec(ctx, map[string]any{
		"asset": "35", "command": "deleteMany logs",
	})
	if err == nil {
		t.Fatal("expected an error for a write op with no resolvable database, got nil")
	}
	if !strings.Contains(err.Error(), "database") {
		t.Fatalf("got %q, want an error naming the missing database", err.Error())
	}
	if slot.Decision != aictx.Deny {
		t.Fatalf("audit decision = %v, want %v (a canonicalize failure must not audit as an un-checked call)",
			slot.Decision, aictx.Deny)
	}
	if *checkCalled {
		t.Fatal("CheckForAsset ran for a write op with no resolvable database — an approval " +
			"dialog must never appear for a command that is going to fail regardless")
	}
}
