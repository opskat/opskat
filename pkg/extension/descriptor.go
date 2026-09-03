package extension

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Descriptor is what the guest answers to describe(): everything an extension can
// do, derived inside the guest from the registration calls themselves.
//
// It exists because these declarations used to live in manifest.json, next to the
// capability grants, in a second declaration language that nothing checked against
// the code: a tool was spelled out once in the manifest's JSON-Schema, once in the
// guest's RegisterTool call and once more in its check_policy switch, and the three
// only met at runtime, as "unknown tool". The manifest keeps what a user must be
// able to audit *before* running the code — the capability grants — and the rest is
// read back from the code that implements it.
//
// The wire shape mirrors pkg/extsdk/describe.go; the field names are the ones the
// merged Manifest already exposes, so the frontend contract is unchanged.
type Descriptor struct {
	Icon       string         `json:"icon"`
	I18n       ManifestI18n   `json:"i18n"`
	AssetTypes []AssetTypeDef `json:"assetTypes"`
	Tools      []ToolDef      `json:"tools"`
	Policies   PoliciesDef    `json:"policies"`
	Frontend   FrontendDef    `json:"frontend"`
	Snippets   SnippetsDef    `json:"snippets"`
}

// ParseDescriptor decodes and validates a describe() payload.
//
// The guest is on the far side of a WASM boundary, so its answer is checked here
// even though the SDK builds it from typed registrations: a hand-rolled guest, or
// a corrupted cache entry, must fail at load rather than halfway through a tool call.
func ParseDescriptor(data []byte) (*Descriptor, error) {
	var d Descriptor
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse describe() result: %w", err)
	}
	if err := d.validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

func (d *Descriptor) validate() error {
	if err := d.validateAssetScope(); err != nil {
		return err
	}
	if err := d.validateTools(); err != nil {
		return err
	}
	if err := d.validatePolicyGroups(); err != nil {
		return err
	}
	return d.validateSnippets()
}

// validateAssetScope enforces "an extension belongs to an asset type".
//
// There used to be a class of asset-less extension: one that declared no asset type
// was loaded anyway, and its tools were reachable only through a dedicated dispatch
// tool whose sole act was a non-reusable confirmation prompt — no policy, no grant,
// no approval surface. Since that class was retired, extension tools travel the same
// exec / help / policy / grant path built-in types do, and the entry to that path is
// an *asset*. A missing declaration is therefore not "an optional field left out"
// but "this extension has no reachable entry point", and must be said at load time.
func (d *Descriptor) validateAssetScope() error {
	if len(d.AssetTypes) == 0 {
		return fmt.Errorf("describe(): assetTypes must declare at least one asset type — extension tools are reached through exec on an asset")
	}
	seen := make(map[string]struct{}, len(d.AssetTypes))
	for i, at := range d.AssetTypes {
		if at.Type == "" {
			return fmt.Errorf("describe(): assetTypes[%d].type is required", i)
		}
		if !nameRe.MatchString(at.Type) {
			return fmt.Errorf("describe(): assetTypes[%d].type must match %s (got %q)", i, nameRe.String(), at.Type)
		}
		if _, dup := seen[at.Type]; dup {
			return fmt.Errorf("describe(): duplicate asset type %q", at.Type)
		}
		seen[at.Type] = struct{}{}
		if len(ConfigSchemaProperties(at.ConfigSchema)) == 0 {
			return fmt.Errorf("describe(): assetTypes[%q].configSchema must declare properties", at.Type)
		}
	}
	if d.Policies.Type == "" {
		return fmt.Errorf("describe(): policies.type is required — it names the policy face the extension's asset types are checked under")
	}
	return nil
}

// supportedParamTypes is what the exec flag DSL can express. object is not
// supported: a nested value goes through the command's --json escape hatch rather
// than inventing a nested flag syntax.
var supportedParamTypes = map[string]bool{
	"string": true, "integer": true, "number": true, "boolean": true, "array": true,
}

// validateTools checks the parameter schemas the flag DSL parses against.
//
// The SDK reflects these from the handler's own argument type, so a compliant guest
// cannot produce a schema this rejects — which is the point: what used to be a
// hand-written manifest block that only the parser read is now generated, and this
// check is the boundary that keeps a non-SDK guest honest.
func (d *Descriptor) validateTools() error {
	seen := make(map[string]bool, len(d.Tools))
	for i, t := range d.Tools {
		if t.Name == "" {
			return fmt.Errorf("describe(): tools[%d].name is required", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("describe(): duplicate tool name %q", t.Name)
		}
		seen[t.Name] = true
		if t.PolicyAction == "" {
			return fmt.Errorf("describe(): tools[%q] declares no policy action — every tool is checked against the asset's permission groups", t.Name)
		}
		if t.Parameters == nil {
			return fmt.Errorf("describe(): tools[%q].parameters is required (an object schema, empty properties for a no-arg tool)", t.Name)
		}
		if typ, _ := t.Parameters["type"].(string); typ != "object" {
			return fmt.Errorf("describe(): tools[%q].parameters.type must be \"object\", got %q", t.Name, typ)
		}
		props, ok := t.Parameters["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("describe(): tools[%q].parameters.properties must be an object", t.Name)
		}
		for name, raw := range props {
			prop, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("describe(): tools[%q].parameters.properties.%s must be an object", t.Name, name)
			}
			typ, _ := prop["type"].(string)
			if typ == "" {
				return fmt.Errorf("describe(): tools[%q].parameters.properties.%s has no type", t.Name, name)
			}
			if !supportedParamTypes[typ] {
				return fmt.Errorf("describe(): tools[%q].parameters.properties.%s has unsupported type %q (supported: string, integer, number, boolean, array)", t.Name, name, typ)
			}
			if typ == "array" {
				items, ok := prop["items"].(map[string]any)
				if !ok {
					return fmt.Errorf("describe(): tools[%q].parameters.properties.%s is an array without an items object", t.Name, name)
				}
				if it, _ := items["type"].(string); it != "string" {
					return fmt.Errorf("describe(): tools[%q].parameters.properties.%s: only array<string> is supported, got array<%s>", t.Name, name, it)
				}
			}
		}
		if rawReq, exists := t.Parameters["required"]; exists {
			req, ok := rawReq.([]any)
			if !ok {
				return fmt.Errorf("describe(): tools[%q].parameters.required must be an array, got %T", t.Name, rawReq)
			}
			for _, r := range req {
				name, _ := r.(string)
				if _, exists := props[name]; !exists {
					return fmt.Errorf("describe(): tools[%q].parameters.required references undeclared property %q", t.Name, name)
				}
			}
		}
	}
	return nil
}

