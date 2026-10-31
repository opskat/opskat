package extreg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/opskat/opskat/pkg/extension"
)

// hostToolDefaultTimeout is the limit of a tool that declares no timeout: the
// runtime's own default for tool calls (pkg/extension defaultToolTimeout), which a
// host caller may shorten but not exceed.
const hostToolDefaultTimeout = 30 * time.Second

// hostCallable is the running extensions that contribute tools, by name; the tools
// contribution point fills and revokes it. Guarded by mu.
var hostCallable = make(map[string]loaded)

// CallHostTool is how a host subsystem calls the host-only tool of a loaded,
// running extension: tool must be declared hostOnly (every other tool is refused —
// exec and pages have their own entries), args is a JSON object checked against
// the tool's parameter schema, and the call runs without an asset scope.
//
// timeout is the caller's own bound on this call and may only shorten the tool's
// limit (its declared timeoutMs, or the host's 30s default): 0 or anything longer
// leaves the limit. The result is the tool's JSON answer; a guest failure, a
// timeout and ctx ending are returned as errors.
func CallHostTool(ctx context.Context, extName, tool string, args json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	mu.Lock()
	l, ok := hostCallable[extName]
	mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("extension %q is not running", extName)
	}
	def, ok := toolDef(l.manifest, tool)
	if !ok {
		return nil, fmt.Errorf("extension %s has no tool %q", extName, tool)
	}
	if !def.HostOnly {
		return nil, fmt.Errorf("%s.%s is not a host-only tool", extName, tool)
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	canonical, err := validateToolArgs(extName, def, args)
	if err != nil {
		return nil, err
	}
	limit := def.Timeout()
	if limit == 0 {
		limit = hostToolDefaultTimeout
	}
	if timeout > 0 && timeout < limit {
		limit = timeout
	}
	return l.plugin.CallTool(ctx, extension.ToolCall{Name: tool, Args: canonical, Timeout: limit})
}
