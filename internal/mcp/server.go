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
	"github.com/opskat/opskat/internal/ai/audit"
	"github.com/opskat/opskat/internal/ai/runner"
)

// GetTaskToolName polls or waits for a backgrounded task after out-of-band approval.
const GetTaskToolName = "get_task"

// NotificationCancelled is the MCP JSON-RPC cancellation notification method name.
const NotificationCancelled = "notifications/cancelled" //nolint:misspell // MCP protocol uses British spelling

const (
	defaultMaxRequestLineBytes = 4 << 20 // 4 MiB per JSON-RPC frame
	defaultMaxConcurrentReqs   = 32
)

var errLineTooLong = errors.New("JSON-RPC request exceeds maximum allowed size")

// serialTool is implemented by tools that require serialized execution (e.g. *tool.RawTool).
type serialTool interface {
	Serial() bool
}

// ServerConfig configures an MCP stdio server.
type ServerConfig struct {
	ServerName        string
	ServerVersion     string
	Tools             []agent.Tool
	ExtraTools        []ExtraTool
	Approver          *ExternalApprover // non-nil in external approval mode
	AuditWriter       audit.AuditWriter
	SessionID         string
	AuditSource       string
	PrepareCtx        func(ctx context.Context) context.Context
	OnMutate          func(toolName string)
	MaxRequestBytes   int // defaults to 4 MiB
	MaxConcurrentReqs int // defaults to 32
}

// ExtraTool registers a tool not defined via agent.Tool (e.g. list_audit).
type ExtraTool struct {
	Spec    ToolSpec
	Audit   bool
	Handler func(ctx context.Context, args map[string]any) (*CallToolResult, error)
}

// Server serves MCP JSON-RPC 2.0 requests over newline-delimited stdio.
type Server struct {
	serverName      string
	serverVersion   string
	specs           []ToolSpec
	agentTools      map[string]agent.Tool
	extraTools      map[string]ExtraTool
	approver        *ExternalApprover
	auditWriter     audit.AuditWriter
	sessionID       string
	auditSource     string
	prepareCtx      func(ctx context.Context) context.Context
	onMutate        func(toolName string)
	maxRequestBytes int
	reqSem          chan struct{}
	serialSem       chan struct{}

	writeMu    sync.Mutex
	inFlightMu sync.Mutex
	inFlight   map[string]context.CancelFunc
}

// NewServer constructs an MCP stdio server from the given configuration.
func NewServer(cfg ServerConfig) *Server {
	name := cfg.ServerName
	if name == "" {
		name = "opsctl"
	}
	source := cfg.AuditSource
	if source == "" {
		source = "mcp"
	}
	maxBytes := cfg.MaxRequestBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxRequestLineBytes
	}
	maxConc := cfg.MaxConcurrentReqs
	if maxConc <= 0 {
		maxConc = defaultMaxConcurrentReqs
	}

	s := &Server{
		serverName:      name,
		serverVersion:   cfg.ServerVersion,
		agentTools:      make(map[string]agent.Tool, len(cfg.Tools)),
		extraTools:      make(map[string]ExtraTool, len(cfg.ExtraTools)+1),
		approver:        cfg.Approver,
		auditWriter:     cfg.AuditWriter,
		sessionID:       cfg.SessionID,
		auditSource:     source,
		prepareCtx:      cfg.PrepareCtx,
		onMutate:        cfg.OnMutate,
		maxRequestBytes: maxBytes,
		reqSem:          make(chan struct{}, maxConc),
		serialSem:       make(chan struct{}, 1),
		inFlight:        make(map[string]context.CancelFunc),
	}

	for _, t := range cfg.Tools {
		s.agentTools[t.Name()] = t
		s.specs = append(s.specs, ToolSpec{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
			Annotations: toolAnnotationsFor(t.Name()),
		})
	}
	for _, extra := range cfg.ExtraTools {
		if extra.Spec.Annotations == nil {
			extra.Spec.Annotations = toolAnnotationsFor(extra.Spec.Name)
		}
		s.extraTools[extra.Spec.Name] = extra
		s.specs = append(s.specs, extra.Spec)
	}

	if s.approver != nil {
		s.registerExternalTaskTool()
	}
	return s
}

func boolPtr(v bool) *bool { return &v }

var supportedProtocolVersions = map[string]bool{
	ProtocolVersion: true,
	"2024-11-05":    true,
}

