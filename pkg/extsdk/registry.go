package opskat

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// The registration API is the single place an extension declares what it can do.
//
// Everything the host needs to know about a tool — its name, the shape of its
// arguments, the policy action it requests, the sentence shown to the model —
// is fixed by one call:
//
//	opskat.Tool("list_objects", handler).Policy("list").Doc("tools.list_objects.description")
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
}

type assetTypeEntry struct {
	typ        string
	name       string
	schema     map[string]any
	proxyChain bool
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
	entry := &toolEntry{
		name:   name,
		schema: reflectSchema(reflect.TypeFor[T](), fmt.Sprintf("tool %q arguments", name), schemaModeParams),
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
// against the user's permission groups before the tool runs; every tool needs one.
func (r *ToolReg[T]) Policy(action string) *ToolReg[T] {
	r.e.action = action
	return r
}

// Doc sets the tool description shown to the model (an i18n key).
func (r *ToolReg[T]) Doc(description string) *ToolReg[T] {
	r.e.doc = description
	return r
}

// Resource derives the resource string reported alongside the policy action.
func (r *ToolReg[T]) Resource(fn func(args T) string) *ToolReg[T] {
	r.e.resource = func(raw json.RawMessage) string {
		args, err := decodeArgs[T](raw)
		if err != nil {
			return ""
		}
		return fn(args)
	}
	return r
}

// AssetTypeReg is the registration handle returned by AssetType.
type AssetTypeReg struct{ e *assetTypeEntry }

// AssetType registers an asset type whose configuration form is reflected from C.
//
// Field tags drive the form: `title` / `placeholder` / `desc` are i18n keys,
// `format:"password"` marks a secret the host encrypts, `enum:"a,b"` renders a
// select. A field without `,omitempty` is required.
func AssetType[C any](typ string) *AssetTypeReg {
	if typ == "" {
		panic("opskat: asset type is required")
	}
	entry := &assetTypeEntry{
		typ:    typ,
		name:   typ,
		schema: reflectSchema(reflect.TypeFor[C](), fmt.Sprintf("asset type %q config", typ), schemaModeConfig),
	}
	assetTypes = append(assetTypes, entry)
	return &AssetTypeReg{e: entry}
}

// Name sets the asset type's display name (an i18n key).
func (r *AssetTypeReg) Name(name string) *AssetTypeReg {
	r.e.name = name
	return r
}

// ProxyChain opts the asset type into the host's SSH proxy chain.
func (r *AssetTypeReg) ProxyChain() *AssetTypeReg {
	r.e.proxyChain = true
	return r
}

// PolicyGroupReg is the registration handle returned by PolicyGroup.
type PolicyGroupReg struct{ e *policyGroupEntry }

// PolicyGroup registers a permission group the user can grant on an asset.
// id must be namespaced as ext:<extension>:<group>.
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
