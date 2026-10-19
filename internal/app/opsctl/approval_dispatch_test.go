package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
)

type extTestLang struct{}

func (extTestLang) Lang() string { return "en" }

// The desktop no longer executes extension tools on opsctl's behalf: opsctl runs
// them in its own process. A request still carrying the retired "ext_tool" type
// is handled like any other unknown type — it reaches the generic single
// approval dialog and a refusal comes back as a plain denial.
func TestHandleRequestTreatsRetiredExtToolTypeAsUnknown(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	shown := answeringDialog(o, permission.ApprovalResponse{Decision: "deny"})

	resp := o.handleRequest(context.Background(), approval.ApprovalRequest{
		Type: "ext_tool", AssetID: 3, Command: "note_put --json='{}'", SessionID: "session-1",
	})

	require.NotNil(t, *shown, "an unknown type is put to the user in the generic approval dialog")
	require.False(t, resp.Approved)
}
