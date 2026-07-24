package ai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
)

func TestRespondAIApprovalInvalidResponseFailsClosedAtBoundary(t *testing.T) {
	tests := []struct {
		name string
		resp permission.ApprovalResponse
	}{
		{name: "empty", resp: permission.ApprovalResponse{}},
		{name: "unknown", resp: permission.ApprovalResponse{Decision: "bogus"}},
		{name: "case variant", resp: permission.ApprovalResponse{Decision: "ALLOW"}},
		{
			name: "malformed edits",
			resp: permission.ApprovalResponse{
				Decision:    "allowAll",
				EditedItems: []permission.ApprovalItem{{Type: "exec", Command: "  "}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &AI{ctx: context.Background()}
			ch := make(chan permission.ApprovalResponse, 1)
			expected := []permission.ApprovalItem{{Type: "exec", Command: "review"}}
			a.pendingAIApprovals.Store("confirm-1", pendingAIApproval{kind: permission.ApprovalKindSingle, items: expected, ch: ch})

			a.RespondAIApproval("confirm-1", tc.resp)

			got := <-ch
			require.Equal(t, "deny", got.Decision)
			require.Empty(t, got.EditedItems)
		})
	}
}
