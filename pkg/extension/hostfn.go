// pkg/extension/hostfn.go
package extension

import (
	"context"
	"encoding/json"
	"fmt"
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

func errUnavailableInDriverCall(what string) error {
	return fmt.Errorf("%s is unavailable during driver calls: a database driver may only write logs, dial its database and ask for its Kerberos tickets", what)
}

// driverCallOps are the host ops a driver call may make: logging, the TCP
// streams (with their deadlines) that carry its connection to the database, and
// the Kerberos tickets it logs in with. Everything else (KV, file dialogs, asset
// config, action events) is refused.
var driverCallOps = map[string]bool{
	"log": true, "io.open": true, "io.set_deadline": true, "action.should_stop": true, "dbdriver.kerberos_ticket": true,
}

func init() {
	for op, fn := range hostOps {
		hostOps[op] = gateDriverCall(op, fn)
	}
}

// gateDriverCall makes fn refuse the calls of a database driver session (the
// invocations that carry a reach path) unless a driver call may make it. For
// io.open only a TCP stream is allowed, and it goes along the reach path alone
// (see opIOOpen); an HTTP request or a file is refused.
func gateDriverCall(op string, fn hostOpFunc) hostOpFunc {
	return func(env hostCallEnv, params json.RawMessage) ([]byte, error) {
		if env.inv.reach == nil {
			return fn(env, params)
		}
		if !driverCallOps[op] {
			return nil, errUnavailableInDriverCall("host op " + op)
		}
		if op == "io.open" {
			var p struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
			if p.Type != "tcp" {
				return nil, errUnavailableInDriverCall("io type " + p.Type)
			}
		}
		return fn(env, params)
	}
}
