package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
)

// One "opsctl:approval" dialog serves both callers of the extension-tool gate, so
// the event itself has to say who is asking: a page of an extension, named by its
// display name, or opsctl. The dialog titles itself from that, not from a guess.

const originTestType = "esverify-origin-test"

// confirmingExtExecutor stands in for the unified exec handler on a call that
// needs confirmation: it runs the gate's checker exactly as handleExec does.
type confirmingExtExecutor struct{}

func (confirmingExtExecutor) ExecuteExtTool(ctx context.Context, assetID int64, command string) (string, error) {
	checker, err := permission.RequireChecker(ctx)
	if err != nil {
		return "", err
	}
	result := checker.HandleConfirm(ctx, assetID, originTestType, command)
	aictx.RecordDecision(ctx, result)
	return "{}", nil
}

func withClassifiedOriginType(t *testing.T) {
	t.Helper()
	require.NoError(t, permission.RegisterPolicyCheck(originTestType,
		func(context.Context, int64, string) aictx.CheckResult {
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		},
		func(context.Context, string) (permission.ExtensionClassification, bool) {
			return permission.ExtensionClassification{PolicyType: "esverify", Action: "delete", Resource: "logs-app", Tool: "request"}, true
		}))
	t.Cleanup(func() { permission.UnregisterPolicyCheck(originTestType) })

	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	assets.EXPECT().Find(gomock.Any(), int64(3)).
		Return(&asset_entity.Asset{ID: 3, Name: "es-logs", Type: originTestType}, nil).AnyTimes()
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(assets)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
}

func TestPageApprovalNamesTheExtensionPage(t *testing.T) {
	withClassifiedOriginType(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: confirmingExtExecutor{}}
	shown := answeringDialog(o, permission.ApprovalResponse{Decision: "allow"})

	_, err := o.RunPageToolCall(context.Background(), "ES Verify", 3, "request --method=DELETE --path=/logs-app")

	require.NoError(t, err)
	require.Equal(t, "extension_page", (*shown)["source"])
	require.Equal(t, "ES Verify", (*shown)["extension"])
	require.Equal(t, "delete:logs-app", (*shown)["remember_pattern"],
		"the dialog's Remember editor is pre-filled with the grant tail, not the command")
}

func TestOpsctlExtToolApprovalKeepsOpsctlOrigin(t *testing.T) {
	withClassifiedOriginType(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}, extExecutor: confirmingExtExecutor{}}
	shown := answeringDialog(o, permission.ApprovalResponse{Decision: "allow"})

	resp := o.handleExtToolExec(approval.ApprovalRequest{AssetID: 3, Command: "request --method=DELETE --path=/logs-app"})

	require.True(t, resp.Approved)
	require.Equal(t, "opsctl", (*shown)["source"])
	require.Empty(t, (*shown)["extension"])
}

func TestOpsctlSingleApprovalKeepsOpsctlOrigin(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	shown := answeringDialog(o, permission.ApprovalResponse{Decision: "allow"})

	resp := o.requestSingleApproval(approval.ApprovalRequest{Type: "exec", AssetID: 7, AssetName: "web-01", Command: "uptime"})

	require.True(t, resp.Approved)
	require.Equal(t, "opsctl", (*shown)["source"])
}
