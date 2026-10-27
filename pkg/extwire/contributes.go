package extwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// I18nName is an i18n key naming one item.
type I18nName struct {
	Name string `json:"name"`
}

// I18nNameDesc is the i18n keys naming and describing one item.
type I18nNameDesc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// I18nDesc is the i18n key describing one item.
type I18nDesc struct {
	Description string `json:"description"`
}

// AssetType is one asset type the extension contributes (KeyAssetTypes).
type AssetType struct {
	Type         string         `json:"type"`
	I18n         I18nName       `json:"i18n"`
	ConfigSchema map[string]any `json:"configSchema"`
	Connection   *Connection    `json:"connection,omitempty"`
	Auth         *Auth          `json:"auth,omitempty"`
	// TestConnection reports whether the type registered a test-connection
	// handler. The asset form shows its "Test connection" button only when this
	// is true; the host dispatches the call itself, never through policy —
	// testing a connection is not an operation on the asset.
	TestConnection bool `json:"testConnection,omitempty"`
}

// Connection is the subset of the host-owned connection settings an asset type
// supports. The host renders a declared item in the asset form and detail card
// and applies it when dialing the asset's endpoint; an undeclared item is
// neither shown nor applied. The extension never reads these settings.
type Connection struct {
	// SSHTunnel routes connections to the endpoint through an SSH asset the user
	// picks on the asset.
	SSHTunnel bool `json:"sshTunnel,omitempty"`
	// ProxyChain routes connections through the asset's proxy chain.
	ProxyChain bool `json:"proxyChain,omitempty"`
	// TLS applies the asset's TLS settings (verification, server name, CA,
	// client certificate) to connections.
	TLS bool `json:"tls,omitempty"`
}

// UnmarshalJSON refuses an item the host does not own: silently dropping it
// would load an extension whose author believes a setting is applied when it is
// not.
func (c *Connection) UnmarshalJSON(data []byte) error {
	type plain Connection
	var v plain
	if err := decodeStrict(data, &v); err != nil {
		return fmt.Errorf("connection (supported items: sshTunnel, proxyChain, tls): %w", err)
	}
	*c = Connection(v)
	return nil
}

// Auth is the credential injection an asset type declares: the host renders its
// bindings from the asset's config — password fields decrypted host-side — and
// injects them into every HTTP request the extension sends to that asset's
// endpoint. The plaintext never crosses into the guest.
//
// Selector names the config field whose value picks the active group (e.g.
// authType); a value no group names means "no credentials". Without a selector
// there is exactly one group, always active.
type Auth struct {
	Selector string      `json:"selector,omitempty"`
	Groups   []AuthGroup `json:"groups"`
}

// UnmarshalJSON refuses a key the host does not read: a mistyped "name" left
// unread would load an extension that injects nothing where its author expects
// a credential.
func (a *Auth) UnmarshalJSON(data []byte) error {
	type plain Auth
	var v plain
	if err := decodeStrict(data, &v); err != nil {
		return fmt.Errorf("auth (keys: selector, groups[].when, groups[].bindings[].in/name/value): %w", err)
	}
	*a = Auth(v)
	return nil
}

// AuthGroup is the set of bindings injected when the selector field equals When.
type AuthGroup struct {
	When     string        `json:"when,omitempty"`
	Bindings []AuthBinding `json:"bindings"`
}

// AuthBinding injects one value. In is "header" (Name = header name), "query"
// (Name = parameter name) or "basic" (no Name; Value renders "user:password"
// and is sent as Authorization: Basic). Value is a template: literal text with
// {{field}} and {{base64(part, ...)}} placeholders, each part a config field or
// a double-quoted literal — e.g. `ApiKey {{base64(apiKeyId, ":", apiKey)}}`.
type AuthBinding struct {
	In    string `json:"in"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value"`
}

// Tool is one tool the extension contributes (KeyTools).
type Tool struct {
	Name       string         `json:"name"`
	I18n       I18nDesc       `json:"i18n"`
	Parameters map[string]any `json:"parameters"`
	// PolicyAction is the fixed action this tool requests; the asset's
	// permission groups are matched against it.
	PolicyAction string `json:"policyAction,omitempty"`
	// PolicyActions is, instead of PolicyAction, the set of actions a tool that
	// classifies each call from its arguments may answer check_policy with. A
	// tool declares exactly one of the two.
	PolicyActions []string `json:"policyActions,omitempty"`
	// TimeoutMs is how long one call of this tool may run, in milliseconds; 0
	// means the host default. It holds for every caller alike.
	TimeoutMs int64 `json:"timeoutMs,omitempty"`
	// FileParams names the string parameters opsctl may fill from a file
	// (`--<name>-file <path>`); every other entry point rejects that form.
	FileParams []string `json:"fileParams,omitempty"`
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

// Policy is the extension's policy face (KeyPolicy): the type its asset types
// are checked under, its permission groups and the ones a new asset starts
// with. The action set is not on the wire: it is exactly the set of actions the
// tools request, and the host derives it from them.
type Policy struct {
	Type    string        `json:"type"`
	Groups  []PolicyGroup `json:"groups,omitempty"`
	Default []string      `json:"default,omitempty"`
}

// PolicyGroup is one permission group, ID namespaced ext:<policy type>:<name>.
type PolicyGroup struct {
	ID     string         `json:"id"`
	I18n   I18nNameDesc   `json:"i18n"`
	Policy map[string]any `json:"policy"`
}

// Snippets is the snippet categories and seed snippets the extension
// contributes (KeySnippets).
type Snippets struct {
	Categories []SnippetCategory `json:"categories"`
	Seed       []SeedSnippet     `json:"seed"`
}

// SnippetCategory is a snippet category id the extension owns, shown for one of
// its asset types.
type SnippetCategory struct {
	ID        string   `json:"id"`
	AssetType string   `json:"assetType"`
	I18n      I18nName `json:"i18n"`
}

// SeedSnippet is a read-only snippet shipped with the extension, in a builtin
// category or one the extension declares.
type SeedSnippet struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Content     string `json:"content"`
	Description string `json:"description"`
}

// Pages is the extension's UI bundle and the pages it contributes (KeyPages).
type Pages struct {
	Entry  string `json:"entry"`
	Styles string `json:"styles"`
	Pages  []Page `json:"pages"`
}

// Page is one frontend page. Slot "asset.connect" makes the page what opening
// the asset shows; Component is the exported symbol in the entry module.
type Page struct {
	ID        string   `json:"id"`
	Slot      string   `json:"slot,omitempty"`
	I18n      I18nName `json:"i18n"`
	Component string   `json:"component"`
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
