package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
)

// answeringDialog stands in for the frontend's "opsctl:approval" dialog: it
// records what the dialog was shown and answers with resp, the way the user's
// click reaches RespondOpsctlApproval.
func answeringDialog(o *Opsctl, resp permission.ApprovalResponse) *map[string]any {
	shown := new(map[string]any)
	o.emit = func(name string, payload map[string]any) {
		if name != "opsctl:approval" {
			return
		}
		*shown = payload
		go o.RespondOpsctlApproval(payload["confirm_id"].(string), resp)
	}
	return shown
}

// The approval of an extension-tool call must show what the call is classified
// as — the (action, resource) the user is actually approving — and hand the
// user's "always allow" (edits included) back to HandleConfirm, which persists
// the classification grant.
func TestExtToolConfirmShowsClassificationAndReturnsTheAnswer(t *testing.T) {
	// A loaded extension registers its type's policy check; that is what makes its
	// approvals grantable ("always allow") at all.
	require.NoError(t, permission.RegisterPolicyCheck("notebook", func(context.Context, int64, string) aictx.CheckResult {
		return aictx.CheckResult{Decision: aictx.NeedConfirm}
	}, nil))
	t.Cleanup(func() { permission.UnregisterPolicyCheck("notebook") })

	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	// For a classified call the Remember editor edits the grant tail
	// (<action>:<resource-glob>), not the command text.
	edited := []permission.ApprovalItem{{Type: "notebook", AssetID: 7, Command: "write:runbook/*"}}
	shown := answeringDialog(o, permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

	item := permission.ApprovalItem{
		Type: "notebook", AssetID: 7, AssetName: "nb",
		Command: `note_put --json='{"key":"runbook/a"}'`, Detail: `{"tool":"note_put"}`,
		Action: "write", Resource: "runbook/a", RememberPattern: "write:runbook/a",
	}
	resp := o.extToolConfirm("session-1", opsctlOrigin)(context.Background(), permission.ApprovalKindFor(item.Type, item.Command), []permission.ApprovalItem{item})

	require.Equal(t, "write", (*shown)["action"])
	require.Equal(t, "runbook/a", (*shown)["resource"])
	require.Equal(t, "allowAll", resp.Decision)
	require.Len(t, resp.EditedItems, 1)
	require.Equal(t, edited[0].Command, resp.EditedItems[0].Command)
}

// On the opsctl socket, ApproveGrant means "the whole session was approved" (a
// grant approval): the CLI then writes the session as its active one. Allowing
// one command always must not tell it that.
func TestRequestSingleApprovalAllowAllDoesNotApproveTheSession(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	answeringDialog(o, permission.ApprovalResponse{Decision: "allowAll"})

	resp := o.requestSingleApproval(approval.ApprovalRequest{
		Type: "exec", AssetID: 7, AssetName: "web-01", Command: "uptime", SessionID: "session-1",
	})

	require.True(t, resp.Approved)
	require.False(t, resp.ApproveGrant)
	require.Empty(t, resp.EditedItems)
}
