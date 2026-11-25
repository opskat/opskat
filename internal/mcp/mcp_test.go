package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/sshagent"
)

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

func (c *mcpTestClient) call(method string, params any) Response {
	c.t.Helper()
	id := c.nextID.Add(1)
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

func TestServerHandshakeAndDirectToolExecution(t *testing.T) {
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

	client, cleanup := startTestServer(t, ServerConfig{
		ServerName:    "opsctl-test",
		ServerVersion: "1.0.0",
		Tools:         []agent.Tool{echoTool},
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
	require.Len(t, listRes.Tools, 1)
	require.Equal(t, "echo", listRes.Tools[0].Name)

	callResp := client.call("tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"msg": "hello"},
	})
	callRes := decodeResult[CallToolResult](t, callResp)
	require.False(t, callRes.IsError)
	require.Equal(t, "echo:hello", callRes.Content[0].Text)
}

func TestExternalApprovalApproveDenyAndSecretGuard(t *testing.T) {
	const secret = "test-secret-xyz"
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:           secret,
		TTL:              5 * time.Second,
		SubjectsResolver: func(_ context.Context, item permission.ApprovalItem) []string { return []string{item.Command} },
	})
	require.NoError(t, err)

	var seenDecision string
	execTool := &tool.RawTool{
		NameStr: "exec",
		SchemaVal: agent.Schema{
			Type: "object",
		},
		Handler: func(ctx context.Context, in map[string]any) (*agent.ToolResultBlock, error) {
			resp := approver.ConfirmFunc(ctx, permission.ApprovalKindSingle, []permission.ApprovalItem{
				{
					Type:      "exec",
					AssetID:   1,
					AssetName: "web-01",
					Command:   in["command"].(string),
				},
			})
			seenDecision = resp.Decision
			if resp.Decision == "deny" {
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

	// 1. Trigger pause
	pauseResp := client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"command": "systemctl restart nginx"},
	})
	pauseRes := decodeResult[CallToolResult](t, pauseResp)
	require.False(t, pauseRes.IsError)
	pause := decodeStructured[PausePayload](t, pauseRes)
	require.Equal(t, StatusNeedsApproval, pause.Status)
	require.Equal(t, permission.ApprovalKindSingle, pause.Kind)
	require.Contains(t, pause.AllowedDecisions, DecisionAllow)
	require.Contains(t, pause.AllowedDecisions, DecisionAllowSession)
	require.NotContains(t, pause.AllowedDecisions, DecisionAllowPermanent) // no writer wired

	// 2. Wrong secret must fail-closed and keep approval intact
	badResp := client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": pause.ApprovalID,
			"secret":      "wrong-secret",
			"decision":    DecisionAllow,
		},
	})
	badRes := decodeResult[CallToolResult](t, badResp)
	require.True(t, badRes.IsError)
	require.Contains(t, badRes.Content[0].Text, "unauthorized")

	// 3. Valid secret with allow_session maps to allowAll
	okResp := client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": pause.ApprovalID,
			"secret":      secret,
			"decision":    DecisionAllowSession,
		},
	})
	okRes := decodeResult[CallToolResult](t, okResp)
	require.False(t, okRes.IsError)
	taskRes := decodeStructured[TaskResultPayload](t, okRes)
	require.Equal(t, StatusCompleted, taskRes.Status)
	require.Equal(t, "ran:systemctl restart nginx:allowAll", taskRes.Output)
	require.Equal(t, "allowAll", seenDecision)

	// 4. Second resolve on same approval_id fails cleanly
	dupResp := client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": pause.ApprovalID,
			"secret":      secret,
			"decision":    DecisionAllow,
		},
	})
	dupRes := decodeResult[CallToolResult](t, dupResp)
	require.True(t, dupRes.IsError)
	require.Contains(t, dupRes.Content[0].Text, "not found")

	// 5. Deny flow
	denyPause := decodeStructured[PausePayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{"command": "rm -rf /tmp/foo"},
	})))
	denyRes := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": denyPause.ApprovalID,
			"secret":      secret,
			"decision":    DecisionDeny,
		},
	}))
	require.True(t, denyRes.IsError)
	denyTask := decodeStructured[TaskResultPayload](t, denyRes)
	require.Equal(t, StatusCompleted, denyTask.Status)
	require.True(t, denyTask.IsError)
	require.Equal(t, "USER DENIED", denyTask.Output)
}

func TestExternalApprovalPermanentRuleFailureKeepsPending(t *testing.T) {
	const secret = "perm-secret"
	var failFirst atomic.Bool
	failFirst.Store(true)
	var persistedCount atomic.Int32

	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:           secret,
		TTL:              5 * time.Second,
		SubjectsResolver: func(_ context.Context, item permission.ApprovalItem) []string { return []string{item.Command} },
		PermanentRuleWriter: func(_ context.Context, items []permission.ApprovalItem, _ []permission.ApprovalItem) error {
			if failFirst.Swap(false) {
				return errors.New("allow rule is shadowed by deny")
			}
			persistedCount.Add(int32(len(items)))
			return nil
		},
	})
	require.NoError(t, err)

	execTool := &tool.RawTool{
		NameStr:   "exec",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(ctx context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			resp := approver.ConfirmFunc(ctx, permission.ApprovalKindSingle, []permission.ApprovalItem{
				{Type: "exec", AssetID: 1, AssetName: "web-01", Command: "uptime"},
			})
			return &agent.ToolResultBlock{
				Content: []agent.ContentBlock{agent.TextBlock{Text: "decision:" + resp.Decision}},
			}, nil
		},
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools:    []agent.Tool{execTool},
		Approver: approver,
	})
	defer cleanup()

	pause := decodeStructured[PausePayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{},
	})))
	require.Contains(t, pause.AllowedDecisions, DecisionAllowPermanent)

	// First attempt fails permanent rule write -> returns error, approval stays pending
	failRes := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": pause.ApprovalID,
			"secret":      secret,
			"decision":    DecisionAllowPermanent,
		},
	}))
	require.True(t, failRes.IsError)
	require.Contains(t, failRes.Content[0].Text, "shadowed by deny")
	require.Equal(t, int32(0), persistedCount.Load())

	// Second attempt succeeds -> persists rule and runs command
	okRes := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": pause.ApprovalID,
			"secret":      secret,
			"decision":    DecisionAllowPermanent,
		},
	}))
	require.False(t, okRes.IsError)
	require.Equal(t, int32(1), persistedCount.Load())
	taskRes := decodeStructured[TaskResultPayload](t, okRes)
	require.Equal(t, StatusCompleted, taskRes.Status)
	require.Equal(t, "decision:allow", taskRes.Output)
}

