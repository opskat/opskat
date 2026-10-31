// pkg/extension/auth.go
package extension

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Credential injection: the host renders an asset's credentials into
// AuthValues (Scope.Auth — the asset-type side's declaration, internal/extreg)
// and the runtime injects them into every HTTP request the extension sends to
// that asset's endpoint. The plaintext never crosses into the guest, which is
// what lets an HTTP extension authenticate without permissions.credentials.

// The locations a credential is injected into (AuthValue.In).
const (
	AuthInHeader = "header"
	AuthInQuery  = "query"
	AuthInBasic  = "basic"
)

// requestAuth is the credential injection one HTTP request carries. It rides on
// the request's context rather than on the (cached, shared) client, and is
// applied by authTransport to each hop isEndpoint admits — the first request and
// every redirect alike.
type requestAuth struct {
	bindings   []AuthValue
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
		if b.Value == "" {
			continue
		}
		forms := []string{b.Value, url.QueryEscape(b.Value), url.PathEscape(b.Value)}
		if b.In == AuthInBasic {
			forms = append(forms, base64.StdEncoding.EncodeToString([]byte(b.Value)))
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
		switch b.In {
		case AuthInHeader:
			out.Header.Set(b.Name, b.Value)
		case AuthInBasic:
			out.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(b.Value)))
		case AuthInQuery:
			out.URL.RawQuery = setRawQueryParam(out.URL.RawQuery, b.Name, b.Value)
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
