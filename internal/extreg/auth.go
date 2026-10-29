package extreg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
	type plain AuthGroup
	return json.Marshal(struct {
		plain
		Fields []string `json:"fields"`
	}{plain(g), g.fields()})
}

// AuthBinding injects one rendered value template into a request.
//
// In is where (extension.AuthIn*): "header" (Name is the header), "query" (Name
// is the parameter), or "basic" (no Name; Value renders "user:password" and is sent as
// Authorization: Basic <base64>). Value is text with {{field}} and
// {{base64(part, ...)}} placeholders, a part being a config field or a
// double-quoted literal.
type AuthBinding struct {
	In    string `json:"in"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value"`
}

// authDef reads a wire auth declaration (extwire.Auth, decoded strictly) into the
// host's AuthDef; nil stays nil.
func authDef(a *extwire.Auth) *AuthDef {
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

// authTemplateFields parses an auth binding's value template and returns the
// config fields it references. It is the one reader of the template grammar: the
// asset-type contribution point validates declarations with it before load, and
// the injection below renders what it accepted.
func authTemplateFields(value string) ([]string, error) {
	tmpl, err := parseAuthTemplate(value)
	if err != nil {
		return nil, err
	}
	return tmpl.fields(), nil
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

// fields returns the config fields the group's templates reference.
func (g *AuthGroup) fields() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, b := range g.Bindings {
		tmpl, _ := parseAuthTemplate(b.Value) // validated at load (authTemplateFields)
		for _, f := range tmpl.fields() {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// render fills the group's bindings from values, the plaintext of the fields
// they reference.
func (g *AuthGroup) render(values map[string]string) []extension.AuthValue {
	out := make([]extension.AuthValue, 0, len(g.Bindings))
	for _, b := range g.Bindings {
		tmpl, _ := parseAuthTemplate(b.Value) // validated at load (authTemplateFields)
		out = append(out, extension.AuthValue{In: b.In, Name: b.Name, Value: tmpl.render(values)})
	}
	return out
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
		v, err := values(ctx, group.fields())
		if err != nil {
			return nil, err
		}
		return group.render(v), nil
	}
}

// authTemplate is a parsed binding value: literal text and placeholders.
type authTemplate []authSegment

// authSegment is one piece of a template: literal text, a field, or a base64
// of parts (each part a field or a literal).
type authSegment struct {
	literal string
	field   string
	base64  []authSegment
}

// parseAuthTemplate parses "text {{field}} {{base64(field, \"lit\")}}".
func parseAuthTemplate(s string) (authTemplate, error) {
	var out authTemplate
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			if s != "" {
				out = append(out, authSegment{literal: s})
			}
			return out, nil
		}
		if start > 0 {
			out = append(out, authSegment{literal: s[:start]})
		}
		end := placeholderEnd(s[start+2:])
		if end < 0 {
			return nil, fmt.Errorf("template %q: unclosed {{", s)
		}
		expr := strings.TrimSpace(s[start+2 : start+2+end])
		seg, err := parseAuthExpr(expr)
		if err != nil {
			return nil, fmt.Errorf("template placeholder {{%s}}: %w", expr, err)
		}
		out = append(out, seg)
		s = s[start+2+end+2:]
	}
}

// placeholderEnd returns the index of the "}}" closing a placeholder body,
// skipping any inside a quoted literal; -1 when there is none.
func placeholderEnd(s string) int {
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch {
		case inQuote && s[i] == '\\':
			i++
		case s[i] == '"':
			inQuote = !inQuote
		case !inQuote && strings.HasPrefix(s[i:], "}}"):
			return i
		}
	}
	return -1
}

var authFieldRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func parseAuthExpr(expr string) (authSegment, error) {
	if authFieldRe.MatchString(expr) {
		return authSegment{field: expr}, nil
	}
	const fn = "base64("
	if !strings.HasPrefix(expr, fn) || !strings.HasSuffix(expr, ")") {
		return authSegment{}, fmt.Errorf("expected a config field or base64(<parts>)")
	}
	parts, err := parseAuthArgs(expr[len(fn) : len(expr)-1])
	if err != nil {
		return authSegment{}, fmt.Errorf("base64: %w", err)
	}
	return authSegment{base64: parts}, nil
}

// parseAuthArgs parses a comma-separated list of fields and quoted literals.
func parseAuthArgs(s string) ([]authSegment, error) {
	var parts []authSegment
	s = strings.TrimSpace(s)
	for s != "" {
		var part authSegment
		if s[0] == '"' {
			quoted, err := strconv.QuotedPrefix(s)
			if err != nil {
				return nil, fmt.Errorf("bad string literal in %q", s)
			}
			lit, _ := strconv.Unquote(quoted)
			part = authSegment{literal: lit}
			s = s[len(quoted):]
		} else {
			n := strings.IndexByte(s, ',')
			if n < 0 {
				n = len(s)
			}
			name := strings.TrimSpace(s[:n])
			if !authFieldRe.MatchString(name) {
				return nil, fmt.Errorf("part %q is neither a config field nor a quoted literal", name)
			}
			part = authSegment{field: name}
			s = s[n:]
		}
		parts = append(parts, part)
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}
		if s[0] != ',' {
			return nil, fmt.Errorf("expected ',' before %q", s)
		}
		s = strings.TrimSpace(s[1:])
		if s == "" {
			return nil, fmt.Errorf("trailing ','")
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("needs at least one part")
	}
	return parts, nil
}

// fields returns every config field the template references.
func (t authTemplate) fields() []string {
	var out []string
	for _, seg := range t {
		if seg.field != "" {
			out = append(out, seg.field)
		}
		for _, p := range seg.base64 {
			if p.field != "" {
				out = append(out, p.field)
			}
		}
	}
	return out
}

// render fills the template from values; a field absent from values renders empty.
func (t authTemplate) render(values map[string]string) string {
	var b strings.Builder
	for _, seg := range t {
		switch {
		case seg.base64 != nil:
			var raw strings.Builder
			for _, p := range seg.base64 {
				if p.field != "" {
					raw.WriteString(values[p.field])
				} else {
					raw.WriteString(p.literal)
				}
			}
			b.WriteString(base64.StdEncoding.EncodeToString([]byte(raw.String())))
		case seg.field != "":
			b.WriteString(values[seg.field])
		default:
			b.WriteString(seg.literal)
		}
	}
	return b.String()
}
