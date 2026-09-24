package opskat

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// The registration API is the single place an extension declares what it can do.
//
// Everything the host needs to know about a tool — its name, the shape of its
// arguments, the policy action it requests, the sentence shown to the model —
// is fixed by one call:
//
//	opskat.Tool("list_objects", handler).Policy("list").Doc("tools.list_objects.description")
//
// or, when the action depends on the arguments, .PolicyFunc(actions, classify).
//
// describe() is generated from these registries, so a declaration cannot drift
// from the handler that serves it: there is no second list to update. The
// parameter schema is reflected from the handler's own argument type, which is
// also the type the handler receives — a renamed field changes both at once.

// Meta is the extension's presentation and policy identity.
// DisplayName / Description are i18n keys resolved against the extension's
// locales/ directory (a key with no entry is shown as-is).
type Meta struct {
	Icon        string
	DisplayName string
	Description string
	// PolicyType names the policy face the extension's asset types are checked
	// under. Required: without it the host has no policy surface to attach the
	// extension's permission groups to.
	PolicyType string
}

// Page is one frontend page the extension contributes.
// Slot "asset.connect" makes the page what opening the asset shows.
type Page struct {
	ID        string
	Slot      string
	Name      string // i18n key
	Component string // exported symbol in the entry module
}

// FrontendSpec declares the extension's UI bundle and its pages.
type FrontendSpec struct {
	Entry  string
	Styles string
	Pages  []Page
}

// Seed is a read-only snippet shipped with the extension.
type Seed struct {
	Key         string
	Name        string
	Category    string
	Content     string
	Description string
}

type toolEntry struct {
	name     string
	doc      string
	action   string
	schema   map[string]any
	invoke   func(ctx *ToolContext) (any, error)
	resource func(args json.RawMessage) string
	// actions and classify are set by PolicyFunc, in place of action / resource.
	actions  []string
	classify func(args json.RawMessage) (action, resource string, err error)
	// timeout is the tool's own call timeout; 0 leaves the host default.
	timeout time.Duration
}

type assetTypeEntry struct {
	typ        string
	name       string
	schema     map[string]any
	connection *Connection
	auth       *Auth
	// testConnection runs the asset form's "Test connection" handler against a
	// decoded config; nil means the type declares none, and describe() reports
	// that so the host never shows the button. Stored as a raw-JSON-in closure
	// (like a tool's invoke) rather than the typed func the caller wrote so the
	// entry stays usable from untyped dispatch code (handler.go), the same
	// reason toolEntry.invoke exists.
	testConnection func(configJSON json.RawMessage) error
}

type policyGroupEntry struct {
	id          string
	name        string
	description string
	allow       []string
	deny        []string
	isDefault   bool
}

type snippetCategoryEntry struct {
	id        string
	assetType string
	name      string
}

var (
	meta            Meta
	tools           = map[string]*toolEntry{}
	toolOrder       []string
	assetTypes      []*assetTypeEntry
	policyGroups    []*policyGroupEntry
	frontendSpec    FrontendSpec
	snippetCats     []snippetCategoryEntry
	snippetSeeds    []Seed
	actions         = map[string]ActionHandler{}
	configValidator ConfigValidator
)

// Extension declares the extension's presentation metadata and policy face.
func Extension(m Meta) { meta = m }

// Frontend declares the extension's UI bundle and pages.
func Frontend(spec FrontendSpec) { frontendSpec = spec }

// SnippetCategory declares a snippet category this extension owns.
// name is an i18n key; assetType must be one of the extension's asset types.
func SnippetCategory(id, assetType, name string) {
	snippetCats = append(snippetCats, snippetCategoryEntry{id: id, assetType: assetType, name: name})
}

// SnippetSeed ships a read-only snippet with the extension.
func SnippetSeed(s Seed) { snippetSeeds = append(snippetSeeds, s) }

// ToolReg is the registration handle returned by Tool. Its methods complete the
// declaration; they all mutate the entry that dispatch and describe already share.
type ToolReg[T any] struct{ e *toolEntry }

// injectedAssetParam is the parameter name a tool may not declare: the asset a
// tool runs against is the `exec` target the host puts in the call envelope, and
// the handler reads it from ctx.Asset. Accepting it as a flag too would give the
// caller a second asset to name — one no policy check ever saw.
const injectedAssetParam = "asset_id"

// Tool registers a tool and reflects its parameter schema from T.
//
// T must be a struct whose fields the exec flag DSL can express (string, bool,
// integer, number, []string). Anything else panics at registration — that is
// init() time in the guest, so a schema the host could not use fails the whole
// extension at load instead of the first time a model calls the tool.
func Tool[T any](name string, handler func(ctx *ToolContext, args T) (any, error)) *ToolReg[T] {
	if name == "" {
		panic("opskat: tool name is required")
	}
	if _, dup := tools[name]; dup {
		panic(fmt.Sprintf("opskat: tool %q is already registered", name))
	}
	schema := reflectSchema(reflect.TypeFor[T](), fmt.Sprintf("tool %q arguments", name), schemaModeParams)
	if _, declared := schema["properties"].(map[string]any)[injectedAssetParam]; declared {
		panic(fmt.Sprintf("opskat: tool %q declares parameter %q — the asset a tool runs against is the exec target, injected by the host; read ctx.Asset instead", name, injectedAssetParam))
	}
	entry := &toolEntry{
		name:   name,
		schema: schema,
		invoke: func(ctx *ToolContext) (any, error) {
			args, err := decodeArgs[T](ctx.Args)
			if err != nil {
				return nil, fmt.Errorf("tool %s: %w", name, err)
			}
			return handler(ctx, args)
		},
	}
	tools[name] = entry
	toolOrder = append(toolOrder, name)
	return &ToolReg[T]{e: entry}
}

