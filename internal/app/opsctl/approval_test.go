package opsctl

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/opskat/opskat/internal/ai/permission"
	approvalpkg "github.com/opskat/opskat/internal/approval"
)

func TestGrantItemsForPersistenceKeepsRawSubjects(t *testing.T) {
	secret := "grant-" + "credential-sentinel"
	reqItems := []approvalpkg.GrantItem{{
		Type: "exec", AssetID: 1, AssetName: "web-1",
		Command: "--password " + secret, Detail: "raw " + secret,
	}}

	got := grantItemsForPersistence("session-1", reqItems, nil)
	require.Len(t, got, 1)
	require.Equal(t, reqItems[0].Command, got[0].Command)
	require.Equal(t, reqItems[0].Detail, got[0].Detail)
}

func TestRespondOpsctlApprovalInvalidResponseLogOmitsPayload(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	oldLogger := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(oldLogger) })

	secret := "approval-log-" + "credential-sentinel"
	o := &Opsctl{ctx: context.Background()}
	ch := make(chan permission.ApprovalResponse, 1)
	o.pendingOpsctlApprovals.Store("bad-log", pendingOpsctlApproval{
		kind:  permission.ApprovalKindSingle,
		items: []permission.ApprovalItem{{Type: "exec", AssetID: 1, Command: "uptime"}},
		ch:    ch,
	})

	o.RespondOpsctlApproval("bad-log", permission.ApprovalResponse{Decision: "invalid --password " + secret})

	require.Equal(t, "deny", (<-ch).Decision)
	entries := logs.FilterMessage("invalid opsctl approval response denied").All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	require.NotContains(t, fields, "decision")
	require.NotContains(t, fields, "error")
	for _, value := range fields {
		require.False(t, strings.Contains(fmt.Sprint(value), secret), "approval payload leaked in log field: %v", value)
	}
}

// 审批响应只经 ParseApprovalResponse 的既有类型/策略校验（deny、allow、allowAll、
// edited_items 的 kind 能力）；伪造响应按白名单拒绝，合法响应逐字透传。
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
