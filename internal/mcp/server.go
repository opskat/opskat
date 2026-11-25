package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/assetref"
	"github.com/opskat/opskat/internal/ai/audit"
)

// ResolveToolName is the protected internal tool used by an external UI host
// (such as a Hob plugin adapter) to resolve a pending approval or MFA challenge.
const ResolveToolName = "_approval_resolve"

// GetTaskToolName polls or waits for a backgrounded task after approval.
const GetTaskToolName = "get_task"

// ServerConfig configures an MCP stdio server.
type ServerConfig struct {
	ServerName    string
	ServerVersion string
	Tools         []agent.Tool
	ExtraTools    []ExtraTool
	Approver      *ExternalApprover // non-nil in external approval mode
	AuditWriter   audit.AuditWriter
	SessionID     string
	AuditSource   string
	PrepareCtx    func(ctx context.Context) context.Context
	OnMutate      func(toolName string)
}

// ExtraTool registers a tool not defined via agent.Tool (e.g. list_audit).
type ExtraTool struct {
	Spec    ToolSpec
	Audit   bool
	Handler func(ctx context.Context, args map[string]any) (*CallToolResult, error)
}

// Server serves MCP JSON-RPC 2.0 requests over newline-delimited stdio.
type Server struct {
	serverName    string
	serverVersion string
	specs         []ToolSpec
	agentTools    map[string]agent.Tool
	extraTools    map[string]ExtraTool
	approver      *ExternalApprover
	auditWriter   audit.AuditWriter
	sessionID     string
	auditSource   string
	prepareCtx    func(ctx context.Context) context.Context
	onMutate      func(toolName string)

	writeMu sync.Mutex
}

// NewServer constructs an MCP stdio server from the given configuration.
func NewServer(cfg ServerConfig) *Server {
	name := cfg.ServerName
	if name == "" {
		name = "opsctl"
	}
	source := cfg.AuditSource
	if source == "" {
		source = "opsctl"
	}

	s := &Server{
		serverName:    name,
		serverVersion: cfg.ServerVersion,
		agentTools:    make(map[string]agent.Tool, len(cfg.Tools)),
		extraTools:    make(map[string]ExtraTool, len(cfg.ExtraTools)+2),
		approver:      cfg.Approver,
		auditWriter:   cfg.AuditWriter,
		sessionID:     cfg.SessionID,
		auditSource:   source,
		prepareCtx:    cfg.PrepareCtx,
		onMutate:      cfg.OnMutate,
	}

	for _, t := range cfg.Tools {
		s.agentTools[t.Name()] = t
		s.specs = append(s.specs, ToolSpec{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		})
	}
	for _, extra := range cfg.ExtraTools {
		s.extraTools[extra.Spec.Name] = extra
		s.specs = append(s.specs, extra.Spec)
	}

	if s.approver != nil {
		s.registerExternalApprovalTools()
	}
	return s
}

func (s *Server) registerExternalApprovalTools() {
	getTask := ExtraTool{
		Spec: ToolSpec{
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
		},
		Handler: func(ctx context.Context, args map[string]any) (*CallToolResult, error) {
			taskID := aictx.ArgString(args, "task_id")
			waitMs := aictx.ArgInt(args, "wait_ms")
			res, err := s.approver.GetTask(ctx, taskID, waitMs)
			if err != nil {
				return textResult(err.Error(), true), nil
			}
			return taskResultToMCP(res), nil
		},
	}
	s.extraTools[GetTaskToolName] = getTask
	s.specs = append(s.specs, getTask.Spec)

	resolveTool := ExtraTool{
		Spec: ToolSpec{
			Name:        ResolveToolName,
			Description: "Internal host callback to submit a human approval or MFA decision for a paused operation.",
			InputSchema: agent.Schema{
				Type: "object",
				Properties: map[string]*agent.Property{
					"approval_id": {Type: "string", Description: "Pending approval_id from needs_approval / needs_mfa."},
					"secret":      {Type: "string", Description: "Per-process approval secret configured via OPSKAT_MCP_APPROVAL_SECRET."},
					"decision":    {Type: "string", Description: "Human decision: deny, allow, allow_session, or allow_permanent."},
					"edited_items": {
						Type:        "array",
						Description: "Optional edited approval items (for grant or allow_session edits).",
						Items:       &agent.Property{Type: "object"},
					},
					"mfa_answers": {
						Type:        "array",
						Description: "Optional MFA prompt answers in server order (for kind=mfa).",
						Items:       &agent.Property{Type: "string"},
					},
					"wait_ms": {Type: "number", Description: "Optional milliseconds to wait for execution completion before returning running (default 45000, max 55000)."},
				},
				Required: []string{"approval_id", "secret", "decision"},
			},
			Meta: map[string]any{
				"ui": map[string]any{
					"visibility": []string{"app"},
				},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (*CallToolResult, error) {
			raw, err := json.Marshal(args)
			if err != nil {
				return textResult(fmt.Sprintf("invalid resolve params: %v", err), true), nil
			}
			var req ResolveRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return textResult(fmt.Sprintf("invalid resolve params: %v", err), true), nil
			}
			res, err := s.approver.Resolve(ctx, req)
			if err != nil {
				return textResult(err.Error(), true), nil
			}
			return taskResultToMCP(res), nil
		},
	}
	s.extraTools[ResolveToolName] = resolveTool
	s.specs = append(s.specs, resolveTool.Spec)
}

// Serve runs the stdio JSON-RPC loop until r reaches EOF or ctx is canceled.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	serveCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	// Register wg.Wait BEFORE cancel/Close so LIFO defers cancel & unblock in-flight
	// handlers first, then wait for them to exit cleanly without deadlocking.
	defer wg.Wait()
	defer cancel()
	if s.approver != nil {
		defer s.approver.Close()
	}

	logger.Ctx(serveCtx).Info("opsctl mcp server started",
		zap.String("server", s.serverName),
		zap.Int("tools", len(s.specs)),
		zap.Bool("externalApproval", s.approver != nil))

	reader := bufio.NewReaderSize(r, 64*1024)

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) > 0 {
				var req Request
				if uerr := json.Unmarshal(trimmed, &req); uerr != nil {
					_ = s.writeResponse(w, Response{
						JSONRPC: "2.0",
						ID:      json.RawMessage("null"),
						Error:   &RPCError{Code: CodeParseError, Message: "parse error: " + uerr.Error()},
					})
				} else if !req.IsNotification() {
					wg.Add(1)
					go func(req Request) {
						defer wg.Done()
						resp := s.handleRequest(serveCtx, req)
						if werr := s.writeResponse(w, resp); werr != nil {
							logger.Ctx(serveCtx).Warn("mcp write response failed", zap.Error(werr))
						}
					}(req)
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				logger.Ctx(serveCtx).Info("opsctl mcp server stopped: stdin closed")
				return nil
			}
			logger.Ctx(serveCtx).Error("opsctl mcp read error", zap.Error(err))
			return err
		}
		select {
		case <-serveCtx.Done():
			logger.Ctx(serveCtx).Info("opsctl mcp server stopped: context canceled")
			return serveCtx.Err()
		default:
		}
	}
}

