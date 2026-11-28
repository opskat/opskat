package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/tool"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	aitool "github.com/opskat/opskat/internal/ai/tool"
)

// mcpTestClient drives the server through the official SDK client over an in-memory
// transport, and exposes results in their JSON wire shape (what a real client sees).
type mcpTestClient struct {
	t       *testing.T
	session *mcpsdk.ClientSession
}

// testResponse is a tools/list or tools/call outcome: Result on success, Error when the
// server answered with a JSON-RPC error.
type testResponse struct {
	Result any
	Error  *jsonrpc.Error
}

// wireToolResult / wireToolSpec decode the JSON wire form of CallToolResult / Tool.
type wireToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

type wireToolSpec struct {
	Name        string `json:"name"`
	Annotations *struct {
		ReadOnlyHint    *bool `json:"readOnlyHint"`
		DestructiveHint *bool `json:"destructiveHint"`
		IdempotentHint  *bool `json:"idempotentHint"`
		OpenWorldHint   *bool `json:"openWorldHint"`
	} `json:"annotations"`
}

// startTestServer connects a client that cannot show prompts (no elicitation handler).
func startTestServer(t *testing.T, cfg ServerConfig) (*mcpTestClient, func()) {
	t.Helper()
	return startTestServerWith(t, cfg, nil)
}

func startTestServerWith(t *testing.T, cfg ServerConfig, clientOpts *mcpsdk.ClientOptions) (*mcpTestClient, func()) {
	t.Helper()
	return startTestServerOn(t, cfg, clientOpts, "")
}

// startTestServerOn connects a client speaking protocolVersion ("" for the latest).
func startTestServerOn(t *testing.T, cfg ServerConfig, clientOpts *mcpsdk.ClientOptions, protocolVersion string) (*mcpTestClient, func()) {
	t.Helper()
	if cfg.AuditWriter == nil {
		cfg.AuditWriter = &recordingAuditWriter{}
	}
	serverT, clientT := mcpsdk.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	srv := NewServer(cfg)
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, serverT)
	}()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "1.0.0"}, clientOpts)
	session, err := client.Connect(ctx, clientT, &mcpsdk.ClientSessionOptions{ProtocolVersion: protocolVersion})
	require.NoError(t, err)

	cleanup := func() {
		_ = session.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("mcp server stopped with %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("mcp server did not shut down cleanly")
		}
	}
	return &mcpTestClient{t: t, session: session}, cleanup
}

func (c *mcpTestClient) call(method string, params any) testResponse {
	c.t.Helper()
	var (
		res any
		err error
	)
	ctx := context.Background()
	switch method {
	case "tools/list":
		res, err = c.session.ListTools(ctx, nil)
	case "tools/call":
		p := params.(map[string]any)
		res, err = c.session.CallTool(ctx, &mcpsdk.CallToolParams{Name: p["name"].(string), Arguments: p["arguments"]})
	default:
		c.t.Fatalf("unsupported test method %q", method)
	}
	if err != nil {
		var rpcErr *jsonrpc.Error
		require.ErrorAs(c.t, err, &rpcErr)
		return testResponse{Error: rpcErr}
	}
	return testResponse{Result: res}
}

func decodeResult[T any](t *testing.T, resp testResponse) T {
	t.Helper()
	require.Nil(t, resp.Error, "unexpected RPC error: %+v", resp.Error)
	raw, err := json.Marshal(resp.Result)
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

func TestServerRejectsMalformedToolArguments(t *testing.T) {
	noop := &tool.RawTool{
		NameStr:   "noop",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(context.Context, map[string]any) (*agent.ToolResultBlock, error) {
			return tool.TextResult("ran"), nil
		},
	}
	client, cleanup := startTestServer(t, ServerConfig{Tools: []agent.Tool{noop}})
	defer cleanup()

	resp := client.call("tools/call", map[string]any{"name": "noop", "arguments": []any{"not", "an", "object"}})
	require.NotNil(t, resp.Error)
	require.EqualValues(t, jsonrpc.CodeInvalidParams, resp.Error.Code)
}

func TestServerCancellationCancelsInFlightToolCall(t *testing.T) {
	for _, tc := range []struct {
		name       string
		clientOpts *mcpsdk.ClientOptions
	}{
		{"client without prompts", nil},
		{"client with prompts", &mcpsdk.ClientOptions{ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			return &mcpsdk.ElicitResult{Action: "cancel"}, nil
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
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

			client, cleanup := startTestServerWith(t, ServerConfig{Tools: []agent.Tool{slowTool}}, tc.clientOpts)
			defer cleanup()

			// Canceling the client call sends the MCP cancellation notification, which must reach the tool's ctx.
			callCtx, cancelCall := context.WithCancel(context.Background())
			callDone := make(chan struct{})
			go func() {
				defer close(callDone)
				_, _ = client.session.CallTool(callCtx, &mcpsdk.CallToolParams{Name: "slow", Arguments: map[string]any{}})
			}()

			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("slow tool did not start")
			}
			cancelCall()
			select {
			case <-canceled:
			case <-time.After(2 * time.Second):
				t.Fatal("client cancellation did not cancel the in-flight tool call")
			}
			<-callDone

			// The session stays usable afterwards.
			require.NoError(t, client.session.Ping(context.Background(), nil))
		})
	}
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

	var wg sync.WaitGroup
	callAsync := func() {
		wg.Go(func() {
			res := decodeResult[wireToolResult](t, client.call("tools/call", map[string]any{"name": "serial_op", "arguments": map[string]any{}}))
			require.Equal(t, "done", res.Content[0].Text)
		})
	}

	callAsync()
	<-firstEntered
	callAsync()
	time.Sleep(40 * time.Millisecond)
	close(gate)
	wg.Wait()
	require.Equal(t, int32(1), maxInFlight.Load(), "IsSerial=true tools must never overlap")
}

