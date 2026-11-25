package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/sshagent"
)

const testStrongSecret = "opskat-mcp-strong-secret-0123456789"

type mcpTestClient struct {
	t      *testing.T
	writer io.Writer
	reader *bufio.Reader
	nextID atomic.Int64
}

func startTestServer(t *testing.T, cfg ServerConfig) (*mcpTestClient, func()) {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	srv := NewServer(cfg)

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, serverR, serverW)
	}()

	client := &mcpTestClient{
		t:      t,
		writer: clientW,
		reader: bufio.NewReader(clientR),
	}

	cleanup := func() {
		cancel()
		_ = clientW.Close()
		_ = clientR.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("mcp server did not shut down cleanly")
		}
	}
	return client, cleanup
}

func startTestControlServer(t *testing.T, approver *ExternalApprover) (string, func()) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "mcp-control.sock")
	ctrl := NewControlServer(approver)
	require.NoError(t, ctrl.Start(sockPath))
	return sockPath, ctrl.Stop
}

func (c *mcpTestClient) call(method string, params any) Response {
	c.t.Helper()
	id := c.nextID.Add(1)
	return c.callWithID(id, method, params)
}

func (c *mcpTestClient) callWithID(id int64, method string, params any) Response {
	c.t.Helper()
	rawID, err := json.Marshal(id)
	require.NoError(c.t, err)
	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		require.NoError(c.t, err)
	}
	reqBytes, err := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      rawID,
		Method:  method,
		Params:  rawParams,
	})
	require.NoError(c.t, err)
	_, err = c.writer.Write(append(reqBytes, '\n'))
	require.NoError(c.t, err)

	line, err := c.reader.ReadBytes('\n')
	require.NoError(c.t, err)
	var resp Response
	require.NoError(c.t, json.Unmarshal(line, &resp))
	return resp
}

func (c *mcpTestClient) sendNotification(method string, params any) {
	c.t.Helper()
	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		require.NoError(c.t, err)
	}
	reqBytes, err := json.Marshal(Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
	})
	require.NoError(c.t, err)
	_, err = c.writer.Write(append(reqBytes, '\n'))
	require.NoError(c.t, err)
}

func decodeResult[T any](t *testing.T, resp Response) T {
	t.Helper()
	require.Nil(t, resp.Error, "unexpected RPC error: %+v", resp.Error)
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func decodeStructured[T any](t *testing.T, res CallToolResult) T {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

type recordingAuditWriter struct {
	mu      sync.Mutex
	entries []audit.ToolCallInfo
	sources []string
}

func (w *recordingAuditWriter) WriteToolCall(ctx context.Context, info audit.ToolCallInfo) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, info)
	w.sources = append(w.sources, aictx.GetAuditSource(ctx))
}

func TestExternalApproverValidatesSecretLengthAndTTL(t *testing.T) {
	_, err := NewExternalApprover(ExternalApproverConfig{Secret: "too-short"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least")

	_, err = NewExternalApprover(ExternalApproverConfig{Secret: testStrongSecret, TTL: MaxApprovalTTL + time.Minute})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
}

func TestServerHandshakeAndDataPlaneNeverExposesResolveTool(t *testing.T) {
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		AuthFailureBackoff: -1,
	})
	require.NoError(t, err)

	echoTool := &tool.RawTool{
		NameStr: "echo",
		DescStr: "Echo message",
		SchemaVal: agent.Schema{
			Type:       "object",
			Properties: map[string]*agent.Property{"msg": {Type: "string"}},
			Required:   []string{"msg"},
		},
		Handler: func(_ context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
			return &agent.ToolResultBlock{
				Content: []agent.ContentBlock{agent.TextBlock{Text: "echo:" + in["msg"].(string)}},
			}, nil
		},
	}

	auditRec := &recordingAuditWriter{}
	client, cleanup := startTestServer(t, ServerConfig{
		ServerName:    "opsctl-test",
		ServerVersion: "1.0.0",
		Tools:         []agent.Tool{echoTool},
		Approver:      approver,
		AuditWriter:   auditRec,
	})
	defer cleanup()

	initResp := client.call("initialize", map[string]any{"protocolVersion": "2025-03-26"})
	require.Nil(t, initResp.Error)
	initMap := decodeResult[map[string]any](t, initResp)
	require.Equal(t, "2025-03-26", initMap["protocolVersion"])

	pingResp := client.call("ping", nil)
	require.Nil(t, pingResp.Error)

	listResp := client.call("tools/list", nil)
	listRes := decodeResult[struct {
		Tools []ToolSpec `json:"tools"`
	}](t, listResp)
	names := make([]string, 0, len(listRes.Tools))
	for _, spec := range listRes.Tools {
		names = append(names, spec.Name)
	}
	require.ElementsMatch(t, []string{"echo", GetTaskToolName}, names)
	require.NotContains(t, names, "_approval_resolve")

	// Calling _approval_resolve on the MCP data plane must be rejected as unknown tool.
	resolveAttempt := client.call("tools/call", map[string]any{
		"name":      "_approval_resolve",
		"arguments": map[string]any{"approval_id": "apr_1", "secret": testStrongSecret, "decision": DecisionAllow},
	})
	require.NotNil(t, resolveAttempt.Error)
	require.Equal(t, CodeInvalidParams, resolveAttempt.Error.Code)

	callResp := client.call("tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hello"},
	})
	callRes := decodeResult[CallToolResult](t, callResp)
	require.False(t, callRes.IsError)
	require.Equal(t, "echo:hello", callRes.Content[0].Text)

	auditRec.mu.Lock()
	require.Len(t, auditRec.sources, 1)
	require.Equal(t, "mcp", auditRec.sources[0])
	auditRec.mu.Unlock()
}