func TestExternalApprovalBackgroundTaskAndSecondaryMFAPause(t *testing.T) {
	const secret = "mfa-secret"
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:           secret,
		TTL:              5 * time.Second,
		SubjectsResolver: func(_ context.Context, item permission.ApprovalItem) []string { return []string{item.Command} },
		AssetNameLookup: func(_ context.Context, assetID int64) string {
			return "prod-bastion"
		},
	})
	require.NoError(t, err)

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
			// Second pause: SSH MFA challenge
			answers, err := approver.NewMFACaller(7).SubmitChallenge(ctx, sshagent.MFAChallenge{
				Name:        "2FA",
				Instruction: "Enter TOTP",
				Prompts:     []string{"Verification code: "},
				Echo:        []bool{false},
			})
			if err != nil {
				return nil, err
			}
			// Simulate slow execution exceeding resolve wait_ms
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

	// Step 1: initial pause for command approval
	pause1 := decodeStructured[PausePayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{},
	})))
	require.Equal(t, StatusNeedsApproval, pause1.Status)

	// Step 2: approve command -> hits secondary MFA pause
	step2 := decodeStructured[TaskResultPayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": pause1.ApprovalID,
			"secret":      secret,
			"decision":    DecisionAllow,
			"wait_ms":     2000,
		},
	})))
	require.Equal(t, StatusNeedsMFA, step2.Status)
	require.NotNil(t, step2.Pause)
	require.Equal(t, "mfa", step2.Pause.Kind)
	require.Equal(t, "prod-bastion", step2.Pause.MFA.AssetName)
	require.Equal(t, []string{"Verification code: "}, step2.Pause.MFA.Prompts)

	// Step 3: submit MFA code with short wait_ms -> returns StatusRunning
	step3 := decodeStructured[TaskResultPayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": step2.Pause.ApprovalID,
			"secret":      secret,
			"decision":    DecisionAllow,
			"mfa_answers": []string{"654321"},
			"wait_ms":     40,
		},
	})))
	require.Equal(t, StatusRunning, step3.Status)
	require.NotEmpty(t, step3.TaskID)

	// Step 4: release slow execution and fetch via get_task
	close(releaseSlow)
	step4 := decodeStructured[TaskResultPayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name": GetTaskToolName,
		"arguments": map[string]any{
			"task_id": step3.TaskID,
			"wait_ms": 2000,
		},
	})))
	require.Equal(t, StatusCompleted, step4.Status)
	require.False(t, step4.IsError)
	require.Equal(t, "deployed-with-mfa:654321", step4.Output)
}

func TestExternalApprovalExpiresFailClosed(t *testing.T) {
	const secret = "ttl-secret"
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret:           secret,
		TTL:              80 * time.Millisecond,
		SubjectsResolver: func(_ context.Context, item permission.ApprovalItem) []string { return []string{item.Command} },
	})
	require.NoError(t, err)

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

	pause := decodeStructured[PausePayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "exec",
		"arguments": map[string]any{},
	})))
	require.Equal(t, StatusNeedsApproval, pause.Status)

	require.Eventually(t, func() bool {
		return decided.Load() == "deny"
	}, time.Second, 20*time.Millisecond)

	lateRes := decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": pause.ApprovalID,
			"secret":      secret,
			"decision":    DecisionAllow,
		},
	}))
	require.True(t, lateRes.IsError)
}

func TestExternalGrantRequestFlow(t *testing.T) {
	const secret = "grant-secret"
	var savedPatterns []string
	approver, err := NewExternalApprover(ExternalApproverConfig{
		Secret: secret,
		TTL:    5 * time.Second,
		GrantPersister: func(_ context.Context, _ []permission.ApprovalItem, edited []permission.ApprovalItem) ([]string, error) {
			for _, it := range edited {
				savedPatterns = append(savedPatterns, it.Command)
			}
			return savedPatterns, nil
		},
	})
	require.NoError(t, err)

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

	pause := decodeStructured[PausePayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name":      "request_permission",
		"arguments": map[string]any{},
	})))
	require.Equal(t, permission.ApprovalKindGrant, pause.Kind)
	require.Equal(t, "restart nginx", pause.Reason)

	res := decodeStructured[TaskResultPayload](t, decodeResult[CallToolResult](t, client.call("tools/call", map[string]any{
		"name": ResolveToolName,
		"arguments": map[string]any{
			"approval_id": pause.ApprovalID,
			"secret":      secret,
			"decision":    DecisionAllow,
			"edited_items": []map[string]any{
				{"type": "grant", "asset_id": 3, "asset_name": "web-01", "command": "systemctl reload nginx"},
			},
		},
	})))
	require.Equal(t, StatusCompleted, res.Status)
	require.Equal(t, "granted:systemctl reload nginx", res.Output)
	require.Equal(t, []string{"systemctl reload nginx"}, savedPatterns)
}
