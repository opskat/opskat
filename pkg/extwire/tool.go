package extwire

import "time"

// Tool is one tool the extension contributes (KeyTools).
//
// A tool is exactly one of two things: a policy tool, reached through exec (and
// opsctl) on an asset and checked against the asset's permission groups — it
// declares PolicyAction or PolicyActions — or a page-only tool, called only by
// the extension's own pages — PageOnly. Neither, or both, is a registration error.
type Tool struct {
	Name       string         `json:"name"`
	I18n       I18nDesc       `json:"i18n"`
	Parameters map[string]any `json:"parameters"`
	// PolicyAction is the fixed action this tool requests; the asset's
	// permission groups are matched against it.
	PolicyAction string `json:"policyAction,omitempty"`
	// PolicyActions is, instead of PolicyAction, the set of actions a tool that
	// classifies each call from its arguments may answer check_policy with. A
	// policy tool declares exactly one of the two.
	PolicyActions []string `json:"policyActions,omitempty"`
	// PageOnly marks a tool only the extension's pages call: it has no policy
	// action, is left out of the help tool reference and exec refuses it.
	PageOnly bool `json:"pageOnly,omitempty"`
	// TimeoutMs is how long one call of this tool may run, in milliseconds; 0
	// means the host default. It holds for every caller alike.
	TimeoutMs int64 `json:"timeoutMs,omitempty"`
	// NoTimeout lets a page-only tool run without a call timeout; a policy tool
	// may not declare it.
	NoTimeout bool `json:"noTimeout,omitempty"`
	// Large names the string parameters that carry large content: opsctl may fill
	// them from a file (`--<name>-file <path>`, `-` for stdin); every other entry
	// point rejects that form.
	Large []string `json:"large,omitempty"`
}

// Timeout is the tool's declared call timeout, 0 when it leaves the host default.
func (t Tool) Timeout() time.Duration {
	return time.Duration(t.TimeoutMs) * time.Millisecond
}

// Actions is every policy action the tool can request.
func (t Tool) Actions() []string {
	if t.PolicyAction != "" {
		return []string{t.PolicyAction}
	}
	return t.PolicyActions
}
