package opsctl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
)

// 与 internal/app/ai/approval_test.go 同一套门禁语义：审批主体被脱敏时后端拒绝伪造的
// allowAll / grant edited_items，只放行 deny 与 allow-once（spec Approval safety）。
func TestRespondOpsctlApprovalRejectsForgedPersistWhenRedacted(t *testing.T) {
	expected := []permission.ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "--password secret"}}
	edited := []permission.ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime *"}}

	t.Run("forged allowAll denied", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store("c1", pendingOpsctlApproval{kind: permission.ApprovalKindSingle, items: expected, redacted: true, ch: ch})

		o.RespondOpsctlApproval("c1", permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

		got := <-ch
		require.Equal(t, "deny", got.Decision)
		require.Empty(t, got.EditedItems)
	})

	t.Run("redacted grant allow denied", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store("c2", pendingOpsctlApproval{kind: permission.ApprovalKindGrant, items: expected, redacted: true, ch: ch})

		o.RespondOpsctlApproval("c2", permission.ApprovalResponse{Decision: "allow", EditedItems: edited})

		got := <-ch
		require.Equal(t, "deny", got.Decision)
	})

	t.Run("allow-once preserved when redacted", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store("c3", pendingOpsctlApproval{kind: permission.ApprovalKindSingle, items: expected, redacted: true, ch: ch})

		o.RespondOpsctlApproval("c3", permission.ApprovalResponse{Decision: "allow"})

		got := <-ch
		require.Equal(t, "allow", got.Decision)
	})

	t.Run("allowAll preserved when not redacted (compat)", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store("c4", pendingOpsctlApproval{kind: permission.ApprovalKindSingle, items: expected, redacted: false, ch: ch})

		o.RespondOpsctlApproval("c4", permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

		got := <-ch
		require.Equal(t, "allowAll", got.Decision)
	})
}
