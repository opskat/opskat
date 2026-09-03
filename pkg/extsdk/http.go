package opskat

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// HTTPTransport implements net/http.RoundTripper using host IO handles.
type HTTPTransport struct{}

// NewHTTPTransport creates a new HTTP transport backed by host IO.
func NewHTTPTransport() *HTTPTransport {
	return &HTTPTransport{}
}

// RoundTrip executes a single HTTP request using the IO handle system:
// IOOpen("http") -> IOWrite(body) -> IOFlush() -> construct http.Response -> IORead(body)
func (t *HTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	headers := make(map[string]any)
	for k := range req.Header {
		headers[k] = req.Header.Get(k)
	}

	h, err := IOOpen("http", map[string]any{
		"method":  req.Method,
		"url":     req.URL.String(),
		"headers": headers,
	})
	if err != nil {
		return nil, fmt.Errorf("io open http: %w", err)
	}

	// Write request body (if any)
	if req.Body != nil {
		if _, err := io.Copy(h, req.Body); err != nil {
			closeQuietly(h)
			closeQuietly(req.Body)
			return nil, fmt.Errorf("write request body: %w", err)
		}
		closeQuietly(req.Body)
	}

	// Flush: sends request, receives response headers
	meta, err := h.Flush()
	if err != nil {
		closeQuietly(h)
		return nil, fmt.Errorf("flush http: %w", err)
	}

	// Build http.Response from metadata + handle as body reader
	resp := &http.Response{
		StatusCode:    meta.Status,
		Status:        fmt.Sprintf("%d %s", meta.Status, http.StatusText(meta.Status)),
		Header:        make(http.Header),
		ContentLength: meta.Size,
		Body:          &ioHandleReadCloser{h: h},
		Request:       req,
	}

	for k, v := range meta.Headers {
		resp.Header.Set(k, v)
	}
	if resp.ContentLength > 0 {
		resp.Header.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}

	return resp, nil
}

// ioHandleReadCloser wraps IOHandle as io.ReadCloser for http.Response.Body.
type ioHandleReadCloser struct {
	h *IOHandle
}

func (r *ioHandleReadCloser) Read(p []byte) (int, error) {
	return r.h.Read(p)
}

func (r *ioHandleReadCloser) Close() error {
	return r.h.Close()
}

// closeQuietly closes a resource on an error path, where the close error can
// only mask the failure the caller is already reporting.
func closeQuietly(c io.Closer) {
	_ = c.Close()
}