func assertNoApprovalLeakOnDataPlane(t *testing.T, res CallToolResult, forbiddenSubstrings ...string) {
	t.Helper()
	var contentText strings.Builder
	for _, item := range res.Content {
		contentText.WriteString(item.Text)
	}
	structuredBytes, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)

	baseForbidden := []string{
		`"items"`,
		`"subjects"`,
		`"allowed_decisions"`,
		`"review"`,
		`"mfa"`,
		`"pause":`,
	}
	for _, needle := range append(baseForbidden, forbiddenSubstrings...) {
		require.NotContains(t, contentText.String(), needle, "data-plane content leaked %q", needle)
		require.NotContains(t, string(structuredBytes), needle, "data-plane structuredContent leaked %q", needle)
	}
}

func TestExternalApprovalControlSocketApproveDenyAndAudit(t *testing.T) {
	var auditEvents []ResolveAuditEvent
	var auditMu sync.Mutex
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		TTL:                5 * time.Second,
		AuthFailureBackoff: -1,
		SubjectsResolver:   func(_ context.Context, item permission.ApprovalItem) []string { return []string{item.Command} },
		OnResolveAudit: func(_ context.Context, ev ResolveAuditEvent) {
			auditMu.Lock()
			auditEvents = append(auditEvents, ev)
			auditMu.Unlock()
		},
	})
	require.NoError(t, err)

	sockPath, stopCtrl := startTestControlServer(t, approver)
	defer stopCtrl()

	var seenDecision string
	execTool := &tool.RawTool{
		NameStr:   "exec",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
			item := permission.ApprovalItem{
				Type:      "exec",
				AssetID:   1,
				AssetName: "web-01",
				Command:   in["command"].(string),
			}
			resp := approver.ConfirmFunc(ctx, permission.ApprovalKindSingle, []permission.ApprovalItem{item})
			parsed, parseErr := permission.ParseApprovalResponse(permission.ApprovalKindSingle, resp, []permission.ApprovalItem{item})
			require.NoError(t, parseErr)
			seenDecision = resp.Decision
			if parsed.Decision == permission.ApprovalDeny {
				return &agent.ToolResultBlock{
					Content: []agent.ContentBlock{agent.TextBlock{Text: "USER DENIED"}},
					IsError: true,
				}, nil
			}
			return &agent.ToolResultBlock{
				Content: []agent.ContentBlock{agent.TextBlock{Text: "ran:" + in["command"].(string) + ":" + resp.Decision}},
			}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    []agent.Tool{execTool},
		Approver: approver,
	})
	defer cleanup()

	// 1. Trigger pause -> data plane returns opaque PauseRef only
	rawPauseRes := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"command": "systemctl restart nginx"},
	}))
	assertNoApprovalLeakOnDataPlane(t, rawPauseRes, "systemctl restart nginx", "web-01")
	pauseRef := decodeStructured[PauseRef](t, rawPauseRes)
	require.Equal(t, StatusNeedsApproval, pauseRef.Status)
	require.NotEmpty(t, pauseRef.ApprovalID)
	require.NotEmpty(t, pauseRef.TaskID)
	require.NotEmpty(t, pauseRef.ExpiresAt)

	// 2. GetPending & ListPending over control socket return full PausePayload
	pause, err := GetPendingViaControlSocket(context.Background(), sockPath, testStrongSecret, pauseRef.ApprovalID)
	require.NoError(t, err)
	require.Equal(t, StatusNeedsApproval, pause.Status)
	require.Equal(t, permission.ApprovalKindSingle, pause.Kind)
	require.Contains(t, pause.AllowedDecisions, DecisionAllow)
	require.Contains(t, pause.AllowedDecisions, DecisionAllowSession)
	require.NotContains(t, pause.AllowedDecisions, DecisionAllowPermanent) // disabled by default

	pending, err := ListPendingViaControlSocket(context.Background(), sockPath, testStrongSecret)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, pauseRef.ApprovalID, pending[0].ApprovalID)

	// 3. Wrong secret fails closed, keeps approval pending, and records audit event
	_, err = ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pauseRef.ApprovalID,
		Secret:     "wrong-secret-0123456789",
		Decision:   DecisionAllow,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unauthorized")

	// 4. Valid secret with allow_session maps to allowAll
	taskRes, err := ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pauseRef.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllowSession,
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, taskRes.Status)
	require.Equal(t, "ran:systemctl restart nginx:allowAll", taskRes.Output)
	require.Equal(t, "allowAll", seenDecision)

	// 5. Second resolve fails cleanly
	_, err = ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pauseRef.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllow,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	// 6. Deny flow
	denyPause := decodeStructured[PauseRef](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"command": "rm -rf /tmp/foo"},
	})))
	denyTask, err := ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: denyPause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionDeny,
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, denyTask.Status)
	require.True(t, denyTask.IsError)
	require.Equal(t, "USER DENIED", denyTask.Output)

	auditMu.Lock()
	require.GreaterOrEqual(t, len(auditEvents), 3)
	require.False(t, auditEvents[0].Success)
	require.Contains(t, auditEvents[0].Error, "unauthorized")
	auditMu.Unlock()
}

