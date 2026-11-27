package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
)

// A grant request for a built-in asset (request_permission over opsctl mcp's desktop mode)
// is persisted the way exec's check splits a command: a compound pattern becomes its
// sub-commands under the asset's real approval type. Stored whole, no sub-command of
// `ls /tmp && cat /etc/hosts` would ever match the grant row.

func withSSHGrantFixture(t *testing.T) *memGrantRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	assets.EXPECT().Find(gomock.Any(), int64(4)).
		Return(&asset_entity.Asset{ID: 4, Name: "web-1", Type: asset_entity.AssetTypeSSH}, nil).AnyTimes()
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

func sshGrantRequest(command string) approval.ApprovalRequest {
	return approval.ApprovalRequest{
		Type: "grant", SessionID: "opsctl-sess", Description: "deploy",
		GrantItems: []approval.GrantItem{{Type: "grant", AssetID: 4, AssetName: "web-1", Command: command}},
	}
}

func persistedGrantRows(repo *memGrantRepo) (commands, tools []string) {
	for _, item := range repo.items["opsctl-sess"] {
		commands = append(commands, item.Command)
		tools = append(tools, item.ToolName)
	}
	return commands, tools
}

func TestHandleGrantApprovalSplitsCompoundBuiltinPattern(t *testing.T) {
	repo := withSSHGrantFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	approvingGrantDialog(o, permission.ApprovalResponse{Decision: "allow"})

	resp := o.handleGrantApproval(context.Background(), sshGrantRequest("ls /tmp && cat /etc/hosts"))

	require.True(t, resp.Approved)
	commands, tools := persistedGrantRows(repo)
	require.ElementsMatch(t, []string{"ls /tmp", "cat /etc/hosts"}, commands)
	require.Equal(t, []string{"exec", "exec"}, tools)
}

func TestHandleGrantApprovalSplitsCompoundBuiltinEdit(t *testing.T) {
	repo := withSSHGrantFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	approvingGrantDialog(o, permission.ApprovalResponse{Decision: "allow", EditedItems: []permission.ApprovalItem{{
		Type: "grant", AssetID: 4, AssetName: "web-1", Command: "systemctl status nginx && journalctl -u nginx",
	}}})

	resp := o.handleGrantApproval(context.Background(), sshGrantRequest("systemctl status nginx"))

	require.True(t, resp.Approved)
	commands, tools := persistedGrantRows(repo)
	require.ElementsMatch(t, []string{"systemctl status nginx", "journalctl -u nginx"}, commands)
	require.Equal(t, []string{"exec", "exec"}, tools)
}
