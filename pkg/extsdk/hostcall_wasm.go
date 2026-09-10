//go:build wasip1

package opskat

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"runtime"
	"unsafe"
)

// The host exposes two imports (see pkg/extension/host_module.go):
//
//	host_call — control plane, a JSON envelope {"op":..., "params":...}
//	host_io   — data plane, raw bytes keyed by handle and opcode
//
// Everything below is a typed wrapper over those two. Reaching for a new host
// capability means adding one method here and one entry in the host's dispatch
// table, instead of threading a new wasmimport through both sides.

//go:wasmimport opskat host_call
func wasmHostCall(reqPtr, reqLen uint32) uint64

//go:wasmimport opskat host_io
func wasmHostIO(handle, op, ptr, size uint32) uint64

// host_io operation codes, mirroring the host.
const (
	ioOpRead  = 0
	ioOpWrite = 1
	ioOpFlush = 2
	ioOpClose = 3
)

// Reply framing, mirroring the host.
const (
	replyTagOK  = 0
	replyTagErr = 1
)

// currentHost is the real WASM host caller.
var currentHost hostCaller = &wasmHostCaller{}

type wasmHostCaller struct{}

// bytesToPtr converts a Go byte slice to (ptr, len) for the host.
//
// The returned pointer is invisible to the GC, so every caller must keep the
// slice reachable (runtime.KeepAlive) until the host call has returned. Under
// the old one-instance-per-call model the collector rarely got a chance to run;
// a reactor instance is long-lived and it does.
func bytesToPtr(b []byte) (uint32, uint32) {
	if len(b) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b))
}

// unpackReply copies a host reply out of the buffer the host allocated, frees
// it, and splits the tag from the payload.
func unpackReply(packed uint64) ([]byte, error) {
	if packed == 0 {
		return nil, fmt.Errorf("host call failed to return a reply")
	}
	ptr := uint32(packed >> 32)
	size := uint32(packed & 0xFFFFFFFF)
	if size == 0 {
		return nil, fmt.Errorf("host call returned an empty reply")
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size)
	tag := raw[0]
	payload := make([]byte, size-1)
	copy(payload, raw[1:])
	free(ptr)

	switch tag {
	case replyTagOK:
		return payload, nil
	case replyTagErr:
		return nil, fmt.Errorf("%s", payload)
	default:
		return nil, fmt.Errorf("host call returned unknown reply tag %d", tag)
	}
}

// call sends a control-plane request and returns its payload.
func call(op string, params any) ([]byte, error) {
	var raw json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal %s params: %w", op, err)
		}
		raw = encoded
	}
	req, err := json.Marshal(struct {
		Op     string          `json:"op"`
		Params json.RawMessage `json:"params,omitempty"`
	}{Op: op, Params: raw})
	if err != nil {
		return nil, fmt.Errorf("marshal %s request: %w", op, err)
	}
	ptr, size := bytesToPtr(req)
	packed := wasmHostCall(ptr, size)
	runtime.KeepAlive(req)
	return unpackReply(packed)
}

// callIO sends a data-plane request. data is only read for writes; size doubles
// as the requested length for reads.
func callIO(handle, op uint32, data []byte, size uint32) ([]byte, error) {
	ptr, dataLen := bytesToPtr(data)
	if op == ioOpWrite {
		size = dataLen
	}
	packed := wasmHostIO(handle, op, ptr, size)
	runtime.KeepAlive(data)
	return unpackReply(packed)
}

func (w *wasmHostCaller) Log(level, msg string) {
	// Logging must not fail a handler; the host already records delivery problems.
	_, _ = call("log", map[string]string{"level": level, "msg": msg})
}

func (w *wasmHostCaller) IOOpen(params []byte) (uint32, []byte, error) {
	payload, err := call("io.open", json.RawMessage(params))
	if err != nil {
		return 0, nil, err
	}
	var resp struct {
		Handle uint32          `json:"handle"`
		Meta   json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(payload, &resp); err != nil {
		return 0, nil, fmt.Errorf("parse io.open reply: %w", err)
	}
	return resp.Handle, resp.Meta, nil
}

func (w *wasmHostCaller) IORead(handleID uint32, size int) ([]byte, error) {
	return callIO(handleID, ioOpRead, nil, uint32(size))
}

func (w *wasmHostCaller) IOWrite(handleID uint32, data []byte) (int, error) {
	payload, err := callIO(handleID, ioOpWrite, data, 0)
	if err != nil {
		return 0, err
	}
	if len(payload) != 4 {
		return 0, fmt.Errorf("host_io write returned %d bytes, want 4", len(payload))
	}
	return int(binary.LittleEndian.Uint32(payload)), nil
}

func (w *wasmHostCaller) IOFlush(handleID uint32) ([]byte, error) {
	return callIO(handleID, ioOpFlush, nil, 0)
}

func (w *wasmHostCaller) IOClose(handleID uint32) error {
	_, err := callIO(handleID, ioOpClose, nil, 0)
	return err
}

func (w *wasmHostCaller) IOSetDeadline(handleID uint32, kind string, unixNanos int64) error {
	_, err := call("io.set_deadline", map[string]any{
		"handle":     handleID,
		"kind":       kind,
		"unix_nanos": unixNanos,
	})
	return err
}

func (w *wasmHostCaller) AssetGetConfig(assetID int64) (json.RawMessage, error) {
	return call("asset.get_config", map[string]any{"asset_id": assetID})
}

func (w *wasmHostCaller) FileDialog(params []byte) (string, error) {
	payload, err := call("file_dialog", json.RawMessage(params))
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (w *wasmHostCaller) KVGet(key string) ([]byte, error) {
	return call("kv.get", map[string]string{"key": key})
}

func (w *wasmHostCaller) KVSet(key string, value []byte) error {
	_, err := call("kv.set", map[string]any{"key": key, "value": value})
	return err
}

func (w *wasmHostCaller) ActionEvent(eventType string, data []byte) {
	// Events are fire-and-forget; a delivery failure is the host's to log.
	_, _ = call("action.event", map[string]any{"type": eventType, "data": json.RawMessage(data)})
}

func (w *wasmHostCaller) ActionShouldStop() bool {
	payload, err := call("action.should_stop", nil)
	if err != nil {
		// The signature has no error to return, so surface it where an operator
		// can see it rather than silently pretending the action may continue.
		w.Log("warn", "action.should_stop failed: "+err.Error())
		return false
	}
	return len(payload) == 1 && payload[0] == 1
}
