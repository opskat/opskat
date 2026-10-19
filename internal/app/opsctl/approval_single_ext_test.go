package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
)

// opsctl runs an extension call itself and asks the desktop only for approval. Its
// request carries the classification of the shared approval item
// (permission.ApprovalItemFor): the dialog shows that extension item, and "always
// allow" saves the extension grant ext:<policyType>:<action>:<resource> the next
// identical call is matched by — not the command text, which no extension call ever
// matches.

const extSingleCommand = "request --method=DELETE --path=/logs-app"

func extSingleRequest() approval.ApprovalRequest {
	return approval.ApprovalRequest{
		Type: grantExtTestType, AssetID: 3, AssetName: "es-logs", SessionID: "opsctl-sess",
		Command:         extSingleCommand,
		Detail:          `{"tool": "request", "args": {"method": "DELETE", "path": "/logs-app"}}`,
		Action:          "delete",
		Resource:        "logs-app",
		Resources:       []string{"logs-app"},
		RememberPattern: "delete:logs-app",
	}
}

// withExtSingleFixture is withGrantExtFixture with the request's session already in
// the store: the in-memory repo answers a missing session with (nil, nil), where the
// real one errors and SaveGrantPattern then creates it.
func withExtSingleFixture(t *testing.T) *memGrantRepo {
	t.Helper()
	repo := withGrantExtFixture(t)
	repo.sessions["opsctl-sess"] = &grant_entity.GrantSession{ID: "opsctl-sess", Status: grant_entity.GrantStatusApproved}
	return repo
}

func extGrantCovers(t *testing.T, resources ...string) bool {
	t.Helper()
	_, ok := permission.MatchExtensionGrant(aictx.WithSessionID(context.Background(), "opsctl-sess"),
		3, grantExtTestType, "esgrant", "delete", resources)
	return ok
}

func TestRequestSingleApprovalShowsTheExtensionItem(t *testing.T) {
	withExtSingleFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	shown := answeringDialog(o, permission.ApprovalResponse{Decision: "allow"})

	resp := o.requestSingleApproval(extSingleRequest())

	require.True(t, resp.Approved)
	require.Equal(t, "delete", (*shown)["action"])
	require.Equal(t, "logs-app", (*shown)["resource"])
	require.Equal(t, []string{"logs-app"}, (*shown)["resources"])
	require.Equal(t, "delete:logs-app", (*shown)["remember_pattern"])
	require.Equal(t, extSingleRequest().Detail, (*shown)["detail"])
	require.Equal(t, permission.ApprovalKindSingle, (*shown)["kind"])
}

func TestRequestSingleApprovalAllowAllSavesTheExtensionGrant(t *testing.T) {
	repo := withExtSingleFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	answeringDialog(o, permission.ApprovalResponse{Decision: "allowAll"})

	resp := o.requestSingleApproval(extSingleRequest())

	require.True(t, resp.Approved)
	require.Len(t, repo.items["opsctl-sess"], 1)
	require.Equal(t, "ext:esgrant:delete:logs-app", repo.items["opsctl-sess"][0].Command)
	require.Equal(t, grantExtTestType, repo.items["opsctl-sess"][0].ToolName)
	require.True(t, extGrantCovers(t, "logs-app"), "the next identical call runs without a prompt")
	require.False(t, extGrantCovers(t, "logs-web"), "the grant covers the resource the user saw only")
}

func TestRequestSingleApprovalAllowAllSavesTheEditedExtensionGrant(t *testing.T) {
	repo := withExtSingleFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	answeringDialog(o, permission.ApprovalResponse{Decision: "allowAll", EditedItems: []permission.ApprovalItem{{
		Type: grantExtTestType, AssetID: 3, AssetName: "es-logs", Command: "delete:logs-*",
	}}})

	resp := o.requestSingleApproval(extSingleRequest())

	require.True(t, resp.Approved)
	require.Len(t, repo.items["opsctl-sess"], 1)
	require.Equal(t, "ext:esgrant:delete:logs-*", repo.items["opsctl-sess"][0].Command)
	require.True(t, extGrantCovers(t, "logs-web"))
}

// An edit that drops the classified action is refused, and nothing is granted.
func TestRequestSingleApprovalRefusesAnExtensionEditChangingTheAction(t *testing.T) {
	repo := withExtSingleFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	answeringDialog(o, permission.ApprovalResponse{Decision: "allowAll", EditedItems: []permission.ApprovalItem{{
		Type: grantExtTestType, AssetID: 3, AssetName: "es-logs", Command: "read",
	}}})

	resp := o.requestSingleApproval(extSingleRequest())

	require.False(t, resp.Approved)
	require.Empty(t, repo.items)
}

// A call that touches no resource is granted for exactly that: the same call again,
// not the action on every resource.
func TestRequestSingleApprovalAllowAllOfACallWithoutResource(t *testing.T) {
	repo := withExtSingleFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	answeringDialog(o, permission.ApprovalResponse{Decision: "allowAll"})
	req := extSingleRequest()
	req.Resource, req.Resources, req.RememberPattern = "", nil, "delete:"

	resp := o.requestSingleApproval(req)

	require.True(t, resp.Approved)
	require.Len(t, repo.items["opsctl-sess"], 1)
	require.Equal(t, "ext:esgrant:delete:", repo.items["opsctl-sess"][0].Command)
	require.True(t, extGrantCovers(t))
	require.False(t, extGrantCovers(t, "logs-app"))
}
