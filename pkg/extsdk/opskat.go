// Package opskat provides the Go SDK for building OpsKat WASM extensions.
package opskat

import (
	"encoding/json"
	"fmt"
)

// Asset is the asset a call runs against, named by the host rather than by the
// call's arguments: for a tool it is the `exec` target the host already resolved
// and checked the policy against. A tool must not declare an asset_id parameter —
// registration refuses it — and reads this instead.
//
// The zero value means the call was not scoped to an asset, which is a real case:
// the frontend runs an action to test a configuration form before the asset it
// describes exists.
type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// ToolContext is passed to tool handlers.
type ToolContext struct {
	Tool  string
	Args  json.RawMessage
	Asset Asset
}

// AssetConfig returns the configuration of the asset this call runs against,
// with password fields decrypted when the extension declares
// capabilities.credentials="read".
func (ctx *ToolContext) AssetConfig() (json.RawMessage, error) {
	return assetConfig(ctx.Asset, "tool "+ctx.Tool)
}

// ActionContext is passed to action handlers.
type ActionContext struct {
	Action string
	Args   json.RawMessage
	Asset  Asset
	Events *EventWriter
}

// AssetConfig returns the configuration of the asset this action runs against.
func (ctx *ActionContext) AssetConfig() (json.RawMessage, error) {
	return assetConfig(ctx.Asset, "action "+ctx.Action)
}

func assetConfig(a Asset, what string) (json.RawMessage, error) {
	if a.ID == 0 {
		return nil, fmt.Errorf("%s is not scoped to an asset, so it has no config to read", what)
	}
	return hostAssetGetConfig(a.ID)
}

// ShouldStop returns true if the caller has requested cancellation.
// Long-running actions should poll this periodically and exit cleanly
// (e.g. send an "ended" event with reason="userStop").
func (ctx *ActionContext) ShouldStop() bool {
	return hostActionShouldStop()
}

// ActionHandler handles an action invocation.
type ActionHandler func(ctx *ActionContext) (any, error)

// ConfigValidator validates asset configuration.
type ConfigValidator func(config json.RawMessage) []ValidationError

// ValidationError represents a config validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
