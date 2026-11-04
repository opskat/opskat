package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
)

// answeringBatchDialog stands in for the frontend's batch approval dialog: it
// records the items the dialog was shown and answers with resp.
func answeringBatchDialog(o *Opsctl, resp permission.ApprovalResponse) *[]map[string]any {
	shown := new([]map[string]any)
	o.emit = func(name string, payload map[string]any) {
		if name != "opsctl:batch-approval" {
			return
		}
		*shown = payload["items"].([]map[string]any)
		go o.RespondOpsctlApproval(payload["confirm_id"].(string), resp)
	}
	return shown
}

func extBatchRequest() approval.ApprovalRequest {
	return approval.ApprovalRequest{
		Type: "batch", SessionID: "opsctl-batch",
		BatchItems: []approval.BatchItem{
			{
				Type: grantExtTestType, AssetID: 3, AssetName: "es-logs", Command: extSingleCommand,
				Action: "delete", Resource: "logs-app", Resources: []string{"logs-app"}, RememberPattern: "delete:logs-app",
			},
			{Type: "exec", AssetID: 7, AssetName: "web-01", Command: "uptime"},
		},
	}
}

// opsctl batch sends one request for every item that needs approval; the dialog
// shows each extension item's action and resources the way the single dialog does,
// and built-in items exactly as before.
func TestHandleBatchApprovalShowsTheExtensionItems(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	shown := answeringBatchDialog(o, permission.ApprovalResponse{Decision: "allow"})

	resp := o.handleBatchApproval(extBatchRequest())

	require.True(t, resp.Approved)
	require.Len(t, *shown, 2)
	ext, builtin := (*shown)[0], (*shown)[1]
	require.Equal(t, "delete", ext["action"])
	require.Equal(t, "logs-app", ext["resource"])
	require.Equal(t, []string{"logs-app"}, ext["resources"])
	require.Equal(t, extSingleCommand, ext["command"])
	require.Equal(t, map[string]any{
		"type": "exec", "asset_id": int64(7), "asset_name": "web-01", "command": "uptime", "detail": "",
		"action": "", "resource": "", "resources": []string(nil), "review": (*aictx.ReviewInfo)(nil),
	}, builtin)
}

// Batch approval stays allow once / deny for extension items too: an "always
// allow" answer is refused, not taken as approval.
func TestHandleBatchApprovalRefusesAllowAllForExtensionItems(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	answeringBatchDialog(o, permission.ApprovalResponse{Decision: "allowAll"})

	resp := o.handleBatchApproval(extBatchRequest())

	require.False(t, resp.Approved)
}
