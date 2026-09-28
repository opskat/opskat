// pkg/extension/auth.go
package extension

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// AuthDef is the credential injection an asset type declares in describe(): the
// host renders its bindings from the asset's config — password fields decrypted
// host-side — and injects them into every HTTP request the extension sends to
// that asset's endpoint. The plaintext never crosses into the guest, which is
// what lets an HTTP extension authenticate without capabilities.credentials.
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

// AuthBinding injects one rendered value template into a request.
//
// In is where: "header" (Name is the header), "query" (Name is the parameter),
// or "basic" (no Name; Value renders "user:password" and is sent as
// Authorization: Basic <base64>). Value is text with {{field}} and
// {{base64(part, ...)}} placeholders, a part being a config field or a
// double-quoted literal.
type AuthBinding struct {
	In    string `json:"in"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value"`
}

const (
	authInHeader = "header"
	authInQuery  = "query"
	authInBasic  = "basic"
)

// UnmarshalJSON refuses a key the host does not read: a mistyped "name" left
// unread would load an extension that injects nothing where its author expects
// a credential.
func (a *AuthDef) UnmarshalJSON(data []byte) error {
	type plain AuthDef
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var v plain
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("auth (keys: selector, groups[].when, groups[].bindings[].in/name/value): %w", err)
	}
	*a = AuthDef(v)
	return nil
}

// headerNameRe is an RFC 7230 token: what net/http accepts as a header name.
var headerNameRe = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// validate checks the declaration against the fields the asset type's
// configSchema declares — a template may reference nothing else — and the
// subset of them that are secrets (format:"password"), which may be injected
// but never select a group: the selector's value is compared and logged as
// plain data.
func (a *AuthDef) validate(fields, secrets map[string]bool) error {
	if len(a.Groups) == 0 {
		return fmt.Errorf("auth.groups must declare at least one group")
	}
	if a.Selector == "" {
		if len(a.Groups) > 1 {
			return fmt.Errorf("auth declares %d groups but no selector — name the config field that picks one", len(a.Groups))
		}
		if a.Groups[0].When != "" {
			return fmt.Errorf("auth.groups[0].when %q needs a selector field to compare against", a.Groups[0].When)
		}
	} else if !fields[a.Selector] {
		return fmt.Errorf("auth.selector %q is not a configSchema property", a.Selector)
	} else if secrets[a.Selector] {
		return fmt.Errorf("auth.selector %q is a password field; a secret cannot select an auth group", a.Selector)
	}
	seen := make(map[string]bool, len(a.Groups))
	for i, g := range a.Groups {
		if a.Selector != "" {
			if g.When == "" {
				return fmt.Errorf("auth.groups[%d].when is required: it is the %s value that selects the group", i, a.Selector)
			}
			if seen[g.When] {
				return fmt.Errorf("auth.groups: duplicate when %q", g.When)
			}
			seen[g.When] = true
		}
		if len(g.Bindings) == 0 {
			return fmt.Errorf("auth.groups[%d].bindings must declare at least one binding", i)
		}
		for j, b := range g.Bindings {
			if err := b.validate(fields); err != nil {
				return fmt.Errorf("auth.groups[%d].bindings[%d]: %w", i, j, err)
			}
		}
	}
	return nil
}

