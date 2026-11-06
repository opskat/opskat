package extreg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/pkg/authtmpl"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// AuthDef is the credential injection an asset type declares in describe(): the
// host renders its bindings from the asset's config — password fields decrypted
// host-side — into the scope's Auth (Inject), and the runtime injects them into
// every HTTP request the extension sends to that asset's endpoint. The plaintext
// never crosses into the guest, which is what lets an HTTP extension authenticate
// without permissions.credentials.
//
// Selector names the config field whose value picks the active group (e.g.
// authType); a value no group names means "no credentials". Without a selector
// there is exactly one group, always active.
type AuthDef struct {
	Selector string      `json:"selector,omitempty"`
	Groups   []AuthGroup `json:"groups"`
}

// AuthGroup is one set of bindings, active when the selector field equals When.
type AuthGroup struct {
	When     string        `json:"when,omitempty"`
	Bindings []AuthBinding `json:"bindings"`
}

// MarshalJSON adds the config fields the group's bindings reference. It is how
// the asset form — which receives the manifest, not this package — shows only
// the selected group's fields; derived here so the template grammar has one
// reader. Output only: a declaration carrying "fields" is refused on the way in.
func (g AuthGroup) MarshalJSON() ([]byte, error) {
	tmpls, err := g.templates()
	if err != nil {
		return nil, err
	}
	type plain AuthGroup
	return json.Marshal(struct {
		plain
		Fields []string `json:"fields"`
	}{plain(g), tmpls.fields()})
}

// AuthBinding injects one rendered value template into a request.
//
// In is where (extension.AuthIn*): "header" (Name is the header), "query" (Name
// is the parameter), or "basic" (no Name; Value renders "user:password" and is sent as
// Authorization: Basic <base64>). Value is a template in the language custom
// types share (internal/pkg/authtmpl): literal text with {{expression}}
// placeholders, a bare name in an expression being a config field.
type AuthBinding struct {
	In    string `json:"in"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value"`
}

// AuthDefOf reads a wire auth declaration (extwire.Auth, decoded strictly) into the
// host's AuthDef; nil stays nil.
func AuthDefOf(a *extwire.Auth) *AuthDef {
	if a == nil {
		return nil
	}
	out := &AuthDef{Selector: a.Selector, Groups: make([]AuthGroup, len(a.Groups))}
	for i, g := range a.Groups {
		out.Groups[i] = AuthGroup{When: g.When, Bindings: make([]AuthBinding, len(g.Bindings))}
		for j, b := range g.Bindings {
			out.Groups[i].Bindings[j] = AuthBinding(b)
		}
	}
	return out
}

// template parses Value. Any name parses as a field reference — the asset-type
// contribution point holds the references against the configSchema — and
// request.* is not available to an extension binding.
func (b AuthBinding) template() (*authtmpl.Template, error) {
	return authtmpl.Parse(b.Value, authtmpl.ParseOptions{AnyField: true})
}

// authTemplateFields parses an auth binding's value template and returns the
// config fields it references. The template language is the one custom asset
// types use (internal/pkg/authtmpl): the asset-type contribution point validates
// declarations with it before load, and the injection below renders what it
// accepted.
func authTemplateFields(value string) ([]string, error) {
	tmpl, err := AuthBinding{Value: value}.template()
	if err != nil {
		return nil, err
	}
	return tmpl.Fields(), nil
}

// activeGroup returns the group selected by selectorValue (ignored without a
// selector); nil when no group is selected.
func (a *AuthDef) activeGroup(selectorValue string) *AuthGroup {
	if a.Selector == "" {
		return &a.Groups[0]
	}
	for i := range a.Groups {
		if a.Groups[i].When == selectorValue {
			return &a.Groups[i]
		}
	}
	return nil
}

// authTemplates is a group's bindings with their value templates parsed.
type authTemplates struct {
	bindings []AuthBinding
	parsed   []*authtmpl.Template // parsed[i] is bindings[i].Value
}

// templates parses the group's value templates.
func (g AuthGroup) templates() (*authTemplates, error) {
	parsed := make([]*authtmpl.Template, len(g.Bindings))
	for i, b := range g.Bindings {
		tmpl, err := b.template()
		if err != nil {
			return nil, fmt.Errorf("bindings[%d]: %w", i, err)
		}
		parsed[i] = tmpl
	}
	return &authTemplates{bindings: g.Bindings, parsed: parsed}, nil
}

// fields returns the config fields the templates reference, each once.
func (t *authTemplates) fields() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, tmpl := range t.parsed {
		for _, f := range tmpl.Fields() {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// render fills the bindings from values, the plaintext of the fields they
// reference; a field absent from values — one the asset's config leaves unset —
// renders empty. The bindings share one render context, so now.* reads the same
// instant in each of them.
func (t *authTemplates) render(values map[string]string) ([]extension.AuthValue, error) {
	fields := t.fields()
	referenced := make(map[string]string, len(fields))
	for _, f := range fields {
		referenced[f] = values[f]
	}
	rc := authtmpl.NewRenderContext(referenced)
	out := make([]extension.AuthValue, 0, len(t.bindings))
	for i, b := range t.bindings {
		value, err := t.parsed[i].Render(rc)
		if err != nil {
			return nil, fmt.Errorf("bindings[%d]: %w", i, err)
		}
		out = append(out, extension.AuthValue{In: b.In, Name: b.Name, Value: value})
	}
	return out, nil
}

// CredentialValues returns the named config fields of an asset as plaintext,
// password fields decrypted: host-side only, for credential injection. A field
// the config lacks is absent from the map.
type CredentialValues func(ctx context.Context, fields []string) (map[string]string, error)

// Inject is the RequestAuth of an asset whose type declares a: on each request it
// reads the selector field, picks the active group (none: no credentials) and
// renders that group's bindings from values. It is how the asset-type side
// turns the declaration into a scope's Auth; any failure to read a field fails
// the request rather than sending it without the credentials the user set.
func (a *AuthDef) Inject(values CredentialValues) extension.RequestAuth {
	return func(ctx context.Context) ([]extension.AuthValue, error) {
		var selected string
		if a.Selector != "" {
			v, err := values(ctx, []string{a.Selector})
			if err != nil {
				return nil, err
			}
			selected = v[a.Selector]
		}
		group := a.activeGroup(selected)
		if group == nil {
			return nil, nil
		}
		tmpls, err := group.templates()
		if err != nil {
			return nil, err
		}
		v, err := values(ctx, tmpls.fields())
		if err != nil {
			return nil, err
		}
		return tmpls.render(v)
	}
}
