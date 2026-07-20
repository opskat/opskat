package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/agents/agent"
	"go.uber.org/mock/gomock"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

// setupExecAssetRepo registers a mock AssetRepo for the duration of the test,
// mirroring the pattern used across internal/ai/{assetref,tool}'s tests
// (mock_asset_repo.RegisterAsset + t.Cleanup restore).
func setupExecAssetRepo(t *testing.T) *mock_asset_repo.MockAssetRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(m)
	// Restore unconditionally, including back to nil: no other test in this package
	// registers an AssetRepo of its own before relying on args["asset_id"]-only
	// attribution, so an `if orig != nil` guard here would silently leave this
	// exhausted mock (and its now-completed *testing.T) as the process-wide default,
	// and a later test's async audit write would panic trying to call it.
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
	return m
}

func registerMockAuditRepo(t *testing.T) *mockAuditRepo {
	t.Helper()
	mockRepo := &mockAuditRepo{}
	origRepo := audit_repo.Audit()
	audit_repo.RegisterAudit(mockRepo)
	t.Cleanup(func() { audit_repo.RegisterAudit(origRepo) })
	return mockRepo
}

func okResult() (*agent.ToolResultBlock, error) {
	return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "ok"}}}, nil
}

// TestAuditMiddleware_ExecToolResolvesAssetByID and ...ByName lock in the fix for
// exec/help's asset attribution: their asset identifier is args["asset"] (numeric id
// or name string), not args["asset_id"]/args["id"], so it needs assetref.Resolve, not
// aictx.ArgInt64.
func TestAuditMiddleware_ExecToolResolvesAssetByID(t *testing.T) {
	Convey("exec 工具用数字 id 作为 asset 参数时，审计能解析出资产归属", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "7").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(7)).Return(
			&asset_entity.Asset{ID: 7, Name: "web-1", Type: asset_entity.AssetTypeSSH}, nil)

		runAuditChain(t, context.Background(), "exec", "tu_asset_id",
			map[string]any{"asset": "7", "command": "uptime"}, nil, okResult)

		waitForAudit(t, mockRepo, 1)
		entry := mockRepo.logs[0]
		So(entry.AssetID, ShouldEqual, int64(7))
		So(entry.AssetName, ShouldEqual, "web-1")
		So(entry.Command, ShouldEqual, "uptime")
	})
}

func TestAuditMiddleware_ExecToolResolvesAssetByName(t *testing.T) {
	Convey("exec 工具用名称字符串作为 asset 参数时，审计能解析出资产归属", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "web-1").Return([]*asset_entity.Asset{
			{ID: 8, Name: "web-1", Type: asset_entity.AssetTypeSSH},
		}, nil)

		runAuditChain(t, context.Background(), "exec", "tu_asset_name",
			map[string]any{"asset": "web-1", "command": "uptime"}, nil, okResult)

		waitForAudit(t, mockRepo, 1)
		entry := mockRepo.logs[0]
		So(entry.AssetID, ShouldEqual, int64(8))
		So(entry.AssetName, ShouldEqual, "web-1")
	})
}

func TestAuditMiddleware_HelpToolResolvesAssetAttribution(t *testing.T) {
	Convey("help 工具同样能解析出资产归属", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)
		m.EXPECT().FindByName(gomock.Any(), "9").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(9)).Return(
			&asset_entity.Asset{ID: 9, Name: "cache-1", Type: asset_entity.AssetTypeRedis}, nil)

		runAuditChain(t, context.Background(), "help", "tu_help",
			map[string]any{"asset": "9"}, nil, func() (*agent.ToolResultBlock, error) {
				return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "docs"}}}, nil
			})

		waitForAudit(t, mockRepo, 1)
		entry := mockRepo.logs[0]
		So(entry.AssetID, ShouldEqual, int64(9))
		So(entry.AssetName, ShouldEqual, "cache-1")
	})
}