func (s *Server) writeResponse(w io.Writer, resp Response) error {
	if resp.JSONRPC == "" {
		resp.JSONRPC = "2.0"
	}
	if len(resp.ID) == 0 {
		resp.ID = json.RawMessage("null")
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = w.Write(data)
	return err
}

func (s *Server) handleRequest(ctx context.Context, req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			logger.Ctx(ctx).Error("mcp request panic recovered",
				zap.String("method", req.Method),
				zap.Any("panic", r),
				zap.Stack("stack"))
			resp = Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: CodeInternalError, Message: "internal server error"},
			}
		}
	}()

	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &params)
		}
		version := ProtocolVersion
		if params.ProtocolVersion != "" {
			version = params.ProtocolVersion
		}
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": version,
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    s.serverName,
					"version": s.serverVersion,
				},
			},
		}

	case "ping":
		return Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": s.specs},
		}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: CodeInvalidParams, Message: "invalid tools/call params: " + err.Error()},
			}
		}
		if params.Arguments == nil {
			params.Arguments = map[string]any{}
		}
		result, rpcErr := s.callTool(ctx, params.Name, params.Arguments)
		if rpcErr != nil {
			return Response{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
		}
		return Response{JSONRPC: "2.0", ID: req.ID, Result: result}

	default:
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: CodeMethodNotFound, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}
}

func (s *Server) callTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, *RPCError) {
	if extra, ok := s.extraTools[name]; ok {
		runCtx := s.enrichContext(ctx)
		res, err := extra.Handler(runCtx, args)
		if err != nil {
			return textResult(err.Error(), true), nil
		}
		return res, nil
	}

	t, ok := s.agentTools[name]
	if !ok {
		return nil, &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", name)}
	}

	execFn := func(callCtx context.Context) (string, bool) {
		return s.executeAgentTool(callCtx, t, args)
	}

	if s.approver != nil {
		return s.approver.Run(ctx, ctx, name, execFn), nil
	}
	out, isErr := execFn(ctx)
	return textResult(out, isErr), nil
}

func (s *Server) enrichContext(ctx context.Context) context.Context {
	ctx = aictx.WithAuditSource(ctx, s.auditSource)
	if s.sessionID != "" {
		ctx = aictx.WithSessionID(ctx, s.sessionID)
	}
	if s.prepareCtx != nil {
		ctx = s.prepareCtx(ctx)
	}
	return ctx
}

