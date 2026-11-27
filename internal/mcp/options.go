package mcp

import (
	"fmt"
	"strings"
)

// ApprovalMode controls how NeedConfirm operations are handled during an MCP session.
type ApprovalMode string

const (
	// ApprovalModeDesktop delegates approvals to the running OpsKat desktop app
	// over local IPC, failing closed when unreachable.
	ApprovalModeDesktop ApprovalMode = "desktop"
	// ApprovalModeExternal pauses NeedConfirm operations and returns structured
	// pending payloads for an external host adapter to resolve over the out-of-band
	// local control socket (--approval-control-socket).
	ApprovalModeExternal ApprovalMode = "external"
)

// Toolset controls which subset of OpsKat tools is advertised via tools/list.
type Toolset string

const (
	// ToolsetCore exposes the high-frequency operational tools (<= 10 tools).
	ToolsetCore Toolset = "core"
	// ToolsetFull exposes all OpsKat AI tools plus session-scoped list_audit
	// (and get_task in external mode).
	ToolsetFull Toolset = "full"
)

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
