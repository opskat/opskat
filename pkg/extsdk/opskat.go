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

// ToolHandler handles a tool invocation.
type ToolHandler func(ctx *ToolContext) (any, error)

// ActionHandler handles an action invocation.
type ActionHandler func(ctx *ActionContext) (any, error)

// PolicyChecker classifies a tool call into (action, resource).
type PolicyChecker func(tool string, args json.RawMessage) (action string, resource string)

// ConfigValidator validates asset configuration.
type ConfigValidator func(config json.RawMessage) []ValidationError

// ValidationError represents a config validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Global registries
var (
	tools           = map[string]ToolHandler{}
	actions         = map[string]ActionHandler{}
	policyChecker   PolicyChecker
	configValidator ConfigValidator
)

// RegisterTool registers a tool handler.
func RegisterTool(name string, handler ToolHandler) {
	tools[name] = handler
}

// RegisterAction registers an action handler.
func RegisterAction(name string, handler ActionHandler) {
	actions[name] = handler
}

// RegisterPolicy registers the policy checker.
func RegisterPolicy(checker PolicyChecker) {
	policyChecker = checker
}

// RegisterConfigValidator registers the config validator.
func RegisterConfigValidator(validator ConfigValidator) {
	configValidator = validator
}

// resetRegistries clears all registrations (for testing).
func resetRegistries() {
	tools = map[string]ToolHandler{}
	actions = map[string]ActionHandler{}
	policyChecker = nil
	configValidator = nil
}