// toolAnnotationsFor returns MCP 2025-03-26 behavioral hints for known OpsKat tools.
func toolAnnotationsFor(name string) *ToolAnnotations {
	switch name {
	case "list_assets", "get_asset", "list_groups", "get_group",
		"list_credentials", "get_credential", "help", "list_audit", GetTaskToolName:
		return &ToolAnnotations{
			ReadOnlyHint:    boolPtr(true),
			DestructiveHint: boolPtr(false),
			IdempotentHint:  boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		}
	case "exec", "batch_exec", "cp":
		return &ToolAnnotations{
			ReadOnlyHint:    boolPtr(false),
			DestructiveHint: boolPtr(true),
			IdempotentHint:  boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		}
	case "delete_asset", "delete_group":
		return &ToolAnnotations{
			ReadOnlyHint:    boolPtr(false),
			DestructiveHint: boolPtr(true),
			IdempotentHint:  boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		}
	case "put_asset", "put_group", "request_permission", "get_asset_secret":
		return &ToolAnnotations{
			ReadOnlyHint:    boolPtr(false),
			DestructiveHint: boolPtr(false),
			IdempotentHint:  boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		}
	default:
		return nil
	}
}

// registerExternalTaskTool exposes only read-only get_task on the MCP data plane.
// Approval resolution intentionally lives on the out-of-band ControlServer socket,
// never as a model-callable MCP tool.
func (s *Server) registerExternalTaskTool() {
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
			Annotations: toolAnnotationsFor(GetTaskToolName),
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

	type readResult struct {
		line []byte
		err  error
	}
	lineCh := make(chan readResult, 1)
	go func() {
		defer close(lineCh)
		reader := bufio.NewReaderSize(r, 64*1024)
		for {
			line, err := readLineLimited(reader, s.maxRequestBytes)
			select {
			case lineCh <- readResult{line: line, err: err}:
			case <-serveCtx.Done():
				return
			}
			if err != nil && !errors.Is(err, errLineTooLong) {
				return
			}
		}
	}()

	for {
		var res readResult
		select {
		case <-serveCtx.Done():
			logger.Ctx(serveCtx).Info("opsctl mcp server stopped: context canceled")
			return serveCtx.Err()
		case item, ok := <-lineCh:
			if !ok {
				logger.Ctx(serveCtx).Info("opsctl mcp server stopped: stdin closed")
				return nil
			}
			res = item
		}
		line, err := res.line, res.err
		if errors.Is(err, errLineTooLong) {
			_ = s.writeResponse(w, Response{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &RPCError{Code: CodeInvalidRequest, Message: errLineTooLong.Error()},
			})
			continue
		}
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
				} else if req.IsNotification() {
					s.handleNotification(req)
				} else {
					select {
					case s.reqSem <- struct{}{}:
						reqCtx, reqCancel := context.WithCancel(serveCtx)
						reqKey := canonicalRequestID(req.ID)
						if !s.registerInFlight(reqKey, reqCancel) {
							reqCancel()
							<-s.reqSem
							_ = s.writeResponse(w, Response{
								JSONRPC: "2.0",
								ID:      req.ID,
								Error:   &RPCError{Code: CodeInvalidRequest, Message: "duplicate in-flight request id"},
							})
							break
						}
						wg.Add(1)
						go func(req Request, rCtx context.Context, rCancel context.CancelFunc, key string) {
							defer wg.Done()
							defer func() {
								s.unregisterInFlight(key)
								rCancel()
								<-s.reqSem
							}()
							resp := s.handleRequest(serveCtx, rCtx, req)
							if werr := s.writeResponse(w, resp); werr != nil {
								logger.Ctx(serveCtx).Warn("mcp write response failed", zap.Error(werr))
							}
						}(req, reqCtx, reqCancel, reqKey)
					default:
						_ = s.writeResponse(w, Response{
							JSONRPC: "2.0",
							ID:      req.ID,
							Error:   &RPCError{Code: CodeInternalError, Message: "server busy: too many concurrent requests"},
						})
					}
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
	}
}

func readLineLimited(r *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) > limit {
			// Drain remainder of the oversized line up to '\n' or EOF.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = r.ReadSlice('\n')
			}
			return nil, errLineTooLong
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return buf, err
	}
}

func canonicalRequestID(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(trimmed)
	}
	switch typed := v.(type) {
	case string:
		return "s:" + typed
	case json.Number:
		return "n:" + typed.String()
	default:
		return string(trimmed)
	}
}

func (s *Server) registerInFlight(key string, cancel context.CancelFunc) bool {
	if key == "" {
		return true
	}
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	if _, exists := s.inFlight[key]; exists {
		return false
	}
	s.inFlight[key] = cancel
	return true
}

func (s *Server) unregisterInFlight(key string) {
	if key == "" {
		return
	}
	s.inFlightMu.Lock()
	delete(s.inFlight, key)
	s.inFlightMu.Unlock()
}