func TestExternalApprovalEditedItemsScopeLockedAndPermanentWithEditSucceeds(t *testing.T) {
	var persistedPatterns []string
	var failFirst atomic.Bool
	failFirst.Store(true)

	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		TTL:                5 * time.Second,
		AllowPermanent:     true,
		AuthFailureBackoff: -1,
		SubjectsResolver:   func(_ context.Context, item permission.ApprovalItem) []string { return []string{item.Command} },
		PermanentRuleWriter: func(_ context.Context, _ []permission.ApprovalItem, edited []permission.ApprovalItem) error {
			if failFirst.Swap(false) {
				return errors.New("allow rule is shadowed by deny")
			}
			for _, it := range edited {
				persistedPatterns = append(persistedPatterns, it.Command)
			}
			return nil
		},
	})
	require.NoError(t, err)

	sockPath, stopCtrl := startTestControlServer(t, approver)
	defer stopCtrl()

	execTool := &tool.RawTool{
		NameStr:   "exec",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			item := permission.ApprovalItem{Type: "exec", AssetID: 42, AssetName: "web-01", Command: "systemctl status nginx"}
			resp := approver.ConfirmFunc(ctx, permission.ApprovalKindSingle, []permission.ApprovalItem{item})
			// Downstream HandleConfirm always passes resp through ParseApprovalResponse.
			parsed, parseErr := permission.ParseApprovalResponse(permission.ApprovalKindSingle, resp, []permission.ApprovalItem{item})
			if parseErr != nil || parsed.Decision == permission.ApprovalDeny {
				return &agent.ToolResultBlock{
					Content: []agent.ContentBlock{agent.TextBlock{Text: "DENIED:" + parseErr.Error()}},
					IsError: true,
				}, nil
			}
			return &agent.ToolResultBlock{
				Content: []agent.ContentBlock{agent.TextBlock{Text: "ok:" + resp.Decision}},
			}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    []agent.Tool{execTool},
		Approver: approver,
	})
	defer cleanup()

	pauseRef := decodeStructured[PauseRef](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{},
	})))
	pause, err := GetPendingViaControlSocket(context.Background(), sockPath, testStrongSecret, pauseRef.ApprovalID)
	require.NoError(t, err)
	require.Contains(t, pause.AllowedDecisions, DecisionAllowPermanent)

	// Forged edited_items attempting to change AssetID=42 -> AssetID=0 (global) must be rejected.
	_, err = ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllowPermanent,
		EditedItems: []permission.ApprovalItem{
			{Type: "exec", AssetID: 0, Command: "*"},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "changes the requested scope")
	require.Empty(t, persistedPatterns)

	// First valid permanent rule write fails (shadowed by deny) -> stays pending
	_, err = ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllowPermanent,
		EditedItems: []permission.ApprovalItem{
			{Type: "exec", AssetID: 42, Command: "systemctl status *"},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "shadowed by deny")
	require.Empty(t, persistedPatterns)

	// Second valid permanent rule write succeeds AND downstream ParseApprovalResponse(single, allow) passes!
	taskRes, err := ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllowPermanent,
		EditedItems: []permission.ApprovalItem{
			{Type: "exec", AssetID: 42, Command: "systemctl status *"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, taskRes.Status)
	require.False(t, taskRes.IsError)
	require.Equal(t, "ok:allow", taskRes.Output)
	require.Equal(t, []string{"systemctl status *"}, persistedPatterns)
}