func (s *Server) executeAgentTool(ctx context.Context, t agent.Tool, args map[string]any) (string, bool) {
	ctx = s.enrichContext(ctx)

	slot := &aictx.CheckResult{}
	commandSlot := ""
	ctx = aictx.WithCheckResultSlot(ctx, slot)
	ctx = aictx.WithAuditCommandSlot(ctx, &commandSlot)
	ctx = aictx.WithAuditRequestSlot(ctx, aictx.NewAuditRequestSlot())
	ctx = aictx.WithAuditResultSlot(ctx, aictx.NewAuditResultSlot())

	var assetID int64
	var assetName string
	if s.auditWriter != nil {
		assetID, assetName = preResolveAssetForAudit(ctx, args)
	}

	block, err := t.Call(ctx, args)

	var textOut string
	isErr := err != nil
	if err != nil {
		textOut = err.Error()
	} else if block != nil {
		textOut = extractBlockText(block)
		isErr = block.IsError
	}
	if slot.Decision == aictx.Deny {
		isErr = true
	}

	if s.auditWriter != nil {
		auditArgs := args
		if projected := aictx.GetAuditRequest(ctx); projected != nil {
			auditArgs = projected
		}
		argsJSON, merr := json.Marshal(auditArgs)
		if merr != nil {
			logger.Ctx(ctx).Warn("mcp audit marshal input", zap.String("toolName", t.Name()), zap.Error(merr))
		}
		auditResult := textOut
		auditErr := err
		if auditErr == nil && isErr {
			auditErr = errors.New(textOut)
		}
		if summary, ok := aictx.GetAuditResult(ctx); ok {
			if auditErr != nil {
				auditErr = errors.New(summary)
			} else {
				auditResult = summary
			}
		}
		var dec *aictx.CheckResult
		if slot.DecisionSource != "" || slot.Decision != aictx.Allow {
			dec = slot
		}
		info := audit.ToolCallInfo{
			ToolName:  t.Name(),
			ArgsJSON:  string(argsJSON),
			Result:    auditResult,
			Error:     auditErr,
			Decision:  dec,
			AssetID:   assetID,
			AssetName: assetName,
			Command:   commandSlot,
		}
		s.auditWriter.WriteToolCall(ctx, info)
	}

	if !isErr && s.onMutate != nil {
		s.onMutate(t.Name())
	}

	return textOut, isErr
}

func preResolveAssetForAudit(ctx context.Context, args map[string]any) (int64, string) {
	ref := aictx.ArgString(args, "asset")
	if ref == "" {
		if id := aictx.ArgInt64(args, "asset_id"); id > 0 {
			ref = fmt.Sprintf("%d", id)
		} else if id := aictx.ArgInt64(args, "id"); id > 0 {
			ref = fmt.Sprintf("%d", id)
		}
	}
	if ref == "" {
		return 0, ""
	}
	asset, err := assetref.Resolve(ctx, ref)
	if err != nil || asset == nil {
		return 0, ""
	}
	return asset.ID, asset.Name
}

func extractBlockText(block *agent.ToolResultBlock) string {
	if block == nil {
		return ""
	}
	var parts []string
	for _, item := range block.Content {
		if tb, ok := item.(agent.TextBlock); ok {
			parts = append(parts, tb.Text)
		} else if tbPtr, ok := item.(*agent.TextBlock); ok && tbPtr != nil {
			parts = append(parts, tbPtr.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func taskResultToMCP(res *TaskResultPayload) *CallToolResult {
	switch res.Status {
	case StatusCompleted:
		return structuredResult(res.Output, res, res.IsError)
	case StatusNeedsApproval, StatusNeedsMFA:
		if res.Pause != nil {
			summary := fmt.Sprintf("Task %s paused (%s, approval_id=%s).", res.TaskID, res.Status, res.Pause.ApprovalID)
			return structuredResult(summary, res, false)
		}
		return structuredResult(fmt.Sprintf("Task %s paused (%s).", res.TaskID, res.Status), res, false)
	default:
		summary := fmt.Sprintf("Task %s is still running in the background. Call %s(task_id=%q) to wait for its result.",
			res.TaskID, GetTaskToolName, res.TaskID)
		return structuredResult(summary, res, false)
	}
}

// FilterTools returns the tools matching the requested Toolset in deterministic order.
func FilterTools(all []agent.Tool, toolset Toolset) []agent.Tool {
	if toolset == ToolsetFull {
		return all
	}
	core := map[string]bool{
		"list_assets":        true,
		"get_asset":          true,
		"help":               true,
		"exec":               true,
		"batch_exec":         true,
		"cp":                 true,
		"request_permission": true,
	}
	out := make([]agent.Tool, 0, len(core))
	for _, t := range all {
		if core[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

// ParseApprovalMode validates an approval mode string.
func ParseApprovalMode(raw string) (ApprovalMode, error) {
	switch ApprovalMode(strings.TrimSpace(raw)) {
	case "", ApprovalModeDesktop:
		return ApprovalModeDesktop, nil
	case ApprovalModeExternal:
		return ApprovalModeExternal, nil
	default:
		return "", fmt.Errorf("invalid --approval-mode %q: must be desktop or external", raw)
	}
}

// ParseToolset validates a toolset string.
func ParseToolset(raw string) (Toolset, error) {
	switch Toolset(strings.TrimSpace(raw)) {
	case "", ToolsetCore:
		return ToolsetCore, nil
	case ToolsetFull:
		return ToolsetFull, nil
	default:
		return "", fmt.Errorf("invalid --toolset %q: must be core or full", raw)
	}
}