// TestAuditMiddleware_ExecToolCanonicalizesK8sCommand is the regression lock for
// defect B: the "exec"->"run_command" alias in extractor.go records args["command"]
// verbatim, which is correct for ssh/serial/redis/database (raw == effective there)
// but wrong for k8s, where handleExec canonicalizes the command (injects
// --context/--namespace) before the permission check / approval dialog sees it.
// Approved-but-not-what's-audited is the exact bug class this locks against.
func TestAuditMiddleware_ExecToolCanonicalizesK8sCommand(t *testing.T) {
	Convey("k8s 资产的 exec 审计记录规范化后的命令，与审批弹窗一致", t, func() {
		mockRepo := registerMockAuditRepo(t)
		m := setupExecAssetRepo(t)

		asset := &asset_entity.Asset{ID: 42, Name: "k8s-1", Type: asset_entity.AssetTypeK8s}
		if err := asset.SetK8sConfig(&asset_entity.K8sConfig{
			Kubeconfig: "enc-kubeconfig",
			Context:    "prod-ctx",
			Namespace:  "prod-ns",
		}); err != nil {
			t.Fatalf("set k8s config: %v", err)
		}
		m.EXPECT().FindByName(gomock.Any(), "42").Return(nil, nil)
		m.EXPECT().Find(gomock.Any(), int64(42)).Return(asset, nil)

		runAuditChain(t, context.Background(), "exec", "tu_k8s",
			map[string]any{"asset": "42", "command": "apply -f deploy.yaml"}, nil, okResult)

		waitForAudit(t, mockRepo, 1)
		entry := mockRepo.logs[0]
		want := "kubectl --context prod-ctx --namespace prod-ns apply -f deploy.yaml"
		So(entry.Command, ShouldEqual, want)
		So(entry.AssetID, ShouldEqual, int64(42))
	})
}

// phaseGatedAssetRepo resolves successfully only until *toolStarted flips true, then
// fails -- standing in for an asset that becomes unresolvable (e.g. soft-deleted,
// status != Active, see asset_repo.Find/FindByName's "AND status = Active" filter)
// partway through a tool call.
type phaseGatedAssetRepo struct {
	asset_repo.AssetRepo
	asset       *asset_entity.Asset
	toolStarted *bool
}

func (r *phaseGatedAssetRepo) Find(_ context.Context, _ int64) (*asset_entity.Asset, error) {
	if *r.toolStarted {
		return nil, errors.New("record not found")
	}
	return r.asset, nil
}

func (r *phaseGatedAssetRepo) FindByName(_ context.Context, _ string) ([]*asset_entity.Asset, error) {
	return nil, nil
}

// TestAuditMiddleware_ExecToolResolvesAssetBeforeToolRuns is the regression lock for
// the ordering sub-requirement: resolution must happen before the tool runs, not
// after. If it happened after (e.g. audit re-resolving post-hoc via asset_repo.Find),
// a future delete_asset tool would flip the asset's status during its own call and
// the post-hoc lookup would come back empty -- losing the name for exactly the
// operation where it matters most. This test simulates that status flip with a repo
// that only resolves successfully until the tool itself starts running.
func TestAuditMiddleware_ExecToolResolvesAssetBeforeToolRuns(t *testing.T) {
	Convey("资产解析必须发生在工具执行之前，而不是之后", t, func() {
		mockRepo := registerMockAuditRepo(t)

		asset := &asset_entity.Asset{ID: 5, Name: "web-5", Type: asset_entity.AssetTypeSSH}
		toolStarted := false
		repo := &phaseGatedAssetRepo{asset: asset, toolStarted: &toolStarted}
		origAsset := asset_repo.Asset()
		asset_repo.RegisterAsset(repo)
		t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })

		runAuditChain(t, context.Background(), "exec", "tu_ordering",
			map[string]any{"asset": "5", "command": "uptime"}, nil,
			func() (*agent.ToolResultBlock, error) {
				toolStarted = true
				return okResult()
			})

		waitForAudit(t, mockRepo, 1)
		entry := mockRepo.logs[0]
		So(entry.AssetID, ShouldEqual, int64(5))
		So(entry.AssetName, ShouldEqual, "web-5")
	})
}