func TestExternalGrantRejectsScopeTamperingAndPersistsValidEdit(t *testing.T) {
	var savedPatterns []string
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		TTL:                5 * time.Second,
		AuthFailureBackoff: -1,
		GrantPersister: func(_ context.Context, _ []permission.ApprovalItem, edited []permission.ApprovalItem) ([]string, error) {
			for _, it := range edited {
				savedPatterns = append(savedPatterns, it.Command)
			}
			return savedPatterns, nil
		},
	})
	require.NoError(t, err)
	sockPath, stopCtrl := startTestControlServer(t, approver)
	defer stopCtrl()

	grantTool := &tool.RawTool{
		NameStr:   "request_permission",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			ok, patterns := approver.GrantRequestFunc(ctx, []permission.ApprovalItem{
				{Type: "grant", AssetID: 3, AssetName: "web-01", Command: "systemctl * nginx"},
			}, "restart nginx")
			if !ok {
				return &agent.ToolResultBlock{IsError: true, Content: []agent.ContentBlock{agent.TextBlock{Text: "denied"}}}, nil
			}
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "granted:" + patterns[0]}}}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    []agent.Tool{grantTool},
		Approver: approver,
	})
	defer cleanup()

	pauseRef := decodeStructured[PauseRef](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "request_permission",
		"arguments": map[string]any{},
	})))
	pause, err := GetPendingViaControlSocket(context.Background(), sockPath, testStrongSecret, pauseRef.ApprovalID)
	require.NoError(t, err)
	require.Equal(t, permission.ApprovalKindGrant, pause.Kind)

	// Escalation attempt: asset_id=0 (all assets) and type="exec" must be rejected!
	_, err = ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllow,
		EditedItems: []permission.ApprovalItem{
			{Type: "exec", AssetID: 0, Command: "*"},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "changes the requested scope")
	require.Empty(t, savedPatterns)

	// Valid narrowed edit on the same asset/type succeeds
	res, err := ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllow,
		EditedItems: []permission.ApprovalItem{
			{Type: "grant", AssetID: 3, AssetName: "web-01", Command: "systemctl reload nginx"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, res.Status)
	require.Equal(t, "granted:systemctl reload nginx", res.Output)
	require.Equal(t, []string{"systemctl reload nginx"}, savedPatterns)
}

