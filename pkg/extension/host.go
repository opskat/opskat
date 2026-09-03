// pkg/extension/host.go
package extension

import "encoding/json"

// HostProvider defines the capabilities that the host provides to extensions.
// Main App and DevServer each provide their own implementation.
//
// Every method is stateless with respect to a single call: the runtime owns the
// per-invocation IO handle table and the action cancellation flag, so a provider
// never has to reason about which concurrent call it is serving.
type HostProvider interface {
	// OpenIO opens a stream. The runtime registers the returned resource in the
	// calling invocation's handle table and closes it when that call ends.
	OpenIO(params IOOpenParams) (*IOResource, error)
	GetAssetConfig(assetID int64) (json.RawMessage, error)
	FileDialog(dialogType string, opts DialogOptions) (string, error)
	Log(level, msg string)
	KVGet(key string) ([]byte, error)
	KVSet(key string, value []byte) error
	ActionEvent(eventType string, data json.RawMessage) error
}

type IOOpenParams struct {
	Type         string            `json:"type"`
	Path         string            `json:"path"`
	Mode         string            `json:"mode"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers"`
	AllowPrivate bool              `json:"allowPrivate"` // dial-time guard: allow connections to private/loopback IPs
	// tcp (new)
	Addr    string `json:"addr,omitempty"`
	Timeout int    `json:"timeout,omitempty"` // ms; 0 = default 10s
}

type DialogOptions struct {
	Title       string   `json:"title"`
	DefaultName string   `json:"defaultName"`
	Filters     []string `json:"filters"`
}
