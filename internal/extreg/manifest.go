package extreg

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// Manifest is the typed view of one extension: the runtime's manifest — security
// contract and presentation — with describe()'s contributions decoded into the
// shapes the host's subsystems read. The runtime keeps the contributions raw
// (extension.Manifest.Contribution); this is the one place they are decoded, with
// the wire types the contribution points judge them by, so every consumer — the
// points' Attach, exec, the asset form, opsctl — reads the same face.
//
// Its JSON is the merged manifest the frontend reads (the extension list, page
// loading). It is a read-only view: build it with ManifestOf.
type Manifest struct {
	*extension.Manifest

	AssetTypes []AssetTypeDef   `json:"assetTypes"`
	Tools      []extwire.Tool   `json:"tools"`
	Policies   PoliciesDef      `json:"policies"`
	Frontend   extwire.Pages    `json:"frontend"`
	Snippets   extwire.Snippets `json:"snippets"`
	// DatabaseDrivers are the driver descriptors, one per contributes.databaseDrivers id.
	DatabaseDrivers []dbdriver.Descriptor `json:"databaseDrivers"`
}

// AssetTypeDef is one asset type the extension contributes: the wire declaration,
// with its auth read into AuthDef, which the host renders and injects.
type AssetTypeDef struct {
	Type         string              `json:"type"`
	I18n         extwire.I18nName    `json:"i18n"`
	ConfigSchema map[string]any      `json:"configSchema"`
	Connection   *extwire.Connection `json:"connection,omitempty"`
	Auth         *AuthDef            `json:"auth,omitempty"`
	// TestConnection reports whether the type registered a test-connection
	// handler (opskat.AssetTypeReg.TestConnection in the guest SDK). The asset
	// form shows its "Test connection" button only when this is true; the host
	// dispatches the call itself (Plugin.TestConnection), never through policy —
	// testing a connection is not an operation on the asset.
	TestConnection bool `json:"testConnection,omitempty"`
}

// PoliciesDef is the policy face: the guest's extwire.Policy plus Actions,
// derived from the tools.
type PoliciesDef struct {
	Type    string                `json:"type"`
	Actions []string              `json:"actions"`
	Groups  []extwire.PolicyGroup `json:"groups"`
	Default []string              `json:"default"`
}

// ManifestOf decodes m's contributions into the typed view. An extension the
// process's contribution registry accepted always decodes — the points judge the
// same entries with the same wire types — so the error is a contribution in
// another shape, naming its key.
//
// policies.actions is not on the wire: it is exactly the set of actions the tools
// request, so deriving it here keeps the tool registrations the only place an
// action is declared. A driver's icon is the extension's: it is the one icon the
// user sees on it.
func ManifestOf(m *extension.Manifest) (*Manifest, error) {
	out := &Manifest{Manifest: m}
	var (
		assetTypes []extwire.AssetType
		policy     extwire.Policy
	)
	for _, c := range []struct {
		key string
		dst any
	}{
		{extwire.KeyAssetTypes, &assetTypes},
		{extwire.KeyTools, &out.Tools},
		{extwire.KeyPolicy, &policy},
		{extwire.KeyPages, &out.Frontend},
		{extwire.KeySnippets, &out.Snippets},
		{extwire.KeyDatabaseDrivers, &out.DatabaseDrivers},
	} {
		if err := decodeContribution(m, c.key, c.dst); err != nil {
			return nil, fmt.Errorf("extension %s: %w", m.Name, err)
		}
	}
	for _, at := range assetTypes {
		out.AssetTypes = append(out.AssetTypes, AssetTypeDef{
			Type:           at.Type,
			I18n:           at.I18n,
			ConfigSchema:   at.ConfigSchema,
			Connection:     at.Connection,
			Auth:           authDef(at.Auth),
			TestConnection: at.TestConnection,
		})
	}
	out.Policies = PoliciesDef{
		Type:    policy.Type,
		Actions: policyActions(out.Tools),
		Groups:  policy.Groups,
		Default: policy.Default,
	}
	for i := range out.DatabaseDrivers {
		out.DatabaseDrivers[i].Icon = m.Icon
	}
	return out, nil
}

// contributions is what a contribution is read from: a point's ExtensionView, or
// the runtime's manifest.
type contributions interface {
	Contribution(key string) json.RawMessage
}

