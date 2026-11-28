package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/runner"
	aitool "github.com/opskat/opskat/internal/ai/tool"
)

// AuditSource is the audit_logs.source value of every tool call made through opsctl mcp.
const AuditSource = "mcp"

// coreTools is the high-frequency subset advertised by ToolsetCore.
var coreTools = map[string]bool{
	"list_assets":        true,
	"get_asset":          true,
	"help":               true,
	"exec":               true,
	"batch_exec":         true,
	"cp":                 true,
	"request_permission": true,
}

// serialTool is implemented by tools that require serialized execution (e.g. *tool.RawTool).
type serialTool interface {
	Serial() bool
}

// IsSerial reports whether t must run exclusively. A tool wrapping another must answer
// with its inner tool's IsSerial.
func IsSerial(t agent.Tool) bool {
	st, ok := t.(serialTool)
	return ok && st.Serial()
}

// behaviorTool lets a tool defined outside the built-in registry (e.g. list_audit)
// declare its side effects; built-in tools are looked up through aitool.BehaviorOf.
type behaviorTool interface {
	Behavior() aitool.Behavior
}

// ServerConfig configures an MCP server.
type ServerConfig struct {
	ServerVersion string
	Tools         []agent.Tool
	AuditWriter   audit.AuditWriter
	SessionID     string
	PrepareCtx    func(ctx context.Context) context.Context
	// Instructions is sent to the client at initialization, for it to put in front of
	// the model: how to use these tools (runner.ToolGuidance).
	Instructions string
	// AnswerTimeout bounds how long a call waits for the human to answer a question
	// asked through the client (Ask); zero means DefaultAnswerTimeout.
	AnswerTimeout time.Duration
}

// Server exposes OpsKat tools over MCP. The protocol (handshake, tools/list,
// tools/call, cancellation, framing, multi round-trip requests) is handled by the
// official go-sdk; this type only adapts OpsKat's agent tools onto it. Each call runs
// through the same cago ToolDispatcher + runner.AuditMiddleware chain as the built-in
// AI, so both entry points share one audit implementation.
type Server struct {
	sdk           *mcpsdk.Server
	dispatcher    *agent.ToolDispatcher
	sessionID     string
	prepareCtx    func(ctx context.Context) context.Context
	serialSem     chan struct{}
	answerTimeout time.Duration

	// serveCtx parents calls that stay paused between rounds of a multi round-trip
	// request; set once in Run before any request is served.
	serveCtx context.Context
	mu       sync.Mutex
	paused   map[string]*pausedCall
}

// NewServer constructs an MCP server from the given configuration.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		sdk: mcpsdk.NewServer(&mcpsdk.Implementation{Name: "opsctl", Version: cfg.ServerVersion}, &mcpsdk.ServerOptions{
			// Only tools: without an explicit value the SDK also advertises logging.
			Capabilities: &mcpsdk.ServerCapabilities{Tools: &mcpsdk.ToolCapabilities{}},
			Instructions: cfg.Instructions,
		}),
		dispatcher: &agent.ToolDispatcher{
			Tools: cfg.Tools,
			Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{
				{Matcher: ".*", Fn: runner.AuditMiddleware(cfg.AuditWriter)},
			},
		},
		sessionID:     cfg.SessionID,
		prepareCtx:    cfg.PrepareCtx,
		serialSem:     make(chan struct{}, 1),
		answerTimeout: cfg.AnswerTimeout,
		paused:        map[string]*pausedCall{},
	}
	if s.answerTimeout <= 0 {
		s.answerTimeout = DefaultAnswerTimeout
	}
	for _, t := range cfg.Tools {
		s.sdk.AddTool(&mcpsdk.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
			Annotations: annotationsFor(t),
		}, s.toolHandler(t))
	}
	return s
}

// Run serves MCP over t until the client disconnects or ctx is canceled.
func (s *Server) Run(ctx context.Context, t mcpsdk.Transport) error {
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.serveCtx = serveCtx

	logger.Ctx(ctx).Info("opsctl mcp server started")
	err := s.sdk.Run(serveCtx, t)
	switch {
	case err == nil, errors.Is(err, io.EOF):
		logger.Ctx(ctx).Info("opsctl mcp server stopped: client disconnected")
		return nil
	case errors.Is(err, context.Canceled):
		logger.Ctx(ctx).Info("opsctl mcp server stopped: context canceled")
		return err
	default:
		logger.Ctx(ctx).Error("opsctl mcp server stopped", zap.Error(err))
		return err
	}
}

