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

func TestGrantItemsForPersistenceKeepsRawSubjects(t *testing.T) {
	secret := "grant-" + "credential-sentinel"
	reqItems := []approvalpkg.GrantItem{{
		Type: "exec", AssetID: 1, AssetName: "web-1",
		Command: "--password " + secret, Detail: "raw " + secret,
	}}

	got := grantItemsForPersistence("session-1", reqItems)
	require.Len(t, got, 1)
	require.Equal(t, reqItems[0].Command, got[0].Command)
	require.Equal(t, reqItems[0].Detail, got[0].Detail)
}

// spec Decision 4 / Direct execution：opsctl extension 委托执行把 executor 返回的 bytes
// 与 error 原样交给调用者，不做内容改写——auditredact 仅属 Audit 边界，不得在直接执行
// 通道里改写结果或错误正文。
func TestHandleExtToolExecPassesThroughResultAndError(t *testing.T) {
	t.Run("result bytes unchanged", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background(), lang: opsctlTestLang{}, extExecutor: extToolExecutorStub{
			result: []byte(`{"token":"extension-credential-sentinel","rows":1}`),
		}}
		resp := o.handleExtToolExec(approvalpkg.ApprovalRequest{Extension: "demo", Tool: "read"})
		require.True(t, resp.Approved)
		require.Equal(t, `{"token":"extension-credential-sentinel","rows":1}`, resp.ToolResult)
	})

	t.Run("error text unchanged", func(t *testing.T) {
		execErr := errors.New("Authorization: Basic extension-credential-sentinel")
		o := &Opsctl{ctx: context.Background(), lang: opsctlTestLang{}, extExecutor: extToolExecutorStub{err: execErr}}
		resp := o.handleExtToolExec(approvalpkg.ApprovalRequest{Extension: "demo", Tool: "read"})
		require.False(t, resp.Approved)
		require.Equal(t, execErr.Error(), resp.ToolError)
	})
}

// spec Decision 3：审批响应只经 ParseApprovalResponse 的既有类型/策略校验（deny、
// allow、allowAll、edited_items 的 kind 能力），不再有投影派生的 redacted 门禁。
// 伪造响应按既有白名单校验拒绝；合法响应逐字透传。
func TestRespondOpsctlApprovalPreservesParsedValidation(t *testing.T) {
	expected := []permission.ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime"}}
	edited := []permission.ApprovalItem{{Type: "exec", AssetID: 1, AssetName: "web-1", Command: "uptime *"}}

	t.Run("valid allowAll single passed through", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store("c1", pendingOpsctlApproval{kind: permission.ApprovalKindSingle, items: expected, ch: ch})

		o.RespondOpsctlApproval("c1", permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

		got := <-ch
		require.Equal(t, "allowAll", got.Decision)
		require.Equal(t, edited, got.EditedItems)
	})

	t.Run("batch allowAll denied by kind validation", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store("c2", pendingOpsctlApproval{kind: permission.ApprovalKindBatch, items: expected, ch: ch})

		o.RespondOpsctlApproval("c2", permission.ApprovalResponse{Decision: "allowAll", EditedItems: edited})

		got := <-ch
		require.Equal(t, "deny", got.Decision)
	})

	t.Run("edited_items rejected for allow-once", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store("c3", pendingOpsctlApproval{kind: permission.ApprovalKindOnce, items: expected, ch: ch})

		o.RespondOpsctlApproval("c3", permission.ApprovalResponse{Decision: "allow", EditedItems: edited})

		got := <-ch
		require.Equal(t, "deny", got.Decision)
	})

	t.Run("deny and allow passed through", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		o.pendingOpsctlApprovals.Store("c4", pendingOpsctlApproval{kind: permission.ApprovalKindSingle, items: expected, ch: ch})

		o.RespondOpsctlApproval("c4", permission.ApprovalResponse{Decision: "allow"})
		require.Equal(t, "allow", (<-ch).Decision)

		o.RespondOpsctlApproval("c4", permission.ApprovalResponse{Decision: "deny"})
		require.Equal(t, "deny", (<-ch).Decision)
	})
}
