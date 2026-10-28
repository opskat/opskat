// pkg/extension/host.go
package extension

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/url"
)

// HostProvider defines the capabilities that the host provides to extensions.
//
// Every method is stateless with respect to a single call: the runtime owns the
// per-invocation IO handle table and the action cancellation flag, so a provider
// never has to reason about which concurrent call it is serving.
type HostProvider interface {
	// OpenIO opens a stream. asset is the asset whose endpoint a network stream
	// targets — nil when the call has no asset, or when the target is not one of
	// its endpoints (the capability layer decides which against the call's
	// Scope, see capHost.OpenIO) — and only then does the stream use the asset's
	// cached client and carry its credentials. The way to dial (params.Dial,
	// params.TLS) is the capability layer's to set. The runtime registers the
	// returned resource in the calling invocation's handle table and closes it
	// when that call ends.
	OpenIO(ctx context.Context, asset *AssetRef, params IOOpenParams) (*IOResource, error)
	// GetAssetConfig returns the config of assetID, which is always the asset
	// the runtime scoped the current invocation to — never a guest-supplied id.
	// Implementations must still refuse an asset whose type the extension does
	// not register.
	GetAssetConfig(assetID int64) (json.RawMessage, error)
	FileDialog(dialogType string, opts DialogOptions) (string, error)
	Log(level, msg string)
	KVGet(key string) ([]byte, error)
	KVSet(key string, value []byte) error
	// ActionEvent forwards one event from a running action. invocationID names
	// the run it came from — without it a listener watching an extension with
	// two actions in flight cannot tell whose progress it is reading.
	ActionEvent(invocationID, eventType string, data json.RawMessage) error
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

	// RedirectGuard, when set, vets every redirect target of an http handle; a
	// non-nil error stops the redirect and fails the request. Only the host sets
	// it — it never crosses the WASM boundary.
	RedirectGuard func(target *url.URL) error `json:"-"`
	// Auth, when set, is the scope's credential injection, applied to every hop
	// of an http handle that targets one of the asset's endpoints. Only the host
	// sets it — it never crosses the WASM boundary.
	Auth *HTTPAuth `json:"-"`
	// Dial is the connection path the stream takes (nil = a direct dial), TLS
	// the settings a TCP stream or the asset's HTTP client applies (nil = none),
	// and Fingerprint keys the asset's cached HTTP client by the settings both
	// were built from. Only the host sets them — they never cross the WASM
	// boundary.
	Dial        DialContextFunc `json:"-"`
	TLS         *tls.Config     `json:"-"`
	Fingerprint string          `json:"-"`
}

// HTTPAuth pairs a scope's credential injection with the endpoint test that
// decides which request hops receive it.
type HTTPAuth struct {
	Credentials RequestAuth
	IsEndpoint  func(target *url.URL) bool
}

type DialogOptions struct {
	Title       string   `json:"title"`
	DefaultName string   `json:"defaultName"`
	Filters     []string `json:"filters"`
}
