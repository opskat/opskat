package mcp

import (
	"encoding/json"

	"github.com/cago-frame/agents/agent"
)

// ProtocolVersion is the default MCP protocol version advertised by opsctl mcp.
const ProtocolVersion = "2025-03-26"

// ApprovalMode controls how NeedConfirm operations are handled during an MCP session.
type ApprovalMode string

const (
	// ApprovalModeDesktop delegates approvals to the running OpsKat desktop app
	// over local IPC, falling back to structured refusal when unreachable.
	ApprovalModeDesktop ApprovalMode = "desktop"
	// ApprovalModeExternal pauses NeedConfirm operations and returns structured
	// pending payloads for an external host (such as a Hob plugin adapter) to resolve.
	ApprovalModeExternal ApprovalMode = "external"
)

// Toolset controls which subset of OpsKat tools is advertised via tools/list.
type Toolset string

const (
	// ToolsetCore exposes the high-frequency operational tools (<= 10 tools).
	ToolsetCore Toolset = "core"
	// ToolsetFull exposes all OpsKat AI tools plus list_audit (and get_task in external mode).
	ToolsetFull Toolset = "full"
)

// JSON-RPC 2.0 standard error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Request is an incoming JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request omits an id (and expects no response).
func (r Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// Response is an outgoing JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ToolSpec describes a single tool returned by tools/list.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema agent.Schema   `json:"inputSchema"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

// ContentItem is a single MCP content block.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// CallToolResult is the MCP tools/call result payload.
type CallToolResult struct {
	Content           []ContentItem  `json:"content"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
	Meta              map[string]any `json:"_meta,omitempty"`
}

func textResult(text string, isError bool) *CallToolResult {
	return &CallToolResult{
		Content: []ContentItem{{Type: "text", Text: text}},
		IsError: isError,
	}
}

func structuredResult(text string, structured any, isError bool) *CallToolResult {
	return &CallToolResult{
		Content:           []ContentItem{{Type: "text", Text: text}},
		StructuredContent: structured,
		IsError:           isError,
	}
}
