package ai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
)

func TestSafeLocalApprovalPatternsDoNotSendSecretPatternsToWails(t *testing.T) {
	patterns := []string{"echo --password local-secret", "/tmp/safe/*"}
	require.Nil(t, safeLocalApprovalPatterns(patterns, true))
	require.Equal(t, patterns, safeLocalApprovalPatterns(patterns, false))
}

// 审批主体被脱敏时（redacted=true），后端必须拒绝伪造的 allowAll / grant edited_items，
// 只放行 deny 与 allow-once——<redacted> 不能成为授权 pattern，原始秘密也不能经编辑
// 响应回传或持久化（spec Approval safety / Compatibility）。前端隐藏按钮不可作为信任
// 依据，后端是最终防线。
func TestRespondAIApprovalRejectsForgedPersistWhenRedacted(t *testing.T) {
	expected := []permission.ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "--password secret"}}
	edited := []permission.ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime *"}}

	t.Run("forged allowAll denied", func(t *testing.T) {
		a := &AI{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store("c1", pendingAIApproval{kind: permission.ApprovalKindSingle, items: expected, redacted: true, ch: ch})

		a.RespondAIApproval("c1", permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

		got := <-ch
		require.Equal(t, "deny", got.Decision)
		require.Empty(t, got.EditedItems)
	})

	t.Run("redacted grant allow denied", func(t *testing.T) {
		a := &AI{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store("c2", pendingAIApproval{kind: permission.ApprovalKindGrant, items: expected, redacted: true, ch: ch})

		a.RespondAIApproval("c2", permission.ApprovalResponse{Decision: "allow", EditedItems: edited})

		got := <-ch
		require.Equal(t, "deny", got.Decision)
	})

	t.Run("allow-once preserved when redacted", func(t *testing.T) {
		a := &AI{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store("c3", pendingAIApproval{kind: permission.ApprovalKindSingle, items: expected, redacted: true, ch: ch})

		a.RespondAIApproval("c3", permission.ApprovalResponse{Decision: "allow"})

		got := <-ch
		require.Equal(t, "allow", got.Decision)
	})

	t.Run("allowAll preserved when not redacted (compat)", func(t *testing.T) {
		a := &AI{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		a.pendingAIApprovals.Store("c4", pendingAIApproval{kind: permission.ApprovalKindSingle, items: expected, redacted: false, ch: ch})

		a.RespondAIApproval("c4", permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

		got := <-ch
		require.Equal(t, "allowAll", got.Decision)
	})
}
