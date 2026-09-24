// pkg/extension/io_http.go
package extension

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// dialGuard wraps a DialContext function and rejects connections to private/loopback
// IPs at dial time. This catches DNS rebinding attacks where a hostname resolves to
// a private IP after the URL-level allowlist check has already passed.
//
// When private targets are allowed there is nothing to deny, so the host is not
// resolved here: the dial — possibly through the asset's SSH tunnel — resolves it
// where it will be reached, and a local lookup would fail a hostname only the far
// side knows.
func dialGuard(origDial func(ctx context.Context, network, addr string) (net.Conn, error), allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if !allowPrivate {
			if err := denyPrivateTarget(ctx, addr); err != nil {
				return nil, err
			}
		}
		if origDial != nil {
			return origDial(ctx, network, addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
}

// denyPrivateTarget rejects addr when it is, or its hostname resolves to, a
// private/loopback IP.
func denyPrivateTarget(ctx context.Context, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsPrivateIP(ip) {
			return fmt.Errorf("dial denied: private IP %s", ip)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, ipa := range ips {
		if IsPrivateIP(ipa.IP) {
			return fmt.Errorf("dial denied: hostname %q resolves to private IP %s", host, ipa.IP)
		}
	}
	return nil
}

type httpPhase int

const (
	httpPhaseWriting httpPhase = iota // can write request body
	httpPhaseFlushed                  // request sent, can read response
	httpPhaseClosed
)

// DialFunc is a custom dialer for HTTP transports (e.g. SSH tunnel).
type DialFunc func(network, addr string) (net.Conn, error)

type httpHandle struct {
	mu      sync.Mutex
	client  *http.Client
	method  string
	url     string
	headers map[string]string
	ctx     context.Context
	cancel  context.CancelFunc
	bodyBuf *bytes.Buffer // buffered request body (for POST/PUT/PATCH)
	resp    *http.Response
	phase   httpPhase
	hasBody bool         // true for POST/PUT/PATCH
	auth    *requestAuth // credentials injected into endpoint hops; nil = none
	// redirectGuard vets where this request may be redirected; nil = anywhere.
	// Like auth it rides on the request, not the client: a cached client is
	// shared by later calls, whose guard may differ from the call that built it.
	redirectGuard func(*url.URL) error
}

// newHTTPHandle creates an HTTP handle ready for writing (POST/PUT/PATCH)
// or immediate flushing (GET/HEAD/DELETE/OPTIONS). The client is single-use:
// for a client reused across many calls to the same asset, see
// buildCachedHTTPClient / openHTTPResourceWithClient.
func newHTTPHandle(params IOOpenParams, dial DialFunc) (*httpHandle, error) {
	if params.URL == "" {
		return nil, fmt.Errorf("URL is required for HTTP handle")
	}

	// dial has no ctx of its own; the transport's own per-request ctx is
	// discarded in favor of whatever ctx this call closes dial over. That is
	// only safe for a client used within a single call's lifetime (this
	// function) — never for one cached across calls (buildCachedHTTPClient
	// keeps the transport's per-request ctx instead).
	var ctxDial func(ctx context.Context, network, addr string) (net.Conn, error)
	if dial != nil {
		ctxDial = func(_ context.Context, network, addr string) (net.Conn, error) { return dial(network, addr) }
	}
	return newHandleFromClient(params, newHTTPClient(newHTTPTransport(ctxDial, nil, params.AllowPrivate)), nil)
}

// buildCachedHTTPClient builds an *http.Client meant to be reused across many
// calls to the same asset (see DefaultHostProvider.httpClients). Unlike
// newHTTPHandle's client, dial keeps its own per-dial context — supplied by
// the transport at the time it actually needs a new connection — since the
// transport may open one long after the call that first built it has
// returned, to serve a different, later call. Nothing a call decides — its
// credentials, where it may be redirected — is built in: each request carries
// its own (see httpHandle.Flush).
func buildCachedHTTPClient(dial DialContextFunc, tlsConfig *tls.Config, allowPrivate bool) *http.Client {
	var ctxDial func(ctx context.Context, network, addr string) (net.Conn, error)
	if dial != nil {
		ctxDial = func(ctx context.Context, network, addr string) (net.Conn, error) { return dial(ctx, network, addr) }
	}
	return newHTTPClient(newHTTPTransport(ctxDial, tlsConfig, allowPrivate))
}

// newHTTPTransport clones the default transport, wires dial through the
// dial-time private-IP guard (catching DNS rebinding after URL-level checks),
// and applies tlsConfig when set. dial nil means "use the clone's own default
// dialer" (plain outbound TCP).
func newHTTPTransport(dial func(ctx context.Context, network, addr string) (net.Conn, error), tlsConfig *tls.Config, allowPrivate bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	baseDial := transport.DialContext
	if dial != nil {
		baseDial = dial
	}
	transport.DialContext = dialGuard(baseDial, allowPrivate)
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}
	return transport
}

// newHTTPClient wraps transport in an *http.Client. The client itself holds
// neither credentials nor a redirect policy: authTransport injects only the
// credentials a request carries on its context, and checkRedirect applies only
// the guard the request carries there.
func newHTTPClient(transport *http.Transport) *http.Client {
	return &http.Client{Transport: &authTransport{base: transport}, CheckRedirect: checkRedirect}
}

type redirectGuardKey struct{}

func withRedirectGuard(ctx context.Context, guard func(*url.URL) error) context.Context {
	return context.WithValue(ctx, redirectGuardKey{}, guard)
}

// checkRedirect keeps net/http's own redirect limit and applies the guard the
// request carries (net/http hands every redirect hop the first request's
// context), which only narrows where a redirect may go.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if guard, _ := req.Context().Value(redirectGuardKey{}).(func(*url.URL) error); guard != nil {
		return guard(req.URL)
	}
	return nil
}