// Policy declares which policy action this tool requests. The host matches it
// against the user's permission groups before the tool runs; every tool needs
// either this or PolicyFunc.
func (r *ToolReg[T]) Policy(action string) *ToolReg[T] {
	if r.e.classify != nil {
		panic(fmt.Sprintf("opskat: tool %q already classifies its calls with PolicyFunc; Policy and PolicyFunc are exclusive", r.e.name))
	}
	r.e.action = action
	return r
}

// PolicyFunc classifies each call from its arguments: fn returns the policy action
// the call requests and the resource it touches (any string, may be empty). The
// host matches the pair against rules ext:<PolicyType>:<action>[:<resource-glob>].
//
// actions is every action fn can return. It is what describe() declares, so it is
// the set the user writes rules and permission groups against; the host treats
// any other action fn returns as a defect and asks the user about the call. Arguments
// that do not decode into T fail the policy check rather than being classified.
//
// It is the general form of Policy + Resource and replaces both on a tool: use it
// when the action depends on the arguments (one request tool that reads or
// writes), or to derive action and resource in one place. A tool whose action is
// fixed may equally keep Policy, plus Resource when it reports a resource.
func (r *ToolReg[T]) PolicyFunc(actions []string, fn func(args T) (action, resource string)) *ToolReg[T] {
	if len(actions) == 0 {
		panic(fmt.Sprintf("opskat: tool %q: PolicyFunc needs the set of actions it can return", r.e.name))
	}
	if r.e.action != "" || r.e.resource != nil {
		panic(fmt.Sprintf("opskat: tool %q already declares Policy/Resource; PolicyFunc replaces both", r.e.name))
	}
	r.e.actions = append([]string(nil), actions...)
	r.e.classify = func(raw json.RawMessage) (string, string, error) {
		args, err := decodeArgs[T](raw)
		if err != nil {
			return "", "", fmt.Errorf("tool %s: %w", r.e.name, err)
		}
		action, resource := fn(args)
		return action, resource, nil
	}
	return r
}

// maxToolTimeout mirrors the host's ceiling (pkg/extension MaxToolTimeout): a
// tool call holds one of the extension's few instance slots while it runs, so
// work that needs longer belongs in an action.
const maxToolTimeout = 10 * time.Minute

// Timeout sets how long one call of this tool may run before the host stops it,
// replacing the host default of 30 seconds. d must be at least a millisecond and
// at most 10 minutes; anything else panics at registration, so the extension
// fails at load instead of at its first slow call.
func (r *ToolReg[T]) Timeout(d time.Duration) *ToolReg[T] {
	if d < time.Millisecond || d > maxToolTimeout {
		panic(fmt.Sprintf("opskat: tool %q: timeout %s is outside [1ms, %s]", r.e.name, d, maxToolTimeout))
	}
	r.e.timeout = d
	return r
}

// Doc sets the tool description shown to the model (an i18n key).
func (r *ToolReg[T]) Doc(description string) *ToolReg[T] {
	r.e.doc = description
	return r
}

// Resource derives the resource string reported alongside the fixed policy action.
func (r *ToolReg[T]) Resource(fn func(args T) string) *ToolReg[T] {
	if r.e.classify != nil {
		panic(fmt.Sprintf("opskat: tool %q already classifies its calls with PolicyFunc, which returns the resource", r.e.name))
	}
	r.e.resource = func(raw json.RawMessage) string {
		args, err := decodeArgs[T](raw)
		if err != nil {
			return ""
		}
		return fn(args)
	}
	return r
}

// AssetTypeReg is the registration handle returned by AssetType. C is the
// type's config struct — carried on the handle (not just at AssetType's call
// site) so TestConnection can decode a call's config into the same struct
// its schema was reflected from, instead of asking the caller to redeclare it.
type AssetTypeReg[C any] struct{ e *assetTypeEntry }

// AssetType registers an asset type whose configuration form is reflected from C.
//
// Field tags drive the form: `title` / `placeholder` / `desc` are i18n keys,
// `format:"password"` marks a secret the host encrypts, `format:"endpoint"` marks
// a URL or host:port the extension may connect to when it declares the
// network.assetEndpoint capability, `enum:"a,b"` renders a select. A field
// without `,omitempty` is required.
func AssetType[C any](typ string) *AssetTypeReg[C] {
	if typ == "" {
		panic("opskat: asset type is required")
	}
	entry := &assetTypeEntry{
		typ:    typ,
		name:   typ,
		schema: reflectSchema(reflect.TypeFor[C](), fmt.Sprintf("asset type %q config", typ), schemaModeConfig),
	}
	assetTypes = append(assetTypes, entry)
	return &AssetTypeReg[C]{e: entry}
}

