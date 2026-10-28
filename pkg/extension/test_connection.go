// pkg/extension/test_connection.go
package extension

import (
	"encoding/json"
)

// AdHocAssetConfig carries the connection inputs for a call scoped to an
// asset that has no trustworthy row in the database: the asset form's "test
// connection" button calls a not-yet-saved asset, or a saved one being
// tested with unsaved edits — the values under test are exactly the ones the
// caller is about to save, or never will. The host's ScopeResolver reads the
// call's scope from here instead of a stored asset when a call's AssetRef
// carries one: its endpoint fields, its connection path and its injected
// credentials (from Credentials).
type AdHocAssetConfig struct {
	// Config is the guest-visible config: host reserved keys already
	// stripped, password fields already resolved to plaintext or, absent the
	// extension's credentials:read capability, left out.
	Config json.RawMessage
	// Credentials is the same config as host-side credential injection reads
	// it: every password field in plaintext, whatever the extension's
	// credentials capability — what a stored asset's decrypted config is to its
	// scope's Auth, for a call with no stored row. It never reaches the guest.
	Credentials json.RawMessage
	// SSHTunnelID, ProxyChain, TLS mirror the host-owned connection settings
	// normally read off a saved asset's own SSHTunnelID column and its
	// Config's HostConnectionConfigKey. ProxyChain and TLS are nil when the
	// form leaves them unset.
	SSHTunnelID int64
	ProxyChain  json.RawMessage
	TLS         json.RawMessage
}
