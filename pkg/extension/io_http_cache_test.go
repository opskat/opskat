// pkg/extension/io_http_cache_test.go
package extension

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// countingConnListener wraps a listener, counting accepted (i.e. newly
// established) connections — the signal that keep-alive was NOT reused.
type countingConnListener struct {
	net.Listener
	accepts *atomic.Int32
}

func (l *countingConnListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.accepts.Add(1)
	}
	return conn, err
}

// newCountingServer starts an httptest server whose accepted-connection count
// is observable, so a test can tell whether a second call reused keep-alive or
// opened a fresh TCP connection.
func newCountingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var accepts atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Listener = &countingConnListener{Listener: ln, accepts: &accepts}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &accepts
}

// openAndDrain performs one HTTP open through h, fully reads the response body
// (required for the underlying connection to become eligible for keep-alive
// reuse) and closes the handle.
func openAndDrain(t *testing.T, h *DefaultHostProvider, asset *AssetRef, url string) {
	t.Helper()
	res, err := h.OpenIO(context.Background(), asset, IOOpenParams{Type: "http", Method: "GET", URL: url, AllowPrivate: true})
	if err != nil {
		t.Fatalf("OpenIO: %v", err)
	}
	if _, err := res.http.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if _, err := io.ReadAll(res.Reader); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := res.Closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestDefaultHostProviderHTTPClientReuse(t *testing.T) {
	Convey("Given a host provider and a server counting new TCP connections", t, func() {
		srv, accepts := newCountingServer(t)

		Convey("two calls for the same asset reuse one keep-alive connection", func() {
			h := NewDefaultHostProvider(DefaultHostConfig{})
			asset := &AssetRef{ID: 42, Type: "fixture"}
			openAndDrain(t, h, asset, srv.URL)
			openAndDrain(t, h, asset, srv.URL)
			So(accepts.Load(), ShouldEqual, int32(1))
		})

		Convey("two different assets get separate connections", func() {
			h := NewDefaultHostProvider(DefaultHostConfig{})
			openAndDrain(t, h, &AssetRef{ID: 1, Type: "fixture"}, srv.URL)
			openAndDrain(t, h, &AssetRef{ID: 2, Type: "fixture"}, srv.URL)
			So(accepts.Load(), ShouldEqual, int32(2))
		})

		Convey("two different provider instances (different extensions) get separate connections", func() {
			h1 := NewDefaultHostProvider(DefaultHostConfig{})
			h2 := NewDefaultHostProvider(DefaultHostConfig{})
			asset := &AssetRef{ID: 1, Type: "fixture"}
			openAndDrain(t, h1, asset, srv.URL)
			openAndDrain(t, h2, asset, srv.URL)
			So(accepts.Load(), ShouldEqual, int32(2))
		})

		Convey("a non-asset open is never cached: every call gets its own connection", func() {
			h := NewDefaultHostProvider(DefaultHostConfig{})
			openAndDrain(t, h, nil, srv.URL)
			openAndDrain(t, h, nil, srv.URL)
			So(accepts.Load(), ShouldEqual, int32(2))
		})

		Convey("invalidating the asset drops the cached client: the next call reconnects", func() {
			h := NewDefaultHostProvider(DefaultHostConfig{})
			asset := &AssetRef{ID: 42, Type: "fixture"}
			openAndDrain(t, h, asset, srv.URL)
			h.InvalidateAssetHTTPClient(asset.ID)
			openAndDrain(t, h, asset, srv.URL)
			So(accepts.Load(), ShouldEqual, int32(2))
		})
	})
}

// fingerprintDialer returns a fixed fingerprint (and no dial override) so a
// test can flip it between calls to simulate the asset's connection settings
// changing without a config write reaching the invalidation path.
type fingerprintDialer struct{ fingerprint string }

func (d *fingerprintDialer) DialContextFor(context.Context, int64) (DialContextFunc, *tls.Config, string, error) {
	return nil, nil, d.fingerprint, nil
}

func TestDefaultHostProviderHTTPClientFingerprint(t *testing.T) {
	Convey("Given an asset whose resolved connection fingerprint can change", t, func() {
		srv, accepts := newCountingServer(t)
		asset := &AssetRef{ID: 42, Type: "fixture"}

		Convey("same fingerprint across calls reuses the client", func() {
			dialer := &fingerprintDialer{fingerprint: "tunnel-a"}
			h := NewDefaultHostProvider(DefaultHostConfig{AssetDialer: dialer})
			openAndDrain(t, h, asset, srv.URL)
			openAndDrain(t, h, asset, srv.URL)
			So(accepts.Load(), ShouldEqual, int32(1))
		})

		Convey("a changed fingerprint rebuilds the client even without an explicit invalidation", func() {
			dialer := &fingerprintDialer{fingerprint: "tunnel-a"}
			h := NewDefaultHostProvider(DefaultHostConfig{AssetDialer: dialer})
			openAndDrain(t, h, asset, srv.URL)
			dialer.fingerprint = "tunnel-b"
			openAndDrain(t, h, asset, srv.URL)
			So(accepts.Load(), ShouldEqual, int32(2))
		})
	})
}

// closeTrackingListener additionally counts closed connections, so a test can
// observe that a discarded client's idle connection was actively released
// rather than left to expire on its own timeout.
type closeTrackingConn struct {
	net.Conn
	closes *atomic.Int32
}

func (c *closeTrackingConn) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

type closeTrackingListener struct {
	net.Listener
	closes *atomic.Int32
}

func (l *closeTrackingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &closeTrackingConn{Conn: conn, closes: l.closes}, nil
}

func newCloseTrackingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var closes atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Listener = &closeTrackingListener{Listener: ln, closes: &closes}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &closes
}

