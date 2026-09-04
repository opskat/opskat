//go:build !wasip1

package opskat

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// TestEvent represents a captured action event.
type TestEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// TestOption configures a TestHost.
type TestOption func(*TestHost)

// WithAssetConfig pre-loads an asset config for the given asset ID.
func WithAssetConfig(assetID int64, config any) TestOption {
	return func(h *TestHost) {
		data, _ := json.Marshal(config)
		h.assetConfigs[assetID] = data
	}
}

// WithMockHTTP sets an HTTP mock handler for all HTTP requests.
func WithMockHTTP(handler http.HandlerFunc) TestOption {
	return func(h *TestHost) { h.httpHandler = handler }
}

// WithMockTCP installs a TCP dialer for IOOpen(type=tcp). The dialer receives
// the requested addr and returns a net.Conn that backs the TestHost's IO
// operations for the resulting handle.
func WithMockTCP(dial func(addr string) (net.Conn, error)) TestOption {
	return func(h *TestHost) { h.mockTCP = dial }
}

// WithActionCancel marks the TestHost as already-canceled, so ActionShouldStop
// returns true. Used to exercise cooperative-cancel paths in action handlers.
func WithActionCancel() TestOption {
	return func(h *TestHost) { h.actionStopped.Store(true) }
}

// TestHost simulates the host environment for extension unit testing.
type TestHost struct {
	assetConfigs  map[int64]json.RawMessage
	kv            map[string][]byte
	httpHandler   http.HandlerFunc
	mockTCP       func(addr string) (net.Conn, error)
	mockTCPMu     sync.Mutex
	mockTCPConns  map[uint32]net.Conn
	actionStopped atomic.Bool
	events        []TestEvent
	eventCb       func(TestEvent)
	mu            sync.Mutex
}

// NewTestHost creates a TestHost and installs it as the active host.
func NewTestHost(opts ...TestOption) *TestHost {
	h := &TestHost{
		assetConfigs: make(map[int64]json.RawMessage),
		kv:           make(map[string][]byte),
	}
	for _, o := range opts {
		o(h)
	}
	SetHostStub(h)
	return h
}

// Close resets the host stub.
func (h *TestHost) Close() {
	SetHostStub(nil)
}

// CallTool invokes a registered tool handler against asset, the way the host
// invokes it for `exec <asset> -- <tool>`. Pass the zero Asset for a call that is
// not scoped to one.
func (h *TestHost) CallTool(asset Asset, name string, args any) (any, error) {
	argsJSON, _ := json.Marshal(args)
	input, _ := json.Marshal(map[string]any{
		"tool":  name,
		"args":  json.RawMessage(argsJSON),
		"asset": asset,
	})
	result, err := dispatch("execute_tool", input)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(result, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CallAction invokes a registered action handler with event capture, scoped to
// asset (the zero Asset for an action the caller has no asset for).
func (h *TestHost) CallAction(asset Asset, name string, args any, onEvent func(TestEvent)) (any, error) {
	h.mu.Lock()
	h.events = nil
	h.eventCb = onEvent
	h.mu.Unlock()

	argsJSON, _ := json.Marshal(args)
	input, _ := json.Marshal(map[string]any{
		"action": name,
		"args":   json.RawMessage(argsJSON),
		"asset":  asset,
	})
	result, err := dispatch("execute_action", input)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(result, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CheckPolicy invokes the registered policy checker.
func (h *TestHost) CheckPolicy(tool string, args any) (action, resource string, err error) {
	argsJSON, _ := json.Marshal(args)
	input, _ := json.Marshal(map[string]any{
		"tool": tool,
		"args": json.RawMessage(argsJSON),
	})
	result, err := dispatch("check_policy", input)
	if err != nil {
		return "", "", err
	}
	var out struct {
		Action   string `json:"action"`
		Resource string `json:"resource"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		return "", "", err
	}
	return out.Action, out.Resource, nil
}

// Events returns all captured action events.
func (h *TestHost) Events() []TestEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]TestEvent(nil), h.events...)
}

// --- hostCaller implementation ---

func (h *TestHost) Log(level, msg string) {}

func (h *TestHost) AssetGetConfig(assetID int64) (json.RawMessage, error) {
	v, ok := h.assetConfigs[assetID]
	if !ok {
		return nil, fmt.Errorf("config not found for asset %d", assetID)
	}
	return v, nil
}

func (h *TestHost) FileDialog(params []byte) (string, error) {
	return "", fmt.Errorf("file dialog not available in test")
}

func (h *TestHost) KVGet(key string) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.kv[key]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (h *TestHost) KVSet(key string, value []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kv[key] = value
	return nil
}

func (h *TestHost) ActionEvent(eventType string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := TestEvent{Type: eventType, Data: data}
	h.events = append(h.events, e)
	if h.eventCb != nil {
		h.eventCb(e)
	}
}

// IOSetDeadline applies a deadline to a TCP mock handle registered via WithMockTCP.
// For non-TCP handles (HTTP mocks) it is a no-op returning nil.
func (h *TestHost) IOSetDeadline(handleID uint32, kind string, unixNanos int64) error {
	h.mockTCPMu.Lock()
	conn, ok := h.mockTCPConns[handleID]
	h.mockTCPMu.Unlock()
	if !ok {
		return nil
	}
	var t time.Time
	if unixNanos != 0 {
		t = time.Unix(0, unixNanos)
	}
	switch kind {
	case "read":
		return conn.SetReadDeadline(t)
	case "write":
		return conn.SetWriteDeadline(t)
	case "both":
		return conn.SetDeadline(t)
	default:
		return fmt.Errorf("unknown deadline kind: %q", kind)
	}
}

// ActionShouldStop returns the cancellation flag (default false; set via WithActionCancel).
func (h *TestHost) ActionShouldStop() bool {
	return h.actionStopped.Load()
}
