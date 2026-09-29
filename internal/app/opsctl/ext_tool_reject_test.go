package opsctl

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/permission"
	aitool "github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// recordingAuditRepo keeps the rows the real audit writer creates.
type recordingAuditRepo struct {
	audit_repo.AuditRepo
	mu   sync.Mutex
	rows []*audit_entity.AuditLog
}

func (r *recordingAuditRepo) Create(_ context.Context, row *audit_entity.AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, row)
	return nil
}

// unusedConnTest satisfies extreg's test-connection wiring, which this test never uses.
type unusedConnTest struct{}

func (unusedConnTest) Build(string, *extension.Manifest, string, extreg.TestConnectionCaller) conntest.TestFunc {
	return func(context.Context, string, string) error { return nil }
}

type unifiedExecExecutor struct{}

func (unifiedExecExecutor) ExecuteExtTool(ctx context.Context, assetID int64, command string) (string, error) {
	return aitool.ExecOnAsset(ctx, assetID, command)
}

// `opsctl exec <asset> -- <tool>` with arguments the tool refuses (RejectArgs) is
// denied with the tool's reason — no approval dialog — and audited as a deny whose
// error column carries that reason. Everything below the socket is real: the
// unified exec handler, the extension's policy check, a WASM guest and the audit
// writer.
func TestOpsctlExtExecOfRejectedArgumentsIsDeniedAndAudited(t *testing.T) {
	extreg.SetConnTestRegistrar(unusedConnTest{})
	t.Cleanup(func() { extreg.SetConnTestRegistrar(nil) })
	ext := extensiontest.LoadFixture(t)
	require.NoError(t, extreg.Register(ext))
	t.Cleanup(func() { extreg.Unregister(ext.Name) })

	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	asset := &asset_entity.Asset{ID: 1, Name: "fixture-1", Type: "fixture"}
	assets.EXPECT().Find(gomock.Any(), int64(1)).Return(asset, nil).AnyTimes()
	assets.EXPECT().FindByName(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	origAssets := asset_repo.Asset()
	asset_repo.RegisterAsset(assets)
	audits := &recordingAuditRepo{}
	origAudit := audit_repo.Audit()
	audit_repo.RegisterAudit(audits)
	t.Cleanup(func() {
		asset_repo.RegisterAsset(origAssets)
		audit_repo.RegisterAudit(origAudit)
	})

	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: unifiedExecExecutor{}}
	dialogs := 0
	o.emit = func(name string, payload map[string]any) {
		if name != "opsctl:approval" {
			return
		}
		dialogs++
		go o.RespondOpsctlApproval(payload["confirm_id"].(string), permission.ApprovalResponse{Decision: "deny"})
	}

	const reason = `resource "http://evil/x" must not name a host`
	resp := o.handleExtToolExec(approval.ApprovalRequest{SessionID: "opsctl-sess", AssetID: 1, Command: "reject_host --resource=http://evil/x"})

	assert.Equal(t, 0, dialogs, "a call the tool refuses must not ask the user")
	assert.False(t, resp.Approved)
	assert.Contains(t, resp.ToolError, reason)

	require.Len(t, audits.rows, 1)
	row := audits.rows[0]
	assert.Equal(t, "opsctl", row.Source)
	assert.Equal(t, "deny", row.Decision)
	assert.Equal(t, "policy_deny", row.DecisionSource)
	assert.Equal(t, 0, row.Success)
	assert.Contains(t, row.Error, reason, "the audit row shows why the call was refused")
	assert.Equal(t, "reject_host --resource=http://evil/x", row.Command)
}