// waitForCount polls counter for up to one second: net/http hands a finished
// response's connection back to the transport's idle pool from the request's
// own read-loop goroutine, so CloseIdleConnections (triggered by our
// registry/discard hooks right after the caller's Close returns) can race that
// handoff by a few milliseconds even though the close itself is not in doubt.
func waitForCount(counter *atomic.Int32, want int32) bool {
	deadline := time.Now().Add(time.Second)
	for {
		if counter.Load() == want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestDefaultHostProviderHTTPCacheLifecycle(t *testing.T) {
	Convey("Given a cached client with an idle keep-alive connection", t, func() {
		srv, closes := newCloseTrackingServer(t)
		asset := &AssetRef{ID: 42, Type: "fixture"}

		Convey("re-registering the same extension name (a reinstall) closes the superseded provider's idle connections", func() {
			h1 := NewDefaultHostProvider(DefaultHostConfig{ExtensionName: "dup-ext"})
			openAndDrain(t, h1, asset, srv.URL)

			_ = NewDefaultHostProvider(DefaultHostConfig{ExtensionName: "dup-ext"})
			So(waitForCount(closes, 1), ShouldBeTrue)
		})

		Convey("DiscardHostHTTPCache closes idle connections and forgets the extension", func() {
			h := NewDefaultHostProvider(DefaultHostConfig{ExtensionName: "disable-ext"})
			openAndDrain(t, h, asset, srv.URL)

			So(DiscardHostHTTPCache("disable-ext"), ShouldBeTrue)
			So(waitForCount(closes, 1), ShouldBeTrue)
			So(DiscardHostHTTPCache("disable-ext"), ShouldBeFalse)
		})

		Convey("an extension name never registered has nothing to discard", func() {
			So(DiscardHostHTTPCache("never-registered-ext"), ShouldBeFalse)
		})
	})
}

// A cached client outlives the call that built it, so whatever decides where a
// request may go must come from the call making it — never from the first call
// that happened to build the client. An asset whose endpoint moved from A to B
// (the invalidation hook never reached this provider) must not keep following
// redirects back to A.
func TestDefaultHostProviderCachedClientUsesEachCallsRedirectGuard(t *testing.T) {
	Convey("Given an asset's cached client built by a call whose guard admitted the old endpoint", t, func() {
		oldEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("old"))
		}))
		t.Cleanup(oldEndpoint.Close)
		newEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, oldEndpoint.URL, http.StatusFound)
		}))
		t.Cleanup(newEndpoint.Close)

		onlyTo := func(allowed string) func(*url.URL) error {
			return func(target *url.URL) error {
				if "http://"+target.Host == allowed {
					return nil
				}
				return fmt.Errorf("redirect to %s denied", target.Host)
			}
		}
		h := NewDefaultHostProvider(DefaultHostConfig{})
		asset := &AssetRef{ID: 42, Type: "fixture"}
		first, err := h.OpenIO(context.Background(), asset, IOOpenParams{
			Type: "http", Method: "GET", URL: oldEndpoint.URL, AllowPrivate: true, RedirectGuard: onlyTo(oldEndpoint.URL),
		})
		So(err, ShouldBeNil)
		_, err = first.http.Flush()
		So(err, ShouldBeNil)
		So(first.Closer.Close(), ShouldBeNil)

		Convey("a later call whose guard admits only the new endpoint refuses the redirect back", func() {
			second, err := h.OpenIO(context.Background(), asset, IOOpenParams{
				Type: "http", Method: "GET", URL: newEndpoint.URL, AllowPrivate: true, RedirectGuard: onlyTo(newEndpoint.URL),
			})
			So(err, ShouldBeNil)
			_, err = second.http.Flush()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "denied")
			So(second.Closer.Close(), ShouldBeNil)
		})
	})
}
