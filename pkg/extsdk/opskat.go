// Package opskat provides the Go SDK for building OpsKat WASM extensions.
package opskat

import "encoding/json"

// ToolContext is passed to tool handlers.
type ToolContext struct {
	Tool string
	Args json.RawMessage
}

// ActionContext is passed to action handlers.
type ActionContext struct {
	Action string
	Args   json.RawMessage
	Events *EventWriter
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
