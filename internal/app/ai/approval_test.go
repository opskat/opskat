package ai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
)

// spec Decision 3：审批响应只经 ParseApprovalResponse 的既有类型/策略校验（deny、
// allow、allowAll、edited_items 的 kind 能力），不再有投影派生的 redacted 门禁。
// 伪造响应按既有白名单校验拒绝；合法响应逐字透传。
func TestRespondAIApprovalPreservesParsedValidation(t *testing.T) {
	expected := []permission.ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime"}}
	edited := []permission.ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime *"}}

	t.Run("valid allowAll single passed through", func(t *testing.T) {
		a := &AI{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store("c1", pendingAIApproval{kind: permission.ApprovalKindSingle, items: expected, ch: ch})

		a.RespondAIApproval("c1", permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

		got := <-ch
		require.Equal(t, "allowAll", got.Decision)
		require.Equal(t, edited, got.EditedItems)
	})

	t.Run("batch allowAll denied by kind validation", func(t *testing.T) {
		a := &AI{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store("c2", pendingAIApproval{kind: permission.ApprovalKindBatch, items: expected, ch: ch})

		a.RespondAIApproval("c2", permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

		got := <-ch
		require.Equal(t, "deny", got.Decision)
	})

	t.Run("edited_items rejected for allow-once", func(t *testing.T) {
		a := &AI{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store("c3", pendingAIApproval{kind: permission.ApprovalKindOnce, items: expected, ch: ch})

		a.RespondAIApproval("c3", permission.ApprovalResponse{Decision: "allow", EditedItems: edited})

		got := <-ch
		require.Equal(t, "deny", got.Decision)
	})

	t.Run("deny and allow passed through", func(t *testing.T) {
		a := &AI{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store("c4", pendingAIApproval{kind: permission.ApprovalKindSingle, items: expected, ch: ch})

		a.RespondAIApproval("c4", permission.ApprovalResponse{Decision: "allow"})
		require.Equal(t, "allow", (<-ch).Decision)

		a.RespondAIApproval("c4", permission.ApprovalResponse{Decision: "deny"})
		require.Equal(t, "deny", (<-ch).Decision)
	})
}