type behaviorRawTool struct {
	*tool.RawTool
	behavior aitool.Behavior
}

func (b behaviorRawTool) Behavior() aitool.Behavior { return b.behavior }

func TestToolsListMapsDeclaredBehaviorToAnnotations(t *testing.T) {
	makeTool := func(name string) *tool.RawTool {
		return &tool.RawTool{
			NameStr:   name,
			SchemaVal: agent.Schema{Type: "object"},
			Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
				return &agent.ToolResultBlock{}, nil
			},
		}
	}

	client, cleanup := startTestServer(t, ServerConfig{
		Tools: []agent.Tool{
			makeTool("list_assets"), // built-in, declared read-only
			makeTool("exec"),        // built-in, declared remote + destructive
			behaviorRawTool{RawTool: makeTool("list_audit"), behavior: aitool.Behavior{ReadOnly: true, Idempotent: true}},
			makeTool("undeclared_tool"),
		},
	})
	defer cleanup()

	listRes := decodeResult[struct {
		Tools []wireToolSpec `json:"tools"`
	}](t, client.call("tools/list", nil))
	byName := make(map[string]wireToolSpec, len(listRes.Tools))
	for _, spec := range listRes.Tools {
		byName[spec.Name] = spec
	}

	assertHints := func(name string, readOnly, destructive, idempotent, openWorld bool) {
		t.Helper()
		a := byName[name].Annotations
		require.NotNil(t, a, "missing annotations on tool %q", name)
		require.Equal(t, readOnly, *a.ReadOnlyHint, "readOnlyHint on %q", name)
		require.Equal(t, destructive, *a.DestructiveHint, "destructiveHint on %q", name)
		require.Equal(t, idempotent, *a.IdempotentHint, "idempotentHint on %q", name)
		require.Equal(t, openWorld, *a.OpenWorldHint, "openWorldHint on %q", name)
	}
	assertHints("list_assets", true, false, true, false)
	assertHints("exec", false, true, false, true)
	assertHints("list_audit", true, false, true, false)
	require.Nil(t, byName["undeclared_tool"].Annotations, "an undeclared tool keeps the conservative MCP defaults")
}

func TestServerAuditDoesNotMisattributeGroupScopedToolsAndAuditsEveryTool(t *testing.T) {
	groupTool := &tool.RawTool{
		NameStr:   "delete_group",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			return &agent.ToolResultBlock{Content: []agent.ContentBlock{agent.TextBlock{Text: `{"id":3,"message":"group deleted"}`}}}, nil
		},
	}
	listAudit := &tool.RawTool{
		NameStr:   "list_audit",
		SchemaVal: agent.Schema{Type: "object"},
		Handler: func(_ context.Context, _ map[string]any) (*agent.ToolResultBlock, error) {
			return tool.TextResult("[]"), nil
		},
	}

	rec := &recordingAuditWriter{}
	client, cleanup := startTestServer(t, ServerConfig{
		Tools:       []agent.Tool{groupTool, listAudit},
		AuditWriter: rec,
	})
	defer cleanup()

	_ = decodeResult[wireToolResult](t, client.call("tools/call", map[string]any{
		"name":      "delete_group",
		"arguments": map[string]any{"id": 3},
	}))
	_ = decodeResult[wireToolResult](t, client.call("tools/call", map[string]any{
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

func TestServerAuditsAPromptedCallOnce(t *testing.T) {
	var runs atomic.Int32
	rec := &recordingAuditWriter{}
	client, cleanup := startTestServerWith(t, ServerConfig{Tools: []agent.Tool{askingTool(&runs, "q")}, AuditWriter: rec},
		&mcpsdk.ClientOptions{ElicitationHandler: func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
			return &mcpsdk.ElicitResult{Action: "accept", Content: map[string]any{"decision": "allow"}}, nil
		}})
	defer cleanup()

	_ = callText(t, client, "ask", map[string]any{})
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.entries, 1, "a call paused for a human is still one tool call in the audit log")
}

func TestServerExitsCleanlyOnContextCancelWhileStdinRemainsOpen(t *testing.T) {
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()
	defer func() {
		_ = clientW.Close()
		_ = clientR.Close()
	}()

	srv := NewServer(ServerConfig{AuditWriter: &recordingAuditWriter{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, &mcpsdk.IOTransport{Reader: serverR, Writer: serverW})
	}()

	// Cancel context WITHOUT closing clientW (simulating SIGINT while stdin pipe stays open)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Server.Run hung on ctx cancel while stdin remained open")
	}
}