func (b AuthBinding) validate(fields map[string]bool) error {
	switch b.In {
	case authInHeader:
		if !headerNameRe.MatchString(b.Name) {
			return fmt.Errorf("header binding needs a valid header name (got %q)", b.Name)
		}
	case authInQuery:
		if b.Name == "" {
			return fmt.Errorf("query binding needs a parameter name")
		}
	case authInBasic:
		if b.Name != "" {
			return fmt.Errorf("basic binding takes no name (got %q): it always sets Authorization", b.Name)
		}
	default:
		return fmt.Errorf("in must be header, query or basic (got %q)", b.In)
	}
	tmpl, err := parseAuthTemplate(b.Value)
	if err != nil {
		return err
	}
	for _, f := range tmpl.fields() {
		if !fields[f] {
			return fmt.Errorf("template references %q, which is not a configSchema property", f)
		}
	}
	return nil
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
	var out []string
	seen := map[string]bool{}
	for _, b := range g.Bindings {
		tmpl, _ := parseAuthTemplate(b.Value) // validated at describe()
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
func (g *AuthGroup) render(values map[string]string) []renderedBinding {
	out := make([]renderedBinding, 0, len(g.Bindings))
	for _, b := range g.Bindings {
		tmpl, _ := parseAuthTemplate(b.Value) // validated at describe()
		out = append(out, renderedBinding{in: b.In, name: b.Name, value: tmpl.render(values)})
	}
	return out
}

// renderedBinding is a binding with its credential filled in.
type renderedBinding struct {
	in, name, value string
}

// requestAuth is the credential injection one HTTP request carries. It rides on
// the request's context rather than on the (cached, shared) client, and is
// applied by authTransport to each hop isEndpoint admits — the first request and
// every redirect alike.
type requestAuth struct {
	bindings   []renderedBinding
	isEndpoint func(*url.URL) bool
}

type requestAuthKey struct{}

// redact removes the injected values from err's text. A server may echo the
// request it received — a redirect whose Location repeats the request URI, query
// credential included, is the common case — and net/http names that hop's URL in
// the error it returns when the hop is refused or fails. The error travels back
// to the guest, which must never see what the host injected for it.
func (a *requestAuth) redact(err error) error {
	msg := err.Error()
	redacted := msg
	for _, b := range a.bindings {
		if b.value == "" {
			continue
		}
		forms := []string{b.value, url.QueryEscape(b.value), url.PathEscape(b.value)}
		if b.in == authInBasic {
			forms = append(forms, base64.StdEncoding.EncodeToString([]byte(b.value)))
		}
		for _, form := range forms {
			redacted = strings.ReplaceAll(redacted, form, "[REDACTED]")
		}
	}
	if redacted == msg {
		return err
	}
	return &redactedError{msg: redacted, err: err}
}

// redactedError reports a redacted message while keeping the original error
// reachable for errors.Is / errors.As (a canceled call is still recognizable).
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func withRequestAuth(ctx context.Context, auth *requestAuth) context.Context {
	return context.WithValue(ctx, requestAuthKey{}, auth)
}

// authTransport injects a request's credentials (see requestAuth) on the way
// out. It works on a clone: the request the client holds — and the URL it
// reports in errors that reach the guest — never carries them.
//
// requireTLS is set on the client of an asset endpoint whose connection
// settings enable TLS: a plain http hop to it would silently skip what the
// user configured, so it is refused.
type authTransport struct {
	base       *http.Transport
	requireTLS bool
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.requireTLS && req.URL.Scheme != "https" {
		return nil, refuseRequest(req, fmt.Errorf("plain %s to the asset's endpoint refused: its connection settings require TLS", req.URL.Scheme))
	}
	auth, _ := req.Context().Value(requestAuthKey{}).(*requestAuth)
	if auth == nil || !auth.isEndpoint(req.URL) {
		return t.base.RoundTrip(req)
	}
	if req.Method == http.MethodTrace {
		// A TRACE is answered by echoing the request, headers included: it would
		// hand the guest the credentials injected so that it never sees them.
		return nil, refuseRequest(req, errors.New("TRACE refused on a request carrying the asset's credentials"))
	}
	out := req.Clone(req.Context())
	for _, b := range auth.bindings {
		switch b.in {
		case authInHeader:
			out.Header.Set(b.name, b.value)
		case authInBasic:
			out.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(b.value)))
		case authInQuery:
			out.URL.RawQuery = setRawQueryParam(out.URL.RawQuery, b.name, b.value)
		}
	}
	return t.base.RoundTrip(out)
}

// refuseRequest honors the RoundTripper contract — the request body is closed
// even when the request is never sent — and returns err.
func refuseRequest(req *http.Request, err error) error {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return err
}

// setRawQueryParam replaces every name=... pair of rawQuery with name=value,
// appended last, and leaves every other pair byte for byte as the guest wrote
// it: re-encoding through url.Values would reorder the query and drop pairs
// url.ParseQuery rejects (such as one containing ';').
func setRawQueryParam(rawQuery, name, value string) string {
	var kept []string
	if rawQuery != "" {
		for _, pair := range strings.Split(rawQuery, "&") {
			key, _, _ := strings.Cut(pair, "=")
			if unescaped, err := url.QueryUnescape(key); err == nil && unescaped == name {
				continue
			}
			kept = append(kept, pair)
		}
	}
	kept = append(kept, url.QueryEscape(name)+"="+url.QueryEscape(value))
	return strings.Join(kept, "&")
}

// CloseIdleConnections lets http.Client.CloseIdleConnections reach the pool.
func (t *authTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

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
