package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
)

// answerOrDeny is the user's click reaching RespondOpsctlApproval. An answer the
// backend refuses leaves the approval pending and shows the reason; the stand-in
// user then denies, so a refused answer can only ever end as a denial.
func answerOrDeny(o *Opsctl, confirmID string, resp permission.ApprovalResponse) {
	if err := o.RespondOpsctlApproval(confirmID, resp); err != nil {
		_ = o.RespondOpsctlApproval(confirmID, permission.ApprovalResponse{Decision: "deny"})
	}
}

// answeringDialog stands in for the frontend's "opsctl:approval" dialog: it
// records what the dialog was shown and answers with resp (answerOrDeny).
func answeringDialog(o *Opsctl, resp permission.ApprovalResponse) *map[string]any {
	shown := new(map[string]any)
	o.emit = func(name string, payload map[string]any) {
		if name != "opsctl:approval" {
			return
		}
		*shown = payload
		go answerOrDeny(o, payload["confirm_id"].(string), resp)
	}
	return shown
}

// On the opsctl socket, ApproveGrant means "the whole session was approved" (a
// grant approval): the CLI then writes the session as its active one. Allowing
// one command always must not tell it that.
func TestRequestSingleApprovalAllowAllDoesNotApproveTheSession(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	answeringDialog(o, permission.ApprovalResponse{Decision: "allowAll"})

	resp := o.requestSingleApproval(context.Background(), approval.ApprovalRequest{
		Type: "exec", AssetID: 7, AssetName: "web-01", Command: "uptime", SessionID: "session-1",
	})

	require.True(t, resp.Approved)
	require.False(t, resp.ApproveGrant)
	require.Empty(t, resp.EditedItems)
}
