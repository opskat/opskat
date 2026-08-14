package opsctl

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	approvalpkg "github.com/opskat/opskat/internal/approval"
)

type opsctlTestLang struct{}

func (opsctlTestLang) Lang() string { return "en" }

type extToolExecutorStub struct {
	result []byte
	err    error
}

func (s extToolExecutorStub) ExecuteExtTool(context.Context, string, string, []byte) ([]byte, error) {
	return s.result, s.err
}

func TestGrantItemsForPersistenceSkipsRedactedSubjects(t *testing.T) {
	secret := "grant-" + "credential-sentinel"
	reqItems := []approvalpkg.GrantItem{{Type: "exec", AssetID: 1, Command: "--password " + secret}}

	require.Nil(t, grantItemsForPersistence("session-1", reqItems, true))
	got := grantItemsForPersistence("session-1", reqItems, false)
	require.Len(t, got, 1)
	require.Equal(t, reqItems[0].Command, got[0].Command)
}

func TestHandleExtToolExecRedactsDelegatedResultAndError(t *testing.T) {
	secret := "extension-" + "credential-sentinel"

	t.Run("result", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background(), lang: opsctlTestLang{}, extExecutor: extToolExecutorStub{
			result: []byte(`{"rows":1,"token":"` + secret + `"}`),
		}}
		resp := o.handleExtToolExec(approvalpkg.ApprovalRequest{Extension: "demo", Tool: "read"})
		require.True(t, resp.Approved)
		require.NotContains(t, resp.ToolResult, secret)
		require.Contains(t, resp.ToolResult, "<redacted>")
		require.Contains(t, resp.ToolResult, `"rows":1`)
	})

	t.Run("error", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background(), lang: opsctlTestLang{}, extExecutor: extToolExecutorStub{
			err: errors.New("Authorization: Basic " + secret),
		}}
		resp := o.handleExtToolExec(approvalpkg.ApprovalRequest{Extension: "demo", Tool: "read"})
		require.False(t, resp.Approved)
		require.NotContains(t, resp.ToolError, secret)
		require.Contains(t, resp.ToolError, "<redacted>")
	})
}

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