func TestExternalApprovalGetTaskPauseStateMachineAndSecondaryMFA(t *testing.T) {
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		TTL:                5 * time.Second,
		AuthFailureBackoff: -1,
		SubjectsResolver:   func(_ context.Context, item permission.ApprovalItem) []string { return []string{item.Command} },
		AssetNameLookup:    func(_ context.Context, _ int64) string { return "prod-bastion" },
	})
	require.NoError(t, err)
	sockPath, stopCtrl := startTestControlServer(t, approver)
	defer stopCtrl()

	releaseSlow := make(chan struct{})
	execTool := &tool.RawTool{
		NameStr:   "exec",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			resp := approver.ConfirmFunc(ctx, permission.ApprovalKindSingle, []permission.ApprovalItem{
				{Type: "exec", AssetID: 7, AssetName: "prod-bastion", Command: "deploy.sh"},
			})
			if resp.Decision == "deny" {
				return &agent.ToolResultBlock{IsError: true, Content: []agent.ContentBlock{agent.TextBlock{Text: "denied"}}}, nil
			}
			answers, err := approver.NewMFACaller(7).SubmitChallenge(ctx, sshagent.MFAChallenge{
				Name:        "2FA",
				Instruction: "Enter TOTP",
				Prompts:     []string{"Verification code: "},
				Echo:        []bool{false},
			})
			if err != nil {
				return nil, err
			}
			<-releaseSlow
			return &agent.ToolResultBlock{
				Content: []agent.ContentBlock{agent.TextBlock{Text: "deployed-with-mfa:" + answers[0]}},
			}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    []agent.Tool{execTool},
		Approver: approver,
	})
	defer cleanup()

	rawPause1 := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{},
	}))
	assertNoApprovalLeakOnDataPlane(t, rawPause1, "deploy.sh", "prod-bastion")
	pause1 := decodeStructured[PauseRef](t, rawPause1)
	require.Equal(t, StatusNeedsApproval, pause1.Status)

	// Calling get_task while still paused must return PauseRef ONLY (no full PausePayload)
	// WITHOUT corrupting subsequent transitions.
	rawPolledPause := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      GetTaskToolName,
		"arguments": map[string]any{"task_id": pause1.TaskID, "wait_ms": 100},
	}))
	assertNoApprovalLeakOnDataPlane(t, rawPolledPause, "deploy.sh", "prod-bastion")
	polledPause := decodeStructured[TaskResultPayload](t, rawPolledPause)
	require.Equal(t, StatusNeedsApproval, polledPause.Status)
	require.Nil(t, polledPause.Pause, "get_task on data plane must never populate full Pause")
	require.NotNil(t, polledPause.PauseRef)
	require.Equal(t, pause1.ApprovalID, polledPause.PauseRef.ApprovalID)

	// Approve command -> control socket returns full MFA PausePayload directly
	step2, err := ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pause1.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllow,
		WaitMs:     2000,
	})
	require.NoError(t, err)
	require.Equal(t, StatusNeedsMFA, step2.Status)
	require.NotNil(t, step2.Pause)
	require.Equal(t, "mfa", step2.Pause.Kind)
	require.Equal(t, "prod-bastion", step2.Pause.MFA.AssetName)

	// Calling get_task while paused on MFA must also redact MFA prompts to PauseRef
	rawPolledMFA := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      GetTaskToolName,
		"arguments": map[string]any{"task_id": pause1.TaskID, "wait_ms": 100},
	}))
	assertNoApprovalLeakOnDataPlane(t, rawPolledMFA, "Enter TOTP", "Verification code", "prod-bastion")
	polledMFA := decodeStructured[TaskResultPayload](t, rawPolledMFA)
	require.Equal(t, StatusNeedsMFA, polledMFA.Status)
	require.Nil(t, polledMFA.Pause)
	require.NotNil(t, polledMFA.PauseRef)
	require.Equal(t, step2.Pause.ApprovalID, polledMFA.PauseRef.ApprovalID)

	// Submit MFA code with short wait_ms -> returns StatusRunning
	step3, err := ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: step2.Pause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllow,
		MFAAnswers: []string{"654321"},
		WaitMs:     40,
	})
	require.NoError(t, err)
	require.Equal(t, StatusRunning, step3.Status)

	// Release slow execution and fetch via get_task
	close(releaseSlow)
	step4 := decodeStructured[TaskResultPayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      GetTaskToolName,
		"arguments": map[string]any{"task_id": step3.TaskID, "wait_ms": 2000},
	})))
	require.Equal(t, StatusCompleted, step4.Status)
	require.False(t, step4.IsError)
	require.Equal(t, "deployed-with-mfa:654321", step4.Output)
}

func TestValidateResolveDecisionHandlesNilMFAWithoutPanic(t *testing.T) {
	p := &pendingPause{
		payload: PausePayload{
			Kind: "mfa",
			MFA:  nil,
		},
	}
	_, err := validateResolveDecision(p, ResolveRequest{Decision: DecisionAllow})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid mfa challenge state")
}

