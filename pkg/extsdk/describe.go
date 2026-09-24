package opskat

import "encoding/json"

// describe() is the guest's answer to "what can you do".
//
// It is generated from the same registries dispatch serves calls from, so the
// host's view of the extension and the code that runs cannot disagree: a tool the
// guest can execute is a tool describe reports, with the schema its handler
// actually decodes. manifest.json keeps only what a user must be able to audit
// before this code ever runs — the capability grants.
//
// The wire shape mirrors what the host merges into its Manifest; keep the JSON
// tags in step with pkg/extension/descriptor.go.

type descriptor struct {
	Icon       string          `json:"icon,omitempty"`
	I18n       descI18n        `json:"i18n"`
	AssetTypes []descAssetType `json:"assetTypes"`
	Tools      []descTool      `json:"tools"`
	Policies   descPolicies    `json:"policies"`
	Frontend   *descFrontend   `json:"frontend,omitempty"`
	Snippets   *descSnippets   `json:"snippets,omitempty"`
}

type descI18n struct {
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

type descName struct {
	Name string `json:"name"`
}

type descAssetType struct {
	Type         string         `json:"type"`
	I18n         descName       `json:"i18n"`
	ConfigSchema map[string]any `json:"configSchema"`
	ProxyChain   bool           `json:"proxyChain,omitempty"`
}

type descTool struct {
	Name         string         `json:"name"`
	I18n         descToolI18n   `json:"i18n"`
	Parameters   map[string]any `json:"parameters"`
	PolicyAction string         `json:"policyAction"`
}

type descToolI18n struct {
	Description string `json:"description"`
}

type descPolicies struct {
	Type string `json:"type"`
	// The action set is not on the wire: it is exactly the set of actions the
	// tools request, and the host derives it from them.
	Groups  []descPolicyGroup `json:"groups,omitempty"`
	Default []string          `json:"default,omitempty"`
}

type descPolicyGroup struct {
	ID     string         `json:"id"`
	I18n   descNameDesc   `json:"i18n"`
	Policy map[string]any `json:"policy"`
}

type descNameDesc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type descFrontend struct {
	Entry  string     `json:"entry"`
	Styles string     `json:"styles,omitempty"`
	Pages  []descPage `json:"pages"`
}

type descPage struct {
	ID        string   `json:"id"`
	Slot      string   `json:"slot,omitempty"`
	I18n      descName `json:"i18n"`
	Component string   `json:"component"`
}

type descSnippets struct {
	Categories []descSnippetCategory `json:"categories,omitempty"`
	Seed       []descSeed            `json:"seed,omitempty"`
}

type descSnippetCategory struct {
	ID        string   `json:"id"`
	AssetType string   `json:"assetType"`
	I18n      descName `json:"i18n"`
}

type descSeed struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Content     string `json:"content"`
	Description string `json:"description,omitempty"`
}

func dispatchDescribe() (json.RawMessage, error) {
	d := descriptor{
		Icon:     meta.Icon,
		I18n:     descI18n{DisplayName: meta.DisplayName, Description: meta.Description},
		Policies: descPolicies{Type: meta.PolicyType},
	}

	for _, at := range assetTypes {
		d.AssetTypes = append(d.AssetTypes, descAssetType{
			Type:         at.typ,
			I18n:         descName{Name: at.name},
			ConfigSchema: at.schema,
			ProxyChain:   at.proxyChain,
		})
	}

	for _, name := range toolOrder {
		t := tools[name]
		d.Tools = append(d.Tools, descTool{
			Name:         t.name,
			I18n:         descToolI18n{Description: t.doc},
			Parameters:   t.schema,
			PolicyAction: t.action,
		})
	}

	for _, g := range policyGroups {
		policy := map[string]any{}
		if len(g.allow) > 0 {
			policy["allow_list"] = g.allow
		}
		if len(g.deny) > 0 {
			policy["deny_list"] = g.deny
		}
		d.Policies.Groups = append(d.Policies.Groups, descPolicyGroup{
			ID:     g.id,
			I18n:   descNameDesc{Name: g.name, Description: g.description},
			Policy: policy,
		})
		if g.isDefault {
			d.Policies.Default = append(d.Policies.Default, g.id)
		}
	}

	if frontendSpec.Entry != "" {
		fe := &descFrontend{Entry: frontendSpec.Entry, Styles: frontendSpec.Styles}
		for _, p := range frontendSpec.Pages {
			fe.Pages = append(fe.Pages, descPage{
				ID:        p.ID,
				Slot:      p.Slot,
				I18n:      descName{Name: p.Name},
				Component: p.Component,
			})
		}
		d.Frontend = fe
	}

	if len(snippetCats) > 0 || len(snippetSeeds) > 0 {
		sn := &descSnippets{}
		for _, c := range snippetCats {
			sn.Categories = append(sn.Categories, descSnippetCategory{
				ID: c.id, AssetType: c.assetType, I18n: descName{Name: c.name},
			})
		}
		for _, s := range snippetSeeds {
			sn.Seed = append(sn.Seed, descSeed(s))
		}
		d.Snippets = sn
	}

	return json.Marshal(d)
}
