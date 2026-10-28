// pkg/extension/hostfn.go
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// readGuestBytes reads bytes from guest memory and makes a copy.
func readGuestBytes(mod api.Module, ptr, size uint32) []byte {
	if size == 0 {
		return nil
	}
	data, ok := mod.Memory().Read(ptr, size)
	if !ok {
		return nil
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp
}

// writeHostReply allocates guest memory and writes a tagged reply into it,
// returning packed (ptr, size) as uint64. 0 means the reply could not be
// delivered at all, which the guest reports rather than mistaking for a result.
func writeHostReply(ctx context.Context, mod api.Module, payload []byte, callErr error) uint64 {
	tag := byte(hostReplyTagOK)
	if callErr != nil {
		tag, payload = hostReplyTagErr, []byte(callErr.Error())
	}
	buf := make([]byte, 0, len(payload)+1)
	buf = append(buf, tag)
	buf = append(buf, payload...)

	malloc := mod.ExportedFunction("malloc")
	if malloc == nil {
		return 0
	}
	results, err := malloc.Call(ctx, uint64(len(buf)))
	if err != nil || len(results) == 0 || results[0] == 0 {
		return 0
	}
	ptr := uint32(results[0])
	if !mod.Memory().Write(ptr, buf) {
		return 0
	}
	return uint64(ptr)<<32 | uint64(len(buf))
}

// deadlineTime converts an absolute Unix-nanosecond deadline from the guest into
// a time.Time. 0 means "clear the deadline", which is the zero Time.
func deadlineTime(unixNanos int64) time.Time {
	if unixNanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNanos)
}

func init() {
	for op, fn := range hostOps {
		hostOps[op] = gateScopeOps(op, fn)
	}
}

// gateScopeOps makes fn refuse a call whose scope does not list op (Scope.Ops),
// before anything reaches the host. io.open is checked per stream type.
func gateScopeOps(op string, fn hostOpFunc) hostOpFunc {
	return func(env hostCallEnv, params json.RawMessage) ([]byte, error) {
		scope := env.inv.boundScope()
		if scope == nil || scope.Ops == nil { // nil: a tool call, before or after its scope resolves
			return fn(env, params)
		}
		name := op
		if op == OpIOOpen {
			var p struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			name = IOOpenOp(p.Type)
		}
		if !scope.allows(name) {
			return nil, fmt.Errorf("host call %q is not allowed in this call's scope, which allows only %s", name, strings.Join(scope.Ops, ", "))
		}
		return fn(env, params)
	}
}
