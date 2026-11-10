package opsctl

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
)

// abandoningCaller stands in for an opsctl process that leaves while its approval
// dialog is up (exit, Ctrl-C, timeout): once the dialog event named shown is out,
// the request's ctx ends. It records every event the desktop received.
type abandoningCaller struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (c *abandoningCaller) wire(o *Opsctl, shown string, cancel context.CancelFunc) {
	o.emit = func(name string, payload map[string]any) {
		c.mu.Lock()
		c.events = append(c.events, recordedEvent{name: name, payload: payload})
		c.mu.Unlock()
		if name == shown {
			go cancel()
		}
	}
}

func (c *abandoningCaller) closed() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for _, e := range c.events {
		if e.name == "opsctl:approval-closed" {
			ids = append(ids, e.payload["confirm_id"].(string))
		}
	}
	return ids
}

func (c *abandoningCaller) shownID(name, key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.events {
		if e.name == name {
			return e.payload[key].(string)
		}
	}
	return ""
}

func runWithDeadline(t *testing.T, fn func() approval.ApprovalResponse) approval.ApprovalResponse {
	t.Helper()
	done := make(chan approval.ApprovalResponse, 1)
	go func() { done <- fn() }()
	select {
	case resp := <-done:
		return resp
	case <-time.After(2 * time.Second):
		t.Fatal("approval kept waiting after the caller left")
		return approval.ApprovalResponse{}
	}
}

// The caller leaving must close its dialog entry: the desktop is told which one,
// and the request ends denied instead of waiting for a click nobody needs.
func TestSingleApprovalAbandonedByCallerClosesTheDialog(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	caller := &abandoningCaller{}
	caller.wire(o, "opsctl:approval", cancel)

	resp := runWithDeadline(t, func() approval.ApprovalResponse {
		return o.handleRequest(ctx, approval.ApprovalRequest{
			Type: "exec", AssetID: 7, AssetName: "web-01", Command: "uptime", SessionID: "s1",
		})
	})

	require.False(t, resp.Approved)
	id := caller.shownID("opsctl:approval", "confirm_id")
	require.NotEmpty(t, id)
	require.Equal(t, []string{id}, caller.closed())
	require.Error(t, o.RespondOpsctlApproval(id, permission.ApprovalResponse{Decision: "allow"}))
}

func TestBatchApprovalAbandonedByCallerClosesTheDialog(t *testing.T) {
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	caller := &abandoningCaller{}
	caller.wire(o, "opsctl:batch-approval", cancel)

	resp := runWithDeadline(t, func() approval.ApprovalResponse {
		return o.handleRequest(ctx, approval.ApprovalRequest{
			Type: "batch", SessionID: "s1",
			BatchItems: []approval.BatchItem{{Type: "exec", AssetID: 7, AssetName: "web-01", Command: "uptime"}},
		})
	})

	require.False(t, resp.Approved)
	id := caller.shownID("opsctl:batch-approval", "confirm_id")
	require.NotEmpty(t, id)
	require.Equal(t, []string{id}, caller.closed())
}

// A grant dialog is keyed by its session id; abandoning it also rejects the
// pending grant session so it never turns into an approved one later.
func TestGrantApprovalAbandonedByCallerClosesTheDialog(t *testing.T) {
	repo := withGrantExtFixture(t)
	o := &Opsctl{ctx: context.Background(), appCtx: context.Background(), lang: extTestLang{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	caller := &abandoningCaller{}
	caller.wire(o, "opsctl:grant-approval", cancel)

	resp := runWithDeadline(t, func() approval.ApprovalResponse {
		return o.handleRequest(ctx, grantRequest("read"))
	})

	require.False(t, resp.Approved)
	require.Equal(t, []string{"opsctl-sess"}, caller.closed())
	require.Equal(t, grant_entity.GrantStatusRejected, repo.sessions["opsctl-sess"].Status)
}

// An answer the backend cannot take is reported to the dialog, not turned into a
// silent deny: the approval stays pending so the user can still answer it.
func TestRespondOpsctlApprovalReportsRejectedAnswers(t *testing.T) {
	t.Run("approval no longer pending", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		require.Error(t, o.RespondOpsctlApproval("gone", permission.ApprovalResponse{Decision: "allow"}))
	})

	t.Run("invalid remember pattern keeps the approval pending", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		expected := []permission.ApprovalItem{{Type: "exec", AssetID: 1, Command: "uptime"}}
		o.pendingOpsctlApprovals.Store("c1", pendingOpsctlApproval{kind: permission.ApprovalKindSingle, items: expected, ch: ch})

		err := o.RespondOpsctlApproval("c1", permission.ApprovalResponse{
			Decision:    "allowAll",
			EditedItems: []permission.ApprovalItem{{Type: "exec", AssetID: 1, Command: "  "}},
		})

		require.ErrorContains(t, err, "command is required")
		require.Empty(t, ch, "a rejected answer must not resolve the approval")
		require.NoError(t, o.RespondOpsctlApproval("c1", permission.ApprovalResponse{Decision: "allow"}))
		require.Equal(t, "allow", (<-ch).Decision)
	})

	t.Run("a second answer is refused", func(t *testing.T) {
		o := &Opsctl{ctx: context.Background()}
		ch := make(chan permission.ApprovalResponse, 1)
		expected := []permission.ApprovalItem{{Type: "exec", AssetID: 1, Command: "uptime"}}
		o.pendingOpsctlApprovals.Store("c2", pendingOpsctlApproval{kind: permission.ApprovalKindSingle, items: expected, ch: ch})

		require.NoError(t, o.RespondOpsctlApproval("c2", permission.ApprovalResponse{Decision: "deny"}))
		require.Error(t, o.RespondOpsctlApproval("c2", permission.ApprovalResponse{Decision: "allow"}))
		require.Equal(t, "deny", (<-ch).Decision)
	})
}