// newHandleFromClient builds the per-call handle state (method, url, headers,
// redirect guard, its own cancelable context, body buffer) around client, which may be freshly
// built (newHTTPHandle) or a cached one shared across calls
// (openHTTPResourceWithClient).
func newHandleFromClient(params IOOpenParams, client *http.Client, auth *requestAuth) (*httpHandle, error) {
	method := strings.ToUpper(params.Method)
	if method == "" {
		method = "GET"
	}
	if params.URL == "" {
		return nil, fmt.Errorf("URL is required for HTTP handle")
	}

	ctx, cancel := context.WithCancel(context.Background())
	hasBody := method == "POST" || method == "PUT" || method == "PATCH"

	return &httpHandle{
		client:  client,
		method:  method,
		url:     params.URL,
		headers: params.Headers,
		ctx:     ctx,
		cancel:  cancel,
		bodyBuf: &bytes.Buffer{},
		phase:   httpPhaseWriting,
		hasBody: hasBody,
		auth:    auth,

		redirectGuard: params.RedirectGuard,
	}, nil
}

// Write writes data to the request body buffer. Only valid before Flush.
func (h *httpHandle) Write(data []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.phase != httpPhaseWriting {
		return 0, fmt.Errorf("not in writing phase")
	}
	return h.bodyBuf.Write(data)
}

// Flush builds the HTTP request, executes it, and returns response metadata.
// Blocks until the response headers arrive.
func (h *httpHandle) Flush() (*IOMeta, error) {
	h.mu.Lock()
	if h.phase != httpPhaseWriting {
		h.mu.Unlock()
		return nil, fmt.Errorf("already flushed or closed")
	}
	h.phase = httpPhaseFlushed

	// Build request body.
	var body *bytes.Reader
	if h.hasBody {
		body = bytes.NewReader(h.bodyBuf.Bytes())
	}

	reqCtx := h.ctx
	if h.auth != nil {
		reqCtx = withRequestAuth(reqCtx, h.auth)
	}
	if h.redirectGuard != nil {
		reqCtx = withRedirectGuard(reqCtx, h.redirectGuard)
	}
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(reqCtx, h.method, h.url, body)
	} else {
		req, err = http.NewRequestWithContext(reqCtx, h.method, h.url, nil)
	}
	if err != nil {
		h.mu.Unlock()
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}

	for k, v := range h.headers {
		req.Header.Set(k, v)
	}

	client := h.client
	h.mu.Unlock()

	// The body is read by Read() and closed by Close() — or below, when Close
	// already ran.
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	h.mu.Lock()
	if h.phase == httpPhaseClosed {
		// Close ran while the round trip was in flight — the invocation was
		// interrupted — and had no response to release then. Release it here, or
		// its body and the connection behind it stay open with no one to close them.
		h.mu.Unlock()
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close HTTP response body of an interrupted request", zap.Error(err))
		}
		return nil, fmt.Errorf("HTTP request interrupted: handle closed")
	}
	h.resp = resp
	h.mu.Unlock()

	meta := &IOMeta{
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        resp.ContentLength,
		Headers:     make(map[string]string),
	}
	for k := range resp.Header {
		meta.Headers[k] = resp.Header.Get(k)
	}

	return meta, nil
}

// Read reads from the response body. Only valid after Flush.
func (h *httpHandle) Read(buf []byte) (int, error) {
	h.mu.Lock()
	if h.phase != httpPhaseFlushed {
		h.mu.Unlock()
		return 0, fmt.Errorf("response not flushed yet; call Flush first")
	}
	resp := h.resp
	h.mu.Unlock()

	if resp == nil {
		return 0, fmt.Errorf("no response available")
	}
	return resp.Body.Read(buf)
}

// Close cancels the context and closes the response body.
func (h *httpHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.phase == httpPhaseClosed {
		return nil
	}
	h.phase = httpPhaseClosed
	// Close the response body before canceling: net/http only returns a
	// finished request's connection to the transport's keep-alive pool once
	// the body is closed with nothing left unread. Canceling first would race
	// that handoff and make the transport tear the connection down instead —
	// silently defeating the whole point of caching the client for reuse.
	if h.resp != nil {
		if err := h.resp.Body.Close(); err != nil {
			logger.Default().Warn("close HTTP response body", zap.Error(err))
		}
	}
	h.cancel()
	return nil
}

// openHTTPResourceWithClient prepares an HTTP request against a pre-built,
// possibly cached client — the keep-alive-reuse counterpart of
// OpenHTTPResource (io_handle.go), used by DefaultHostProvider.OpenIO once it
// has resolved (or reused) the asset's client via httpClientCache. auth, when
// set, is injected into the hops that target the asset's endpoint.
func openHTTPResourceWithClient(params IOOpenParams, client *http.Client, auth *requestAuth) (*IOResource, error) {
	h, err := newHandleFromClient(params, client, auth)
	if err != nil {
		return nil, err
	}
	return &IOResource{
		Reader: &httpReadAdapter{h: h},
		Writer: &httpWriteAdapter{h: h},
		Closer: &httpCloseAdapter{h: h},
		http:   h,
	}, nil
}
