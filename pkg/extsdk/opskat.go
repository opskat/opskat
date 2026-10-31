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
// a page calling a tool before it has an asset, or a configuration form tested
// before the asset it describes exists.
type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// ToolContext is passed to tool handlers.
//
// Events and ShouldStop serve every tool, whoever calls it: events reach the
// caller only when it is the extension's own page (AI exec and opsctl ignore
// them), and ShouldStop turns true once the page cancels the call by its
// invocation id. A long-running tool — typically a page-only one declaring
// NoTimeout — polls it and returns; the call then ends as canceled whatever it
// returns, and is cut off if it does not return within the host's grace period.
type ToolContext struct {
	Tool   string
	Args   json.RawMessage
	Asset  Asset
	Events *EventWriter
}

// AssetConfig returns the configuration of the asset this call runs against,
// with password fields decrypted when the extension declares
// permissions.credentials="read".
//
// It is the only way to read an asset config: the host serves the asset it
// scoped the call to, and only when that asset's type is one this extension
// registers. There is no by-id lookup — an extension cannot read another asset.
func (ctx *ToolContext) AssetConfig() (json.RawMessage, error) {
	return assetConfig(ctx.Asset, "tool "+ctx.Tool)
}

func assetConfig(a Asset, what string) (json.RawMessage, error) {
	if a.ID == 0 {
		return nil, fmt.Errorf("%s is not scoped to an asset, so it has no config to read", what)
	}
	return hostAssetGetConfig(a.ID)
}

// ShouldStop reports whether the caller has canceled this call. A long-running
// tool polls it and returns early, cleaning up first if it must (abort an
// upload); host IO it is blocked in fails as soon as the call is canceled.
func (ctx *ToolContext) ShouldStop() bool {
	return hostToolShouldStop()
}

// ConfigValidator validates asset configuration.
type ConfigValidator func(config json.RawMessage) []ValidationError

// ValidationError represents a config validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