func annotationsFor(t agent.Tool) *mcpsdk.ToolAnnotations {
	var b aitool.Behavior
	if bt, ok := t.(behaviorTool); ok {
		b = bt.Behavior()
	} else if declared, ok := aitool.BehaviorOf(t.Name()); ok {
		b = declared
	} else {
		return nil // MCP defaults (not read-only, destructive, open world) are the conservative reading
	}
	return &mcpsdk.ToolAnnotations{
		ReadOnlyHint:    b.ReadOnly,
		DestructiveHint: &b.Destructive,
		IdempotentHint:  b.Idempotent,
		OpenWorldHint:   &b.OpenWorld,
	}
}

func (s *Server) toolHandler(t agent.Tool) mcpsdk.ToolHandler {
	name := t.Name()
	isSerial := IsSerial(t)

	return func(reqCtx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var args map[string]any
		if raw := req.Params.Arguments; len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid tool arguments: " + err.Error()}
			}
		}
		if args == nil {
			args = map[string]any{}
		}

		run := func(ctx context.Context) *mcpsdk.CallToolResult {
			if isSerial {
				return textResult(s.dispatchSerial(ctx, name, args))
			}
			return textResult(s.dispatch(ctx, name, args))
		}
		if req.Params.RequestState == "" && !clientCanPrompt(req) {
			return run(reqCtx), nil
		}
		binding, err := callBinding(name, args)
		if err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid tool arguments: " + err.Error()}
		}
		if req.Params.RequestState != "" {
			return s.resume(reqCtx, req.Params, binding), nil
		}
		return s.startPrompting(reqCtx, binding, run), nil
	}
}

// dispatchSerial runs a Serial tool exclusively. A call paused on a question keeps the
// lock: the human is deciding about this very operation.
func (s *Server) dispatchSerial(ctx context.Context, name string, args map[string]any) (string, bool) {
	select {
	case s.serialSem <- struct{}{}:
	case <-ctx.Done():
		return "request canceled", true
	}
	defer func() { <-s.serialSem }()
	return s.dispatch(ctx, name, args)
}

// dispatch runs one tool through the shared middleware chain and flattens its result
// into the text returned to the client.
func (s *Server) dispatch(ctx context.Context, name string, args map[string]any) (out string, isErr bool) {
	// MCP handlers run on SDK goroutines; a tool panic must not take the server down.
	defer func() {
		if r := recover(); r != nil {
			logger.Ctx(ctx).Error("mcp tool panic recovered",
				zap.String("toolName", name), zap.Any("panic", r), zap.Stack("stack"))
			out, isErr = fmt.Sprintf("internal error: panic in tool %s", name), true
		}
	}()

	res := s.dispatcher.Run(s.enrichContext(ctx), agent.DispatchInput{
		ToolName:  name,
		ToolUseID: "mcp_" + uuid.NewString(),
		Input:     args,
	})
	return blockText(res.Output), res.Output.IsError
}

func (s *Server) enrichContext(ctx context.Context) context.Context {
	ctx = aictx.WithAuditSource(ctx, AuditSource)
	if s.sessionID != "" {
		ctx = aictx.WithSessionID(ctx, s.sessionID)
	}
	if s.prepareCtx != nil {
		ctx = s.prepareCtx(ctx)
	}
	return ctx
}

func blockText(block *agent.ToolResultBlock) string {
	var b strings.Builder
	for _, c := range block.Content {
		switch v := c.(type) {
		case agent.TextBlock:
			b.WriteString(v.Text)
		case *agent.TextBlock:
			b.WriteString(v.Text)
		}
	}
	if b.Len() == 0 && block.IsError {
		return "tool call failed"
	}
	return b.String()
}

func textResult(text string, isError bool) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
		IsError: isError,
	}
}

// FilterTools returns the tools matching the requested Toolset in deterministic order.
func FilterTools(all []agent.Tool, toolset Toolset) []agent.Tool {
	if toolset == ToolsetFull {
		return all
	}
	out := make([]agent.Tool, 0, len(coreTools))
	for _, t := range all {
		if coreTools[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}
