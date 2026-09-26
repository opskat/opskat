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
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// A grant request over the opsctl approval channel for an extension asset is written <action>[:<resource-glob>] and
// persisted as ext:<policyType>:… — the only grant shape an extension call matches.
// One that could never match is refused before anything is stored or shown.

const grantExtTestType = "esgrant-opsctl-test"

type memGrantRepo struct {
	sessions map[string]*grant_entity.GrantSession
	items    map[string][]*grant_entity.GrantItem
}

func (r *memGrantRepo) CreateSession(_ context.Context, s *grant_entity.GrantSession) error {
	r.sessions[s.ID] = s
	return nil
}
func (r *memGrantRepo) GetSession(_ context.Context, id string) (*grant_entity.GrantSession, error) {
	return r.sessions[id], nil
}
func (r *memGrantRepo) UpdateSessionStatus(_ context.Context, id string, status int) error {
	r.sessions[id].Status = status
	return nil
}
func (r *memGrantRepo) CreateItems(_ context.Context, items []*grant_entity.GrantItem) error {
	for _, item := range items {
		r.items[item.GrantSessionID] = append(r.items[item.GrantSessionID], item)
	}
	return nil
}
func (r *memGrantRepo) UpdateItems(_ context.Context, id string, items []*grant_entity.GrantItem) error {
	r.items[id] = items
	return nil
}
func (r *memGrantRepo) ListItems(_ context.Context, id string) ([]*grant_entity.GrantItem, error) {
	return r.items[id], nil
}
func (r *memGrantRepo) ListApprovedItems(_ context.Context, id string) ([]*grant_entity.GrantItem, error) {
	if s := r.sessions[id]; s == nil || s.Status != grant_entity.GrantStatusApproved {
		return nil, nil
	}
	return r.items[id], nil
}

func withGrantExtFixture(t *testing.T) *memGrantRepo {
	t.Helper()
	require.NoError(t, permission.RegisterPolicyCheck(grantExtTestType,
		func(context.Context, int64, string) aictx.CheckResult {
			return aictx.CheckResult{Decision: aictx.NeedConfirm}
		}, nil))
	t.Cleanup(func() { permission.UnregisterPolicyCheck(grantExtTestType) })
	require.NoError(t, permission.RegisterExtensionRuleSink(grantExtTestType, "esgrant", []string{"read", "delete"}))
	t.Cleanup(func() { permission.UnregisterRuleSink(grantExtTestType) })

	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	assets.EXPECT().Find(gomock.Any(), int64(3)).
		Return(&asset_entity.Asset{ID: 3, Name: "es-logs", Type: grantExtTestType}, nil).AnyTimes()
	origAssets := asset_repo.Asset()
	asset_repo.RegisterAsset(assets)

	repo := &memGrantRepo{sessions: map[string]*grant_entity.GrantSession{}, items: map[string][]*grant_entity.GrantItem{}}
	origGrant := grant_repo.Grant()
	grant_repo.RegisterGrant(repo)
	t.Cleanup(func() {
		asset_repo.RegisterAsset(origAssets)
		grant_repo.RegisterGrant(origGrant)
	})
	return repo
}

// approvingGrantDialog answers the "opsctl:grant-approval" dialog with resp and
// counts how often it was shown.
func approvingGrantDialog(o *Opsctl, resp permission.ApprovalResponse) *int {
	shown := new(int)
	o.emit = func(name string, payload map[string]any) {
		if name != "opsctl:grant-approval" {
			return
		}
		*shown++
		go o.RespondOpsctlApproval(payload["session_id"].(string), resp)
	}
	return shown
}

func grantRequest(command string) approval.ApprovalRequest {
	return approval.ApprovalRequest{
		Type: "grant", SessionID: "opsctl-sess", Description: "cleanup",
		GrantItems: []approval.GrantItem{{Type: "exec", AssetID: 3, AssetName: "es-logs", Command: command}},
	}
}

func TestHandleGrantApprovalPersistsExtensionGrantsThatMatch(t *testing.T) {
	repo := withGrantExtFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	approvingGrantDialog(o, permission.ApprovalResponse{Decision: "allow"})

	resp := o.handleGrantApproval(grantRequest("delete:logs-*\nread"))

	require.True(t, resp.Approved)
	persisted := make([]string, 0, len(repo.items["opsctl-sess"]))
	for _, item := range repo.items["opsctl-sess"] {
		persisted = append(persisted, item.Command)
	}
	require.Equal(t, []string{"ext:esgrant:delete:logs-*", "ext:esgrant:read"}, persisted)

	ctx := aictx.WithSessionID(context.Background(), "opsctl-sess")
	_, ok := permission.MatchExtensionGrant(ctx, 3, grantExtTestType, "esgrant", "delete", "logs-app")
	require.True(t, ok, "the next delete on a matching resource must run without a prompt")
	_, ok = permission.MatchExtensionGrant(ctx, 3, grantExtTestType, "esgrant", "delete", "metrics-app")
	require.False(t, ok)
}

func TestHandleGrantApprovalRefusesExtensionPatternsThatCouldNeverMatch(t *testing.T) {
	for _, command := range []string{"delete *", "purge:*", "delete:logs-["} {
		t.Run(command, func(t *testing.T) {
			repo := withGrantExtFixture(t)
			o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
			shown := approvingGrantDialog(o, permission.ApprovalResponse{Decision: "allow"})

			resp := o.handleGrantApproval(grantRequest(command))

			require.False(t, resp.Approved)
			require.False(t, resp.ApproveGrant)
			require.Contains(t, resp.Reason, command)
			require.Zero(t, *shown, "an invalid request must not be shown as grantable")
			require.Empty(t, repo.sessions)
			require.Empty(t, repo.items)
		})
	}
}

func TestHandleGrantApprovalRejectsAnInvalidExtensionEdit(t *testing.T) {
	repo := withGrantExtFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	approvingGrantDialog(o, permission.ApprovalResponse{Decision: "allow", EditedItems: []permission.ApprovalItem{{
		Type: grantExtTestType, AssetID: 3, AssetName: "es-logs", Command: "delete *",
	}}})

	resp := o.handleGrantApproval(grantRequest("delete:logs-*"))

	require.False(t, resp.Approved)
	require.Equal(t, grant_entity.GrantStatusRejected, repo.sessions["opsctl-sess"].Status)
	_, ok := permission.MatchExtensionGrant(aictx.WithSessionID(context.Background(), "opsctl-sess"),
		3, grantExtTestType, "esgrant", "delete", "logs-app")
	require.False(t, ok)
}
