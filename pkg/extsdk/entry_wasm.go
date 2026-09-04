//go:build wasip1

package opskat

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"unsafe"
)

// The guest is built as a WASI reactor (`-buildmode=c-shared`): the host runs
// `_initialize` once and then calls opskat_call for every invocation, so the Go
// runtime is initialized exactly once per instance instead of once per call.
//
// `_initialize` runs package init functions only — `func main()` is never
// called. Extensions therefore register their handlers from `init()`.

// responseTag values prefix the buffer returned by opskat_call.
const (
	responseTagOK  = 0
	responseTagErr = 1
)

// lastResponse keeps the buffer returned by the previous opskat_call alive.
// The host reads it immediately after the call returns and before any further
// guest code runs, so a single slot is enough.
var lastResponse []byte

// opskat_call is the single guest entry point.
//
// The request is a JSON envelope {"fn":"<name>","input":<raw>}; the reply is a
// one-byte tag followed by either the handler result (tag 0) or the error
// message (tag 1). The tag replaces the old "does the JSON have an .error key"
// sniffing, which could not tell a failure apart from a handler that legitimately
// returned {"error": ...}.
//
//go:wasmexport opskat_call
func opskatCall(reqPtr, reqLen uint32) uint64 {
	req := copyGuestBytes(reqPtr, reqLen)
	result, err := invoke(req)
	if err != nil {
		return packResponse(responseTagErr, []byte(err.Error()))
	}
	return packResponse(responseTagOK, result)
}

// invoke decodes the envelope and dispatches, converting a handler panic into an
// error. Without the recover a single bad handler would trap the WASM instance,
// which under the reactor model is shared by later calls.
func invoke(req []byte) (result json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("extension panic: %v\n%s", r, debug.Stack())
		}
	}()
	var env struct {
		Fn    string          `json:"fn"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(req, &env); err != nil {
		return nil, fmt.Errorf("parse call envelope: %w", err)
	}
	return dispatch(env.Fn, env.Input)
}

func packResponse(tag byte, payload []byte) uint64 {
	buf := make([]byte, 0, len(payload)+1)
	buf = append(buf, tag)
	buf = append(buf, payload...)
	lastResponse = buf
	return uint64(uint32(uintptr(unsafe.Pointer(&lastResponse[0]))))<<32 | uint64(len(lastResponse))
}

// copyGuestBytes copies host-written memory into a Go-owned slice and releases
// the host's buffer, so a long-running action that makes thousands of host calls
// does not accumulate them for the lifetime of the instance.
func copyGuestBytes(ptr, size uint32) []byte {
	if size == 0 {
		return nil
	}
	out := make([]byte, size)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size))
	free(ptr)
	return out
}