func (s *Server) handleNotification(req Request) {
	if req.Method != NotificationCancelled || len(req.Params) == 0 {
		return
	}
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params.RequestID) == 0 {
		return
	}
	key := canonicalRequestID(params.RequestID)
	s.inFlightMu.Lock()
	cancel, ok := s.inFlight[key]
	s.inFlightMu.Unlock()
	if ok {
		cancel()
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

func (s *Server) handleRequest(serveCtx, reqCtx context.Context, req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			logger.Ctx(reqCtx).Error("mcp request panic recovered",
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
		if supportedProtocolVersions[params.ProtocolVersion] {
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
		result, rpcErr := s.callTool(serveCtx, reqCtx, params.Name, params.Arguments)
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

func (s *Server) callTool(serveCtx, reqCtx context.Context, name string, args map[string]any) (*CallToolResult, *RPCError) {
	if extra, ok := s.extraTools[name]; ok {
		runCtx := s.enrichContext(reqCtx)
		res, err := extra.Handler(runCtx, args)
		if extra.Audit && s.auditWriter != nil {
			s.writeExtraAudit(runCtx, name, args, res, err)
		}
		if err != nil {
			return textResult(err.Error(), true), nil
		}
		return res, nil
	}

	t, ok := s.agentTools[name]
	if !ok {
		return nil, &RPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("unknown tool: %s", name)}
	}

	isSerial := false
	if st, ok := t.(serialTool); ok && st.Serial() {
		isSerial = true
	}

	execFn := func(callCtx context.Context) (string, bool) {
		if !isSerial {
			return s.executeAgentTool(callCtx, t, args)
		}
		select {
		case s.serialSem <- struct{}{}:
		case <-callCtx.Done():
			return "request canceled", true
		}
		held := true
		defer func() {
			if held {
				<-s.serialSem
			}
		}()
		if s.approver != nil {
			// Release serialSem while parked waiting for human approval so other requests
			// can enter and return their PauseRef without deadlocking, then re-acquire
			// serialSem before resuming execution so Serial tools never execute concurrently.
			callCtx = withPauseHooks(callCtx, pauseHooks{
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
		return s.executeAgentTool(callCtx, t, args)
	}

	if s.approver != nil {
		// Parent ctx is serveCtx so a parked background task survives after tools/call returns PauseRef;
		// reqCtx still cancels before pausing if the client sends a cancellation notification.
		return s.approver.Run(serveCtx, reqCtx, name, execFn), nil
	}
	out, isErr := execFn(reqCtx)
	return textResult(out, isErr), nil
}

func (s *Server) writeExtraAudit(ctx context.Context, name string, args map[string]any, res *CallToolResult, callErr error) {
	argsJSON, merr := json.Marshal(args)
	if merr != nil {
		logger.Ctx(ctx).Warn("mcp audit marshal extra input", zap.String("toolName", name), zap.Error(merr))
	}
	var outText string
	if res != nil && len(res.Content) > 0 {
		outText = res.Content[0].Text
	}
	auditErr := callErr
	if auditErr == nil && res != nil && res.IsError {
		auditErr = errors.New(outText)
	}
	s.auditWriter.WriteToolCall(runner.DetachAuditContext(ctx), audit.ToolCallInfo{
		ToolName: name,
		ArgsJSON: string(argsJSON),
		Result:   outText,
		Error:    auditErr,
	})
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
	var assetName, command string
	if s.auditWriter != nil {
		assetID, assetName, command = runner.ResolveAssetForAudit(ctx, t.Name(), args)
	}

	block, err := t.Call(ctx, args)
	if commandSlot != "" {
		command = commandSlot
	}

	var textOut string
	isErr := err != nil
	if err != nil {
		textOut = err.Error()
	} else if block != nil {
		if slot.Decision == aictx.Deny {
			block.IsError = true
		}
		var blockErr error
		textOut, blockErr = runner.ExtractAuditResult(block)
		if blockErr != nil {
			isErr = true
			if textOut == "" {
				textOut = blockErr.Error()
			}
		}
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
		if assetID == 0 && auditErr == nil && audit.ShouldResolveAssetFromResult(t.Name()) {
			assetID, assetName = runner.ResolveResultAssetForAudit(ctx, auditResult)
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
			Command:   command,
		}
		s.auditWriter.WriteToolCall(runner.DetachAuditContext(ctx), info)
	}

	if !isErr && s.onMutate != nil {
		s.onMutate(t.Name())
	}

	return textOut, isErr
}

func taskResultToMCP(res *TaskResultPayload) *CallToolResult {
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
