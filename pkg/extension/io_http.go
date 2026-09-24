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
	hasBody bool // true for POST/PUT/PATCH
}

// newHTTPHandle creates an HTTP handle ready for writing (POST/PUT/PATCH)
// or immediate flushing (GET/HEAD/DELETE/OPTIONS). tlsConfig, when set, is the
// asset's declared TLS settings — net/http performs its own handshake using it
// for an https:// URL, over the conn dial returns.
func newHTTPHandle(params IOOpenParams, dial DialFunc, tlsConfig *tls.Config) (*httpHandle, error) {
	method := strings.ToUpper(params.Method)
	if method == "" {
		method = "GET"
	}

	if params.URL == "" {
		return nil, fmt.Errorf("URL is required for HTTP handle")
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Build transport; clone default so we don't mutate the global one.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	var baseDial func(ctx context.Context, network, addr string) (net.Conn, error)
	if dial != nil {
		baseDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dial(network, addr)
		}
	} else {
		baseDial = transport.DialContext
	}
	// Always wrap with the dial-time guard to catch DNS rebinding after URL-level checks.
	transport.DialContext = dialGuard(baseDial, params.AllowPrivate)
	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}

	hasBody := method == "POST" || method == "PUT" || method == "PATCH"

	client := &http.Client{Transport: transport}
	if guard := params.RedirectGuard; guard != nil {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			// Keep net/http's own redirect limit; the guard only narrows where to.
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return guard(req.URL)
		}
	}

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

	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequestWithContext(h.ctx, h.method, h.url, body)
	} else {
		req, err = http.NewRequestWithContext(h.ctx, h.method, h.url, nil)
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

	resp, err := client.Do(req) //nolint:bodyclose // body is read by Read() and closed by Close()
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	h.mu.Lock()
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
	h.cancel()
	if h.resp != nil {
		if err := h.resp.Body.Close(); err != nil {
			logger.Default().Warn("close HTTP response body", zap.Error(err))
		}
	}
	return nil
}