func TestExternalApprovalLocalWriteAndMaxPendingFailClosed(t *testing.T) {
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		TTL:                5 * time.Second,
		MaxPending:         1,
		AuthFailureBackoff: -1,
	})
	require.NoError(t, err)
	sockPath, stopCtrl := startTestControlServer(t, approver)
	defer stopCtrl()

	localWriteTool := &tool.RawTool{
		NameStr:   "write_test",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
			path := in["path"].(string)
			resp := approver.ConfirmLocalWrite(ctx, LocalWritePrompt{
				ToolName:        "local_write",
				Command:         path,
				Detail:          "cp s3:/b/k → " + path,
				Paths:           []string{path},
				DefaultPatterns: []string{path},
			})
			if resp.Decision == "deny" {
				return &agent.ToolResultBlock{IsError: true, Content: []agent.ContentBlock{agent.TextBlock{Text: "local write denied"}}}, nil
			}
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "wrote:" + path}}}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    []agent.Tool{localWriteTool},
		Approver: approver,
	})
	defer cleanup()

	// 1. First call parks and occupies the single MaxPending slot
	pauseRef := decodeStructured[PauseRef](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "write_test",
		"arguments": map[string]any{"path": "/tmp/out.txt"},
	})))
	pause, err := GetPendingViaControlSocket(context.Background(), sockPath, testStrongSecret, pauseRef.ApprovalID)
	require.NoError(t, err)
	require.Equal(t, permission.ApprovalKindLocalTool, pause.Kind)
	require.Len(t, pause.Items, 1)
	require.Equal(t, []string{"/tmp/out.txt"}, pause.Items[0].LocalPaths)

	// 2. Second concurrent pause exceeds MaxPending=1 -> fails closed immediately
	secondRes := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "write_test",
		"arguments": map[string]any{"path": "/tmp/second.txt"},
	}))
	require.True(t, secondRes.IsError)
	require.Contains(t, secondRes.Content[0].Text, "local write denied")

	// 3. Approve first local write
	doneRes, err := ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllow,
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, doneRes.Status)
	require.Equal(t, "wrote:/tmp/out.txt", doneRes.Output)
}

