package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

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

// GetTaskToolName polls or waits for a backgrounded task after out-of-band approval.
const GetTaskToolName = "get_task"

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

// behaviorTool lets a tool defined outside the built-in registry (e.g. list_audit)
// declare its side effects; built-in tools are looked up through aitool.BehaviorOf.
type behaviorTool interface {
	Behavior() aitool.Behavior
}

// ServerConfig configures an MCP server.
type ServerConfig struct {
	ServerVersion string
	Tools         []agent.Tool
	Approver      *ExternalApprover // non-nil in external approval mode
	AuditWriter   audit.AuditWriter
	SessionID     string
	PrepareCtx    func(ctx context.Context) context.Context
	OnMutate      func(toolName string)
}

// Server exposes OpsKat tools over MCP. The protocol (handshake, tools/list,
// tools/call, cancellation, framing) is handled by the official go-sdk; this type
// only adapts OpsKat's agent tools onto it. Each call runs through the same cago
// ToolDispatcher + runner.AuditMiddleware chain as the built-in AI, so both entry
// points share one audit implementation.
type Server struct {
	sdk        *mcpsdk.Server
	dispatcher *agent.ToolDispatcher
	approver   *ExternalApprover
	sessionID  string
	prepareCtx func(ctx context.Context) context.Context
	onMutate   func(toolName string)
	serialSem  chan struct{}

	// serveCtx parents background tasks that must outlive the tools/call that
	// started them (external approval); set once in Run before any request is served.
	serveCtx context.Context
}

// NewServer constructs an MCP server from the given configuration.
func NewServer(cfg ServerConfig) *Server {
	s := &Server{
		sdk: mcpsdk.NewServer(&mcpsdk.Implementation{Name: "opsctl", Version: cfg.ServerVersion}, &mcpsdk.ServerOptions{
			// Only tools: without an explicit value the SDK also advertises logging.
			Capabilities: &mcpsdk.ServerCapabilities{Tools: &mcpsdk.ToolCapabilities{}},
		}),
		dispatcher: &agent.ToolDispatcher{
			Tools: cfg.Tools,
			Middleware: []agent.ToolHookEntry[agent.ToolMiddleware]{
				{Matcher: ".*", Fn: runner.AuditMiddleware(cfg.AuditWriter)},
			},
		},
		approver:   cfg.Approver,
		sessionID:  cfg.SessionID,
		prepareCtx: cfg.PrepareCtx,
		onMutate:   cfg.OnMutate,
		serialSem:  make(chan struct{}, 1),
	}
	for _, t := range cfg.Tools {
		s.sdk.AddTool(&mcpsdk.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
			Annotations: annotationsFor(t),
		}, s.toolHandler(t))
	}
	if s.approver != nil {
		s.registerExternalTaskTool()
	}
	return s
}

// Run serves MCP over t until the client disconnects or ctx is canceled.
func (s *Server) Run(ctx context.Context, t mcpsdk.Transport) error {
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.serveCtx = serveCtx
	if s.approver != nil {
		defer s.approver.Close()
	}

	logger.Ctx(ctx).Info("opsctl mcp server started", zap.Bool("externalApproval", s.approver != nil))
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
	st, ok := t.(serialTool)
	isSerial := ok && st.Serial()

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

		execFn := func(callCtx context.Context) (string, bool) {
			if !isSerial {
				return s.dispatch(callCtx, name, args)
			}
			return s.dispatchSerial(callCtx, name, args)
		}
		if s.approver != nil {
			// Parent is serveCtx so a parked background task survives after tools/call returns
			// its PauseRef; reqCtx (canceled by the client's cancellation notification) still stops it before it parks.
			return s.approver.Run(s.serveCtx, reqCtx, name, execFn), nil
		}
		out, isErr := execFn(reqCtx)
		return textResult(out, isErr), nil
	}
}

// dispatchSerial runs a Serial tool exclusively. In external mode the lock is released
// while the call is parked waiting for a human, so other requests can still return their
// PauseRef, and re-acquired before execution resumes.
func (s *Server) dispatchSerial(ctx context.Context, name string, args map[string]any) (string, bool) {
	select {
	case s.serialSem <- struct{}{}:
	case <-ctx.Done():
		return "request canceled", true
	}
	held := true
	defer func() {
		if held {
			<-s.serialSem
		}
	}()
	if s.approver != nil {
		ctx = withPauseHooks(ctx, pauseHooks{
			onPark: func() {
				if held {
					held = false
					<-s.serialSem
				}
			},
			onResume: func(resumeCtx context.Context) bool {
				if held {
					return true
				}
				select {
				case s.serialSem <- struct{}{}:
					held = true
					return true
				case <-resumeCtx.Done():
					return false
				}
			},
		})
	}
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
	out, isErr = blockText(res.Output), res.Output.IsError
	if !isErr && s.onMutate != nil {
		s.onMutate(name)
	}
	return out, isErr
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

// registerExternalTaskTool exposes only read-only get_task on the MCP data plane.
// Approval resolution intentionally lives on the out-of-band ControlServer socket,
// never as a model-callable MCP tool.
func (s *Server) registerExternalTaskTool() {
	s.sdk.AddTool(&mcpsdk.Tool{
		Name: GetTaskToolName,
		Description: "Wait for or retrieve the result of a previously approved OpsKat operation that is still running in the background. " +
			"Pass the task_id returned after approval.",
		InputSchema: agent.Schema{
			Type: "object",
			Properties: map[string]*agent.Property{
				"task_id": {Type: "string", Description: "Task ID returned when an approved operation continued running in the background."},
				"wait_ms": {Type: "number", Description: "Optional maximum time in milliseconds to wait for completion (default 15000, max 60000)."},
			},
			Required: []string{"task_id"},
		},
		Annotations: &mcpsdk.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: new(false),
			IdempotentHint:  true,
			OpenWorldHint:   new(false),
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var args map[string]any
		if raw := req.Params.Arguments; len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid tool arguments: " + err.Error()}
			}
		}
		res, err := s.approver.GetTask(ctx, aictx.ArgString(args, "task_id"), aictx.ArgInt(args, "wait_ms"))
		if err != nil {
			return textResult(err.Error(), true), nil
		}
		return taskResultToMCP(res), nil
	})
}

func textResult(text string, isError bool) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
		IsError: isError,
	}
}

func structuredResult(text string, structured any, isError bool) *mcpsdk.CallToolResult {
	res := textResult(text, isError)
	res.StructuredContent = structured
	return res
}

func taskResultToMCP(res *TaskResultPayload) *mcpsdk.CallToolResult {
	safe := toDataPlaneTaskResult(res)
	switch safe.Status {
	case StatusCompleted:
		return structuredResult(safe.Output, safe, safe.IsError)
	case StatusNeedsApproval, StatusNeedsMFA:
		if safe.PauseRef != nil {
			summary := fmt.Sprintf("Task %s paused (%s, approval_id=%s). Wait for the user to decide before continuing.",
				safe.TaskID, safe.Status, safe.PauseRef.ApprovalID)
			return structuredResult(summary, safe, false)
		}
		return structuredResult(fmt.Sprintf("Task %s paused (%s).", safe.TaskID, safe.Status), safe, false)
	default:
		summary := fmt.Sprintf("Task %s is still running in the background. Call %s(task_id=%q) to wait for its result.",
			safe.TaskID, GetTaskToolName, safe.TaskID)
		return structuredResult(summary, safe, false)
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
