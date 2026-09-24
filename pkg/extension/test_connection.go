// pkg/extension/test_connection.go
package extension

import (
	"context"
	"crypto/tls"
	"encoding/json"
)

// AdHocAssetConfig carries the connection inputs for a call scoped to an
// asset that has no trustworthy row in the database: the asset form's "test
// connection" button calls a not-yet-saved asset, or a saved one being
// tested with unsaved edits — the values under test are exactly the ones the
// caller is about to save, or never will. Every host function that would
// otherwise read these off a stored asset reads them from here instead when
// a call's AssetRef carries one: endpoint fields (capHost.assetEndpoints),
// the dial path (AssetDialer via AdHocAssetDialer), and injected credentials
// (DefaultHostProvider.resolveAuth).
type AdHocAssetConfig struct {
	// Config is the guest-visible config: host reserved keys already
	// stripped, password fields already resolved to plaintext (or, absent
	// the extension's credentials:read capability, to the zero value) — the
	// same shape ctx.AssetConfig() would return for a saved asset.
	Config json.RawMessage
	// SSHTunnelID, ProxyChain, TLS mirror the host-owned connection settings
	// normally read off a saved asset's own SSHTunnelID column and its
	// Config's HostConnectionConfigKey. ProxyChain and TLS are nil when the
	// form leaves them unset.
	SSHTunnelID int64
	ProxyChain  json.RawMessage
	TLS         json.RawMessage
}

// AdHocAssetDialer is implemented by an AssetDialer that can also resolve a
// dial path from connection settings supplied directly, for a call scoped by
// an AdHocAssetConfig rather than a stored asset id.
//
// It is a separate interface from AssetDialer rather than an added method,
// so a fake that only ever dials a saved asset (most of pkg/extension's own
// tests) keeps compiling — see AGENTS.md "keep the boundary contract
// narrow". A production AssetDialer implements both.
//
// DialContextForConfig never returns a fingerprint: the resolved dial path
// is never cached (DefaultHostProvider.OpenIO always builds a fresh client
// for an ad-hoc call), since a test call's settings are one-shot.
type AdHocAssetDialer interface {
	DialContextForConfig(ctx context.Context, assetType string, adhoc *AdHocAssetConfig) (DialContextFunc, *tls.Config, error)
}