func (d *Descriptor) validatePolicyGroups() error {
	for _, g := range d.Policies.Groups {
		if !strings.HasPrefix(g.ID, "ext:") {
			return fmt.Errorf("describe(): policy group ID must start with ext: (got %q)", g.ID)
		}
		if !policyIDRe.MatchString(g.ID) {
			return fmt.Errorf("describe(): policy group ID has invalid characters (got %q)", g.ID)
		}
	}
	return nil
}

// validateSnippets validates the snippets.categories and snippets.seed blocks.
// Both are optional; an empty block passes.
func (d *Descriptor) validateSnippets() error {
	assetTypeSet := make(map[string]struct{}, len(d.AssetTypes))
	for _, at := range d.AssetTypes {
		assetTypeSet[at.Type] = struct{}{}
	}

	catIDs := make(map[string]struct{}, len(d.Snippets.Categories))
	for _, c := range d.Snippets.Categories {
		if c.ID == "" {
			return fmt.Errorf("describe(): snippets.categories[].id is required")
		}
		if !snippetCatIDRe.MatchString(c.ID) {
			return fmt.Errorf("describe(): snippets.categories[].id must match %s (got %q)", snippetCatIDRe.String(), c.ID)
		}
		if IsBuiltinSnippetCategoryID(c.ID) {
			return fmt.Errorf("describe(): snippets.categories[].id %q collides with a builtin category", c.ID)
		}
		if _, dup := catIDs[c.ID]; dup {
			return fmt.Errorf("describe(): duplicate snippets.categories[].id %q", c.ID)
		}
		catIDs[c.ID] = struct{}{}
		if c.AssetType == "" {
			return fmt.Errorf("describe(): snippets.categories[%q].assetType is required", c.ID)
		}
		if _, ok := assetTypeSet[c.AssetType]; !ok {
			return fmt.Errorf("describe(): snippets.categories[%q].assetType %q is not one of this extension's asset types", c.ID, c.AssetType)
		}
	}

	seedKeys := make(map[string]struct{}, len(d.Snippets.Seed))
	for _, s := range d.Snippets.Seed {
		if s.Key == "" {
			return fmt.Errorf("describe(): snippets.seed[].key is required")
		}
		if !snippetSeedKeyRe.MatchString(s.Key) {
			return fmt.Errorf("describe(): snippets.seed[].key must match %s (got %q)", snippetSeedKeyRe.String(), s.Key)
		}
		if _, dup := seedKeys[s.Key]; dup {
			return fmt.Errorf("describe(): duplicate snippets.seed[].key %q", s.Key)
		}
		seedKeys[s.Key] = struct{}{}
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("describe(): snippets.seed[%q].name is required", s.Key)
		}
		if strings.TrimSpace(s.Content) == "" {
			return fmt.Errorf("describe(): snippets.seed[%q].content is required", s.Key)
		}
		if s.Category == "" {
			return fmt.Errorf("describe(): snippets.seed[%q].category is required", s.Key)
		}
		_, isLocal := catIDs[s.Category]
		if !IsBuiltinSnippetCategoryID(s.Category) && !isLocal {
			return fmt.Errorf("describe(): snippets.seed[%q].category %q is neither builtin nor declared by this extension", s.Key, s.Category)
		}
	}
	return nil
}

// apply merges the guest's functional face onto the manifest's security face.
//
// policies.actions is not on the wire: it is exactly the set of actions the tools
// request, so deriving it here keeps the tool registrations the only place an
// action is declared.
func (m *Manifest) apply(d *Descriptor) {
	m.Icon = d.Icon
	m.I18n = d.I18n
	m.AssetTypes = d.AssetTypes
	m.Tools = d.Tools
	m.Policies = d.Policies
	m.Policies.Actions = policyActions(d.Tools)
	m.Frontend = d.Frontend
	m.Snippets = d.Snippets
}

func policyActions(tools []ToolDef) []string {
	seen := make(map[string]struct{}, len(tools))
	actions := make([]string, 0, len(tools))
	for _, t := range tools {
		if _, dup := seen[t.PolicyAction]; dup {
			continue
		}
		seen[t.PolicyAction] = struct{}{}
		actions = append(actions, t.PolicyAction)
	}
	sort.Strings(actions)
	return actions
}
