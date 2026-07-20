package audit

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
)

// memAuditRepo captures Create() calls synchronously. Unlike runner's
// mockAuditRepo (used behind the async auditMiddleware goroutine), WriteToolCall is
// called directly here, so no wait/lock dance is needed.
type memAuditRepo struct {
	logs []*audit_entity.AuditLog
}

func (m *memAuditRepo) Create(_ context.Context, log *audit_entity.AuditLog) error {
	m.logs = append(m.logs, log)
	return nil
}

func (m *memAuditRepo) List(_ context.Context, _ audit_repo.ListOptions) ([]*audit_entity.AuditLog, int64, error) {
	return m.logs, int64(len(m.logs)), nil
}

func (m *memAuditRepo) ListSessions(_ context.Context, _ int64) ([]audit_repo.SessionInfo, error) {
	return nil, nil
}

func setupAuditRepo(t *testing.T) *memAuditRepo {
	t.Helper()
	m := &memAuditRepo{}
	orig := audit_repo.Audit()
	audit_repo.RegisterAudit(m)
	// Restore unconditionally, including back to nil: an `if orig != nil` guard would
	// silently leave this test's exhausted mock as the process-wide default once the
	// test finished, for any later test relying on the original (or nil) registration
	// -- see setupExecAssetRepo in runner/audit_exec_asset_test.go, whose sibling guard
	// this mirrors.
	t.Cleanup(func() { audit_repo.RegisterAudit(orig) })
	return m
}

// TestWriteToolCall_PrefersPreResolvedAssetAttribution locks in the fix for the
// exec/help audit-attribution regression: those tools identify their asset via
// args["asset"] (a numeric id OR a name string), which aictx.ArgInt64 cannot parse,
// so the old args["asset_id"]/args["id"] lookup always produced AssetID=0 and
// AssetName="" for them. The resolution itself has to happen in runner's
// auditMiddleware (via assetref.Resolve, before the tool runs) because package audit
// can't import assetref/permission without an import cycle -- see ToolCallInfo's doc
// comment. This test locks in the receiving end: WriteToolCall must prefer
// info.AssetID/AssetName over parsing args when they're supplied.
func TestWriteToolCall_PrefersPreResolvedAssetAttribution(t *testing.T) {
	repo := setupAuditRepo(t)
	w := NewDefaultAuditWriter()

	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName:  "exec",
		ArgsJSON:  `{"asset":"web-1","command":"uptime"}`,
		Result:    "ok",
		AssetID:   7,
		AssetName: "web-1",
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.AssetID != 7 {
		t.Fatalf("AssetID = %d, want 7", entry.AssetID)
	}
	if entry.AssetName != "web-1" {
		t.Fatalf("AssetName = %q, want %q", entry.AssetName, "web-1")
	}
}

// TestWriteToolCall_PrefersPreResolvedCommand locks in the k8s canonicalization fix
// (the "exec"->"run_command" alias records the raw command for every asset type,
// including k8s, where the permission check and approval dialog see the
// --context/--namespace-injected effective command instead). When the caller
// supplies Command, WriteToolCall must use it verbatim instead of calling
// ExtractCommandForAudit.
func TestWriteToolCall_PrefersPreResolvedCommand(t *testing.T) {
	repo := setupAuditRepo(t)
	w := NewDefaultAuditWriter()

	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "exec",
		ArgsJSON: `{"asset":"k8s-1","command":"apply -f x"}`,
		Command:  "kubectl --context prod --namespace app apply -f x",
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	want := "kubectl --context prod --namespace app apply -f x"
	if got := repo.logs[0].Command; got != want {
		t.Fatalf("Command = %q, want %q", got, want)
	}
}

// TestWriteToolCall_FallsBackToArgsWhenNotPreResolved verifies the 14 pre-existing
// tools (run_command/exec_sql/...) are unaffected by this change: when the caller
// doesn't supply AssetID/AssetName/Command (their zero values), WriteToolCall must
// still fall back to the original args["asset_id"]/args["id"] + ExtractCommandForAudit
// path exactly as before.
func TestWriteToolCall_FallsBackToArgsWhenNotPreResolved(t *testing.T) {
	repo := setupAuditRepo(t)

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockAsset := mock_asset_repo.NewMockAssetRepo(ctrl)
	origAsset := asset_repo.Asset()
	asset_repo.RegisterAsset(mockAsset)
	t.Cleanup(func() { asset_repo.RegisterAsset(origAsset) })
	mockAsset.EXPECT().Find(gomock.Any(), int64(3)).Return(
		&asset_entity.Asset{ID: 3, Name: "db-1"}, nil)

	w := NewDefaultAuditWriter()
	w.WriteToolCall(context.Background(), ToolCallInfo{
		ToolName: "run_command",
		ArgsJSON: `{"asset_id":3,"command":"uptime"}`,
	})

	if len(repo.logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(repo.logs))
	}
	entry := repo.logs[0]
	if entry.AssetID != 3 || entry.AssetName != "db-1" {
		t.Fatalf("got AssetID=%d AssetName=%q, want 3/db-1", entry.AssetID, entry.AssetName)
	}
	if entry.Command != "uptime" {
		t.Fatalf("Command = %q, want %q", entry.Command, "uptime")
	}
}
