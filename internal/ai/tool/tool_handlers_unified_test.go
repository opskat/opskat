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
func TestHandleExec_UndocumentedTypeReturnsGuidance(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil)
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(
		&asset_entity.Asset{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil)

	out, err := handleExec(context.Background(), map[string]any{
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
func TestHandleHelp_ReturnsDocAndMarksGate(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil)
	m.EXPECT().Find(gomock.Any(), int64(7)).Return(
		&asset_entity.Asset{ID: 7, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil)

	out, err := handleHelp(context.Background(), map[string]any{"asset": "7"})
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
}

// 未注册执行器的类型（Plan A 尚未支持 mongodb）应给出明确错误。
func TestHandleExec_UnsupportedTypeIsExplicit(t *testing.T) {
	m := setupUnified(t)
	m.EXPECT().FindByName(gomock.Any(), "5").Return(nil, nil)
	m.EXPECT().Find(gomock.Any(), int64(5)).Return(
		&asset_entity.Asset{ID: 5, Name: "m1", Type: asset_entity.AssetTypeMongoDB}, nil)

	out, err := handleExec(context.Background(), map[string]any{
		"asset": "5", "command": "find app.users {}",
	})
	if err == nil && !strings.Contains(out, "mongodb") {
		t.Fatalf("expected an explicit unsupported-type message, got %q / %v", out, err)
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
		Context:   "prod-ctx",
		Namespace: "prod-ns",
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
