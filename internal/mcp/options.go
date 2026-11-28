package mcp

import (
	"fmt"
	"strings"
)

// Toolset controls which subset of OpsKat tools is advertised via tools/list.
type Toolset string

const (
	// ToolsetCore exposes the high-frequency operational tools (<= 10 tools).
	ToolsetCore Toolset = "core"
	// ToolsetFull exposes all OpsKat AI tools plus session-scoped list_audit.
	ToolsetFull Toolset = "full"
)

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