// Name sets the asset type's display name (an i18n key).
func (r *AssetTypeReg[C]) Name(name string) *AssetTypeReg[C] {
	r.e.name = name
	return r
}

// Connection names the host-owned connection settings the asset type supports.
//
// The host owns these settings: it shows a declared item in the asset form and
// detail card and applies it whenever the extension opens a connection to the
// asset's endpoint. The extension never reads or handles them — they are not
// part of the config struct. An item left out is neither shown nor applied.
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

// Connection declares the host-owned connection settings the asset type supports.
func (r *AssetTypeReg[C]) Connection(c Connection) *AssetTypeReg[C] {
	r.e.connection = &c
	return r
}

// Auth declares the credentials the host injects into the extension's HTTP
// requests to the asset's endpoint. The host renders each binding's Value from
// the asset's config — decrypting format:"password" fields itself — so the
// guest authenticates without ever holding the plaintext, and without the
// credentials:read capability. Requests to any other target get nothing.
//
// Selector names the config field whose value picks the active group (a value
// no group names injects nothing); leave it empty to declare a single,
// always-active group. The host refuses the extension at load when a template
// references a field the config does not declare.
type Auth struct {
	Selector string      `json:"selector,omitempty"`
	Groups   []AuthGroup `json:"groups"`
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

// Auth declares the credentials the host injects into requests to the asset's endpoint.
func (r *AssetTypeReg[C]) Auth(a Auth) *AssetTypeReg[C] {
	r.e.auth = &a
	return r
}

// TestConnection declares the handler behind the asset form's "Test
// connection" button: describe() reports it, so the host shows the button
// only when this is called, and calls back into fn with the submitted config
// decoded into C — the same struct AssetType reflected the form from.
//
// fn reaches the endpoint the same way a tool does (Dial, an *http.Client
// built on NewHTTPTransport, …), scoped by the host to the same
// network.assetEndpoint / connection settings a real call would use, except
// resolved from the form's current values rather than a saved asset: the
// values under test are exactly the ones the caller is about to save, or
// never will. A nil error means the connection succeeded.
func (r *AssetTypeReg[C]) TestConnection(fn func(cfg C) error) *AssetTypeReg[C] {
	r.e.testConnection = func(configJSON json.RawMessage) error {
		cfg, err := decodeArgs[C](configJSON)
		if err != nil {
			return fmt.Errorf("test connection: %w", err)
		}
		return fn(cfg)
	}
	return r
}

// PolicyGroupReg is the registration handle returned by PolicyGroup.
type PolicyGroupReg struct{ e *policyGroupEntry }

// PolicyGroup registers a permission group the user can grant on an asset.
// id must be namespaced by the Meta.PolicyType: ext:<PolicyType>:<group>.
func PolicyGroup(id string) *PolicyGroupReg {
	entry := &policyGroupEntry{id: id, name: id}
	policyGroups = append(policyGroups, entry)
	return &PolicyGroupReg{e: entry}
}

// Name sets the group's display name (an i18n key).
func (r *PolicyGroupReg) Name(name string) *PolicyGroupReg {
	r.e.name = name
	return r
}

// Description sets the group's description (an i18n key).
func (r *PolicyGroupReg) Description(description string) *PolicyGroupReg {
	r.e.description = description
	return r
}

// Allow adds actions this group permits without asking.
func (r *PolicyGroupReg) Allow(actions ...string) *PolicyGroupReg {
	r.e.allow = append(r.e.allow, actions...)
	return r
}

// Deny adds actions this group refuses.
func (r *PolicyGroupReg) Deny(actions ...string) *PolicyGroupReg {
	r.e.deny = append(r.e.deny, actions...)
	return r
}

// Default marks the group as applied to a new asset of this extension's types.
func (r *PolicyGroupReg) Default() *PolicyGroupReg {
	r.e.isDefault = true
	return r
}

// RegisterAction registers an action handler. Actions are driven by the
// extension's own UI, not by the model, so they carry no schema or policy action.
func RegisterAction(name string, handler ActionHandler) {
	actions[name] = handler
}

// RegisterConfigValidator registers the config validator.
func RegisterConfigValidator(validator ConfigValidator) {
	configValidator = validator
}

func decodeArgs[T any](raw json.RawMessage) (T, error) {
	var args T
	if len(raw) == 0 {
		return args, nil
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return args, fmt.Errorf("parse arguments: %w", err)
	}
	return args, nil
}

// resetRegistries clears all registrations (for testing).
func resetRegistries() {
	meta = Meta{}
	tools = map[string]*toolEntry{}
	toolOrder = nil
	assetTypes = nil
	policyGroups = nil
	frontendSpec = FrontendSpec{}
	snippetCats = nil
	snippetSeeds = nil
	actions = map[string]ActionHandler{}
	configValidator = nil
}