func TestServerRequestSizeLimitAndCancellationNotification(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	slowTool := &tool.RawTool{
		NameStr:   "slow",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			started <- struct{}{}
			<-ctx.Done()
			canceled <- struct{}{}
			return nil, ctx.Err()
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:           []agent.Tool{slowTool},
		MaxRequestBytes: 256,
	})
	defer cleanup()

	// 1. Oversized frame (> 256 bytes) is rejected without killing the session
	oversized := `{"jsonrpc":"2.0","id":99,"method":"ping","params":{"pad":"` + strings.Repeat("a", 300) + `"}}` + "\n"
	_, err := client.writer.Write([]byte(oversized))
	require.NoError(t, err)
	line, err := client.reader.ReadBytes('\n')
	require.NoError(t, err)
	var errResp Response
	require.NoError(t, json.Unmarshal(line, &errResp))
	require.NotNil(t, errResp.Error)
	require.Equal(t, CodeInvalidRequest, errResp.Error.Code)

	// 2. Subsequent normal ping still works
	require.Nil(t, client.call("ping", nil).Error)

	// 3. notifications/cancelled cancels an in-flight tool call
	reqBytes, err := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("77"),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"slow","arguments":{}}`),
	})
	require.NoError(t, err)
	_, err = client.writer.Write(append(reqBytes, '\n'))
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow tool did not start")
	}

	client.sendNotification("notifications/cancelled", map[string]any{"requestId": 77})
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("notifications/cancelled did not cancel in-flight request")
	}

	cancelLine, err := client.reader.ReadBytes('\n')
	require.NoError(t, err)
	var cancelResp Response
	require.NoError(t, json.Unmarshal(cancelLine, &cancelResp))
	require.Equal(t, "77", string(cancelResp.ID))
}

func TestServerSerializesSerialTools(t *testing.T) {
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	gate := make(chan struct{})
	firstEntered := make(chan struct{}, 1)

	serialOp := &tool.RawTool{
		NameStr:   "serial_op",
		SchemaVal: agent.Schema{Type: "object"},
		IsSerial:  true,
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			cur := inFlight.Add(1)
			for {
				prev := maxInFlight.Load()
				if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			select {
			case firstEntered <- struct{}{}:
				<-gate
			default:
			}
			inFlight.Add(-1)
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: "done"}}}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools: []agent.Tool{serialOp},
	})
	defer cleanup()

	writeCall := func(id string) {
		b, err := json.Marshal(Request{
			JSONRPC: "2.0",
			ID:      json.RawMessage(id),
			Method:  "tools/call",
			Params:  json.RawMessage(`{"name":"serial_op","arguments":{}}`),
		})
		require.NoError(t, err)
		_, err = client.writer.Write(append(b, '\n'))
		require.NoError(t, err)
	}

	writeCall("1")
	<-firstEntered
	writeCall("2")
	time.Sleep(40 * time.Millisecond)
	close(gate)

	for range 2 {
		_, err := client.reader.ReadBytes('\n')
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), maxInFlight.Load(), "IsSerial=true tools must never overlap")
}

func TestExternalApprovalExpiresFailClosed(t *testing.T) {
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		TTL:                80 * time.Millisecond,
		AuthFailureBackoff: -1,
		SubjectsResolver:   func(_ context.Context, item permission.ApprovalItem) []string { return []string{item.Command} },
	})
	require.NoError(t, err)
	sockPath, stopCtrl := startTestControlServer(t, approver)
	defer stopCtrl()

	var decided atomic.Value
	execTool := &tool.RawTool{
		NameStr:   "exec",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			resp := approver.ConfirmFunc(ctx, permission.ApprovalKindSingle, []permission.ApprovalItem{
				{Type: "exec", AssetID: 1, AssetName: "web-01", Command: "reboot"},
			})
			decided.Store(resp.Decision)
			return &agent.ToolResultBlock{
				Content: []agent.ContentBlock{agent.TextBlock{Text: resp.Decision}},
				IsError: resp.Decision == "deny",
			}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    []agent.Tool{execTool},
		Approver: approver,
	})
	defer cleanup()

	pause := decodeStructured[PauseRef](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{},
	})))
	require.Equal(t, StatusNeedsApproval, pause.Status)

	require.Eventually(t, func() bool {
		return decided.Load() == "deny"
	}, time.Second, 20*time.Millisecond)

	_, err = GetPendingViaControlSocket(context.Background(), sockPath, testStrongSecret, pause.ApprovalID)
	require.Error(t, err)

	_, err = ResolveViaControlSocket(context.Background(), sockPath, ResolveRequest{
		ApprovalID: pause.ApprovalID,
		Secret:     testStrongSecret,
		Decision:   DecisionAllow,
	})
	require.Error(t, err)
}

func TestControlPlaneGetPendingAuthBackoffGroupFieldsAndAddr(t *testing.T) {
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		TTL:                5 * time.Second,
		AuthFailureBackoff: 30 * time.Millisecond,
		SubjectsResolver:   func(_ context.Context, item permission.ApprovalItem) []string { return []string{"group-subject"} },
	})
	require.NoError(t, err)

	ctrl := NewControlServer(approver)
	require.Empty(t, ctrl.Addr(), "Addr must be empty before Start")
	sockPath := filepath.Join(t.TempDir(), "ctrl-addr.sock")
	require.NoError(t, ctrl.Start(sockPath))
	defer ctrl.Stop()

	addr := ctrl.Addr()
	require.NotEmpty(t, addr)
	if runtime.GOOS == "windows" {
		require.True(t, strings.HasPrefix(addr, `\\.\pipe\opskat-`), "expected Windows named pipe address, got %q", addr)
	} else {
		require.Equal(t, sockPath, addr)
	}

	deleteGroupTool := &tool.RawTool{
		NameStr:   "delete_group",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			resp := approver.ConfirmFunc(ctx, permission.ApprovalKindOnce, []permission.ApprovalItem{{
				Type:      "delete",
				GroupID:   9,
				GroupName: "prod-cluster",
				Command:   "delete group #9 (prod-cluster)",
				Review: &aictx.ReviewInfo{
					Mode:   "assisted",
					Reason: "internal-policy-rule-42",
					Failed: []string{"prod-deletion-check"},
				},
			}})
			return &agent.ToolResultBlock{
				Content: []agent.ContentBlock{agent.TextBlock{Text: resp.Decision}},
				IsError: resp.Decision == "deny",
			}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    []agent.Tool{deleteGroupTool},
		Approver: approver,
	})
	defer cleanup()

	rawRes := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "delete_group",
		"arguments": map[string]any{},
	}))
	assertNoApprovalLeakOnDataPlane(t, rawRes, "prod-cluster", "internal-policy-rule-42", "group-subject")
	ref := decodeStructured[PauseRef](t, rawRes)
	require.Equal(t, StatusNeedsApproval, ref.Status)
	require.NotEmpty(t, ref.ApprovalID)
	require.NotEmpty(t, ref.TaskID)
	require.NotEmpty(t, ref.ExpiresAt)

	// Wrong secret is rejected with backoff
	start := time.Now()
	_, err = GetPendingViaControlSocket(context.Background(), sockPath, "wrong-secret-0123456789", ref.ApprovalID)
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unauthorized")
	require.GreaterOrEqual(t, elapsed, 20*time.Millisecond, "wrong secret must incur auth failure backoff")

	// Unknown approval_id returns error
	_, err = GetPendingViaControlSocket(context.Background(), sockPath, testStrongSecret, "apr_nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	// Valid secret returns full PausePayload including GroupID, GroupName, Review, Subjects
	full, err := GetPendingViaControlSocket(context.Background(), sockPath, testStrongSecret, ref.ApprovalID)
	require.NoError(t, err)
	require.Equal(t, ref.ApprovalID, full.ApprovalID)
	require.Equal(t, ref.TaskID, full.TaskID)
	require.Len(t, full.Items, 1)
	require.Equal(t, int64(9), full.Items[0].GroupID)
	require.Equal(t, "prod-cluster", full.Items[0].GroupName)
	require.Equal(t, []string{"group-subject"}, full.Items[0].Subjects)
	require.NotNil(t, full.Items[0].Review)
	require.Equal(t, "internal-policy-rule-42", full.Items[0].Review.Reason)
}

func TestToolsListPopulatesMCP2025Annotations(t *testing.T) {
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:             testStrongSecret,
		AuthFailureBackoff: -1,
	})
	require.NoError(t, err)

	makeTool := func(name string) agent.Tool {
		return &tool.RawTool{
			NameStr:   name,
			SchemaVal: agent.Schema{Type: "object"},
			Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
				return &agent.ToolResultBlock{}, nil
			},
		}
	}

	allNames := []string{
		"list_assets", "get_asset", "list_groups", "get_group",
		"list_credentials", "get_credential", "help",
		"exec", "batch_exec", "cp",
		"put_asset", "put_group", "delete_asset", "delete_group",
		"request_permission", "get_asset_secret",
	}
	tools := make([]agent.Tool, 0, len(allNames))
	for _, name := range allNames {
		tools = append(tools, makeTool(name))
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    tools,
		Approver: approver,
		ExtraTools: []ExtraTool{{
			Spec: ToolSpec{Name: "list_audit", InputSchema: agent.Schema{Type: "object"}},
			Handler: func(_ context.Context, _ map[string]any) (*CallToolResult, error) {
				return textResult("[]", false), nil
			},
		}},
	})
	defer cleanup()

	listRes := decodeResult[struct {
		Tools []ToolSpec `json:"tools"`
	}](t, client.call("tools/list", nil))
	byName := make(map[string]ToolSpec, len(listRes.Tools))
	for _, spec := range listRes.Tools {
		byName[spec.Name] = spec
	}

	assertHints := func(name string, wantReadOnly, wantDestructive bool) {
		spec, ok := byName[name]
		require.True(t, ok, "missing tool %q", name)
		require.NotNil(t, spec.Annotations, "missing annotations on tool %q", name)
		require.NotNil(t, spec.Annotations.ReadOnlyHint, "missing readOnlyHint on tool %q", name)
		require.Equal(t, wantReadOnly, *spec.Annotations.ReadOnlyHint, "readOnlyHint mismatch on %q", name)
		require.NotNil(t, spec.Annotations.DestructiveHint, "missing destructiveHint on tool %q", name)
		require.Equal(t, wantDestructive, *spec.Annotations.DestructiveHint, "destructiveHint mismatch on %q", name)
	}

	for _, name := range []string{"list_assets", "get_asset", "list_groups", "get_group", "list_credentials", "get_credential", "help", "list_audit", GetTaskToolName} {
		assertHints(name, true, false)
	}
	for _, name := range []string{"exec", "batch_exec", "cp", "delete_asset", "delete_group"} {
		assertHints(name, false, true)
	}
	for _, name := range []string{"put_asset", "put_group", "request_permission", "get_asset_secret"} {
		assertHints(name, false, false)
	}
}

func TestServerAuditDoesNotMisattributeGroupScopedToolsAndAuditsExtraTools(t *testing.T) {
	groupTool := &tool.RawTool{
		NameStr:   "delete_group",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: `{"id":3,"message":"group deleted"}`}}}, nil
		},
	}
	extra := ExtraTool{
		Spec:  ToolSpec{Name: "list_audit", InputSchema: agent.Schema{Type: "object"}},
		Audit: true,
		Handler: func(_ context.Context, _ map[string]any) (*CallToolResult, error) {
			return textResult("[]", false), nil
		},
	}

	rec := &recordingAuditWriter{}
	client, cleanup := startTestServer(t, ServerConfig{
		Tools:       []agent.Tool{groupTool},
		ExtraTools:  []ExtraTool{extra},
		AuditWriter: rec,
	})
	defer cleanup()

	_ = decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "delete_group",
		"arguments": map[string]any{"id": 3},
	}))
	_ = decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "list_audit",
		"arguments": map[string]any{},
	}))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.entries, 2)
	require.Equal(t, int64(0), rec.entries[0].AssetID, "group-scoped id=3 must never be attributed to asset #3")
	require.Equal(t, "list_audit", rec.entries[1].ToolName)
	require.Equal(t, "mcp", rec.sources[0])
	require.Equal(t, "mcp", rec.sources[1])
}
