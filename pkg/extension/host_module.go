// pkg/extension/host_module.go
package extension

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// The host exposes exactly two WASM imports, split by what they carry rather
// than by capability:
//
//	host_call(req_ptr, req_len) -> packed   control plane, JSON envelope
//	host_io(handle, op, ptr, len) -> packed data plane, raw bytes
//
// Every reply is a one-byte tag followed by an op-defined payload, allocated in
// guest memory through the guest's malloc; the guest frees it after copying.
// Tag 1 means the payload is an error message.
//
// The split exists because the two planes have opposite costs. A control call
// happens once per operation and benefits from being self-describing; a data
// call happens once per chunk of a file or HTTP body, where a JSON round trip
// per chunk would dominate the transfer. Adding a capability is one entry in
// hostOps below plus its typed wrapper in the guest SDK — it used to mean a new
// wasmimport, a new host function, a new HostProvider method, and a matching
// pass-through in capHost.
const (
	hostReplyTagOK  = 0
	hostReplyTagErr = 1
)

// host_io operation codes.
const (
	hostIOOpRead  = 0
	hostIOOpWrite = 1
	hostIOOpFlush = 2
	hostIOOpClose = 3
)

// hostRequest is the host_call envelope.
type hostRequest struct {
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params"`
}

// hostCallEnv is what an op handler is allowed to reach: the extension's
// capabilities, and the invocation the call belongs to.
type hostCallEnv struct {
	host HostProvider
	inv  *invocation
}

type hostOpFunc func(env hostCallEnv, params json.RawMessage) ([]byte, error)

// hostOps is the control-plane dispatch table. A payload of nil means "no
// result"; the reply is then just the OK tag.
var hostOps = map[string]hostOpFunc{
	"log":              opLog,
	"kv.get":           opKVGet,
	"kv.set":           opKVSet,
	"asset.get_config": opAssetGetConfig,
	"file_dialog":      opFileDialog,
	"io.open":          opIOOpen,
	"io.set_deadline":  opIOSetDeadline,
	"action.event":     opActionEvent,
	"action.should_stop": func(env hostCallEnv, _ json.RawMessage) ([]byte, error) {
		if env.inv.shouldStop() {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	},
}

// registerHostModule registers the "opskat" host module the guest imports.
func registerHostModule(ctx context.Context, r wazero.Runtime, host HostProvider) error {
	b := r.NewHostModuleBuilder("opskat")

	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, reqPtr, reqLen uint32) uint64 {
		payload, err := hostCall(ctx, host, readGuestBytes(mod, reqPtr, reqLen))
		return writeHostReply(ctx, mod, payload, err)
	}).Export("host_call")

	b.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, handle, op, ptr, size uint32) uint64 {
		payload, err := hostIO(ctx, mod, handle, op, ptr, size)
		return writeHostReply(ctx, mod, payload, err)
	}).Export("host_io")

	_, err := b.Instantiate(ctx)
	return err
}

func hostCall(ctx context.Context, host HostProvider, req []byte) ([]byte, error) {
	inv, err := invocationFrom(ctx)
	if err != nil {
		return nil, err
	}
	var envelope hostRequest
	if err := json.Unmarshal(req, &envelope); err != nil {
		return nil, fmt.Errorf("parse host call: %w", err)
	}
	fn, ok := hostOps[envelope.Op]
	if !ok {
		return nil, fmt.Errorf("unknown host op %q", envelope.Op)
	}
	return fn(hostCallEnv{host: host, inv: inv}, envelope.Params)
}

func hostIO(ctx context.Context, mod api.Module, handle, op, ptr, size uint32) ([]byte, error) {
	inv, err := invocationFrom(ctx)
	if err != nil {
		return nil, err
	}
	switch op {
	case hostIOOpRead:
		data, err := readHandle(inv, handle, int(size))
		if err == io.EOF {
			// An empty reply is EOF. Reporting it as an error would surface on
			// the guest as a plain error value rather than io.EOF, which breaks
			// io.ReadAll and every SDK built on it.
			return nil, nil
		}
		return data, err
	case hostIOOpWrite:
		n, err := inv.io.Write(handle, readGuestBytes(mod, ptr, size))
		if err != nil {
			return nil, err
		}
		var out [4]byte
		binary.LittleEndian.PutUint32(out[:], uint32(n))
		return out[:], nil
	case hostIOOpFlush:
		meta, err := inv.io.Flush(handle)
		if err != nil {
			return nil, err
		}
		return json.Marshal(meta)
	case hostIOOpClose:
		return nil, inv.io.Close(handle)
	default:
		return nil, fmt.Errorf("unknown host_io op %d", op)
	}
}

func opLog(env hostCallEnv, params json.RawMessage) ([]byte, error) {
	var p struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	env.host.Log(p.Level, p.Msg)
	return nil, nil
}

func opKVGet(env hostCallEnv, params json.RawMessage) ([]byte, error) {
	var p struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	return env.host.KVGet(p.Key)
}

func opKVSet(env hostCallEnv, params json.RawMessage) ([]byte, error) {
	var p struct {
		Key   string `json:"key"`
		Value []byte `json:"value"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	return nil, env.host.KVSet(p.Key, p.Value)
}

func opAssetGetConfig(env hostCallEnv, params json.RawMessage) ([]byte, error) {
	var p struct {
		AssetID int64 `json:"asset_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	return env.host.GetAssetConfig(p.AssetID)
}

func opFileDialog(env hostCallEnv, params json.RawMessage) ([]byte, error) {
	var p struct {
		Type string        `json:"type"`
		Opts DialogOptions `json:"opts"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	path, err := env.host.FileDialog(p.Type, p.Opts)
	if err != nil {
		return nil, err
	}
	return []byte(path), nil
}

func opIOOpen(env hostCallEnv, params json.RawMessage) ([]byte, error) {
	var p IOOpenParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	res, err := env.host.OpenIO(p)
	if err != nil {
		return nil, err
	}
	handle, err := env.inv.io.Register(res)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"handle": handle, "meta": res.Meta})
}

func opIOSetDeadline(env hostCallEnv, params json.RawMessage) ([]byte, error) {
	var p struct {
		Handle    uint32 `json:"handle"`
		Kind      string `json:"kind"`
		UnixNanos int64  `json:"unix_nanos"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	return nil, env.inv.io.SetDeadline(p.Handle, p.Kind, deadlineTime(p.UnixNanos))
}

func opActionEvent(env hostCallEnv, params json.RawMessage) ([]byte, error) {
	var p struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	return nil, env.host.ActionEvent(p.Type, p.Data)
}

// readHandle reads up to size bytes, keeping io.EOF distinguishable while never
// discarding bytes that were read alongside a real error.
func readHandle(inv *invocation, handleID uint32, size int) ([]byte, error) {
	buf := make([]byte, size)
	n, err := inv.io.Read(handleID, buf)
	if n > 0 {
		// Only io.EOF is safe to delay — the guest gets it on the next read when n==0.
		if err == nil || err == io.EOF {
			return buf[:n], nil
		}
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return nil, nil
}