// decodeContribution 把扩展在 key 下的原始贡献解进 dst；扩展没有贡献这个键时 dst 不变。
func decodeContribution(c contributions, key string, dst any) error {
	raw := c.Contribution(key)
	if raw == nil {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func policyActions(tools []extwire.Tool) []string {
	seen := make(map[string]struct{}, len(tools))
	actions := make([]string, 0, len(tools))
	for _, t := range tools {
		for _, a := range t.Actions() {
			if _, dup := seen[a]; dup {
				continue
			}
			seen[a] = struct{}{}
			actions = append(actions, a)
		}
	}
	sort.Strings(actions)
	return actions
}

// AssetType returns the declaration of assetType, nil when the extension does not
// register it.
func (m *Manifest) AssetType(assetType string) *AssetTypeDef {
	for i := range m.AssetTypes {
		if m.AssetTypes[i].Type == assetType {
			return &m.AssetTypes[i]
		}
	}
	return nil
}

// Localized returns a copy of the view with every i18n key resolved by tr: the
// presentation, asset type, tool, policy group, page and snippet category names,
// and the config schema's titles, placeholders, descriptions and option labels.
func (m *Manifest) Localized(tr func(key string) string) *Manifest {
	out := *m
	runtime := *m.Manifest
	runtime.I18n = extension.ManifestI18n{
		DisplayName: tr(m.I18n.DisplayName),
		Description: tr(m.I18n.Description),
	}
	out.Manifest = &runtime

	if len(m.AssetTypes) > 0 {
		out.AssetTypes = make([]AssetTypeDef, len(m.AssetTypes))
		for i, at := range m.AssetTypes {
			at.I18n = extwire.I18nName{Name: tr(at.I18n.Name)}
			at.ConfigSchema = localizeConfigSchema(at.ConfigSchema, tr)
			out.AssetTypes[i] = at
		}
	}

	if len(m.Tools) > 0 {
		out.Tools = make([]extwire.Tool, len(m.Tools))
		for i, t := range m.Tools {
			t.I18n = extwire.I18nDesc{Description: tr(t.I18n.Description)}
			out.Tools[i] = t
		}
	}

	if len(m.Policies.Groups) > 0 {
		out.Policies.Groups = make([]extwire.PolicyGroup, len(m.Policies.Groups))
		for i, pg := range m.Policies.Groups {
			pg.I18n = extwire.I18nNameDesc{
				Name:        tr(pg.I18n.Name),
				Description: tr(pg.I18n.Description),
			}
			out.Policies.Groups[i] = pg
		}
	}

	if len(m.Frontend.Pages) > 0 {
		out.Frontend.Pages = make([]extwire.Page, len(m.Frontend.Pages))
		for i, p := range m.Frontend.Pages {
			p.I18n = extwire.I18nName{Name: tr(p.I18n.Name)}
			out.Frontend.Pages[i] = p
		}
	}

	if len(m.Snippets.Categories) > 0 {
		out.Snippets.Categories = make([]extwire.SnippetCategory, len(m.Snippets.Categories))
		for i, c := range m.Snippets.Categories {
			c.I18n = extwire.I18nName{Name: tr(c.I18n.Name)}
			out.Snippets.Categories[i] = c
		}
	}

	return &out
}

// localizeConfigSchema translates title, placeholder, description and enumLabels fields in a JSON Schema.
func localizeConfigSchema(schema map[string]any, tr func(string) string) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		out[k] = v
	}
	// Translate top-level title/placeholder/description
	for _, field := range []string{"title", "placeholder", "description"} {
		if s, ok := out[field].(string); ok && s != "" {
			out[field] = tr(s)
		}
	}
	// Enum option labels are i18n keys, like title.
	if labels, ok := out["enumLabels"].([]any); ok {
		translated := make([]any, len(labels))
		for i, l := range labels {
			if key, ok := l.(string); ok {
				translated[i] = tr(key)
			} else {
				translated[i] = l
			}
		}
		out["enumLabels"] = translated
	}
	// Recurse into properties
	if props, ok := out["properties"].(map[string]any); ok {
		newProps := make(map[string]any, len(props))
		for name, propVal := range props {
			if propMap, ok := propVal.(map[string]any); ok {
				newProps[name] = localizeConfigSchema(propMap, tr)
			} else {
				newProps[name] = propVal
			}
		}
		out["properties"] = newProps
	}
	return out
}
