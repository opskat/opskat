package extension

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
)

// callBound is how long a timed-out or canceled call may take to come back. It
// is far above the deadlines the tests set and far below "never", which is what
// a guest blocked inside a host IO call used to mean.
const callBound = 5 * time.Second

// stallingTCP accepts connections and never answers, so a guest reading from one
// blocks inside the host until something closes the connection. arrived fires
// once per accepted connection.
func stallingTCP(t *testing.T) (net.Listener, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	arrived := make(chan struct{}, 16)
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			arrived <- struct{}{}
		}
	}()
	return ln, arrived
}

// stallingHTTP answers every request by blocking until the test ends — before
// any response when headersFirst is false (the guest blocks in Flush), after the
// headers and a first body chunk when it is true (the guest blocks reading the
// body).
func stallingHTTP(t *testing.T, headersFirst bool) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	arrived := make(chan struct{}, 16)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if headersFirst {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
		}
		arrived <- struct{}{}
		<-release
	}))
	// Cleanups run last-in first-out: release the handlers, then close the server.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv, arrived
}

// callAsync runs one tool call in the background so a test can bound how long
// it takes to come back.
func callAsync(ctx context.Context, p *Plugin, tool string, args json.RawMessage) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := p.CallTool(ctx, tool, args, fixtureAsset)
		done <- err
	}()
	return done
}

func waitCall(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(callBound):
		t.Fatalf("call blocked in host IO did not return within %s", callBound)
		return nil
	}
}

func waitArrival(t *testing.T, arrived <-chan struct{}) {
	t.Helper()
	select {
	case <-arrived:
	case <-time.After(callBound):
		t.Fatal("the guest's request never reached the server")
	}
}

// assertSlotFreed proves the one instance slot came back: with a pool of one, a
// slot the stuck call still held would make this call wait forever.
func assertSlotFreed(t *testing.T, p *Plugin) {
	t.Helper()
	err := waitCall(t, callAsync(context.Background(), p, "echo", mustJSON(t, map[string]any{"msg": "next"})))
	So(err, ShouldBeNil)
}

// TestBlockedHostIOIsInterrupted is the regression guard for a guest stuck inside
// a host function. wazero's CloseOnContextDone only acts while the guest runs
// bytecode, so a guest blocked in a TCP read with no deadline, or in an HTTP
// round trip / body read, used to ignore both the call's deadline and its
// cancellation — and kept its instance slot for good.
func TestBlockedHostIOIsInterrupted(t *testing.T) {
	Convey("Given a plugin with a single instance slot", t, func() {
		opts := []PluginOption{WithMaxInstances(1)}

		Convey("a tool blocked on a TCP read", func() {
			ln, arrived := stallingTCP(t)
			newPlugin := func(extra ...PluginOption) *Plugin {
				return newEndpointFixtureWith(t, map[string]any{"endpoint": ln.Addr().String()}, append(opts, extra...)...)
			}
			args := mustJSON(t, map[string]any{"addr": ln.Addr().String()})

			Convey("is interrupted by the call's timeout, and frees its slot", func() {
				p := newPlugin(WithToolTimeout(300 * time.Millisecond))
				So(waitCall(t, callAsync(context.Background(), p, "tcp_echo", args)), ShouldNotBeNil)
				assertSlotFreed(t, p)
			})

			Convey("is interrupted by canceling the call, and frees its slot", func() {
				p := newPlugin()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := callAsync(ctx, p, "tcp_echo", args)
				waitArrival(t, arrived)
				cancel()
				So(waitCall(t, done), ShouldNotBeNil)
				assertSlotFreed(t, p)
			})
		})

		for _, c := range []struct {
			name         string
			headersFirst bool
		}{
			{"a tool blocked waiting for an HTTP response", false},
			{"a tool blocked reading an HTTP response body", true},
		} {
			Convey(c.name, func() {
				srv, arrived := stallingHTTP(t, c.headersFirst)
				newPlugin := func(extra ...PluginOption) *Plugin {
					return newEndpointFixtureWith(t, map[string]any{"endpoint": srv.URL}, append(opts, extra...)...)
				}
				args := mustJSON(t, map[string]any{"url": srv.URL + "/stall"})

				Convey("is interrupted by the call's timeout, and frees its slot", func() {
					p := newPlugin(WithToolTimeout(300 * time.Millisecond))
					So(waitCall(t, callAsync(context.Background(), p, "http_get", args)), ShouldNotBeNil)
					assertSlotFreed(t, p)
				})

				Convey("is interrupted by canceling the call, and frees its slot", func() {
					p := newPlugin()
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := callAsync(ctx, p, "http_get", args)
					waitArrival(t, arrived)
					cancel()
					So(waitCall(t, done), ShouldNotBeNil)
					assertSlotFreed(t, p)
				})
			})
		}
	})
}

// TestCancelActionInterruptsBlockedHostIO covers the action side: an action has
// no host deadline, so the cancellation flag it polls is the only way to stop it
// — and a guest blocked in a host read never gets to poll it.
func TestCancelActionInterruptsBlockedHostIO(t *testing.T) {
	Convey("Given an action blocked on a TCP read, on a plugin with one slot", t, func() {
		ln, arrived := stallingTCP(t)
		p := newEndpointFixtureWith(t, map[string]any{"endpoint": ln.Addr().String()}, WithMaxInstances(1))

		done := make(chan error, 1)
		go func() {
			_, err := p.CallAction(context.Background(), "blocked-1", "tcp_echo",
				mustJSON(t, map[string]any{"addr": ln.Addr().String()}), fixtureAsset)
			done <- err
		}()
		waitArrival(t, arrived)

		Convey("canceling it fails the blocked read, ends the action and frees its slot", func() {
			So(p.CancelAction("blocked-1"), ShouldBeTrue)
			So(waitCall(t, done), ShouldNotBeNil)
			assertSlotFreed(t, p)
		})
	})
}

// newEndpointFixtureWith is newEndpointFixture with plugin options, for tests
// that need a small pool or a short deadline.
func newEndpointFixtureWith(t *testing.T, config map[string]any, opts ...PluginOption) *Plugin {
	t.Helper()
	manifest := fixtureManifest(t)
	inner := NewDefaultHostProvider(DefaultHostConfig{
		AssetConfigs: assetConfigs{fixtureAsset.ID: mustJSON(t, config)},
	})
	return loadDescribedFixture(t, manifest, inner, opts...)
}

// roundTripFunc lets a test decide what the transport does mid round trip.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type closeTrackingBody struct {
	closed chan struct{}
}

func (b *closeTrackingBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *closeTrackingBody) Close() error {
	close(b.closed)
	return nil
}

// TestHTTPHandleClosedDuringRoundTrip covers the race the interrupt opens up: the
// invocation's handles are closed while a round trip is in flight, and the
// response lands after Close already ran. Close had nothing to release then, so
// the response — and the connection behind its body — must be released by Flush
// instead of being handed to a handle nobody will ever read or close again.
func TestHTTPHandleClosedDuringRoundTrip(t *testing.T) {
	Convey("Given an HTTP handle closed while its request is in flight", t, func() {
		body := &closeTrackingBody{closed: make(chan struct{})}
		var h *httpHandle
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			So(h.Close(), ShouldBeNil)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}, nil
		})}
		var err error
		h, err = newHandleFromClient(IOOpenParams{URL: "http://example.invalid/"}, client, nil)
		So(err, ShouldBeNil)

		_, err = h.Flush()

		Convey("Flush fails and closes the late response body", func() {
			So(err, ShouldNotBeNil)
			select {
			case <-body.closed:
			default:
				t.Fatal("the late response body was never closed")
			}
		})
	})
}

// TestToolDeclaredTimeout covers a tool's own timeout from describe(): it
// replaces the host default for that tool's calls, in both directions, while
// tools that declare none keep the default.
func TestToolDeclaredTimeout(t *testing.T) {
	Convey("Given the described fixture", t, func() {
		Convey("a tool declaring a timeout shorter than the default is cut off at its own", func() {
			p := newEndpointFixtureWith(t, map[string]any{"endpoint": "127.0.0.1:1"})
			start := time.Now()
			err := waitCall(t, callAsync(context.Background(), p, "spin_short", mustJSON(t, map[string]any{"ms": 20000})))
			So(err, ShouldNotBeNil)
			So(time.Since(start), ShouldBeLessThan, callBound)
		})

		Convey("a tool declaring a timeout longer than the default is allowed to finish", func() {
			p := newEndpointFixtureWith(t, map[string]any{"endpoint": "127.0.0.1:1"}, WithToolTimeout(150*time.Millisecond))
			So(waitCall(t, callAsync(context.Background(), p, "spin_long", mustJSON(t, map[string]any{"ms": 600}))), ShouldBeNil)

			Convey("while a tool without a declaration still gets the default", func() {
				So(waitCall(t, callAsync(context.Background(), p, "spin", mustJSON(t, map[string]any{"ms": 3000}))), ShouldNotBeNil)
			})
		})
	})
}

// TestToolResultSizeLimit: a result over the host's limit fails the call with an
// error that says so, rather than reaching the caller truncated.
func TestToolResultSizeLimit(t *testing.T) {
	Convey("Given a plugin whose result limit is 4 KiB", t, func() {
		p := newFixturePlugin(t, newRecordedHost(), t.TempDir(), WithMaxResultBytes(4096))

		Convey("a result under the limit is returned whole", func() {
			out := callTool(t, p, "blob", map[string]any{"bytes": 1000})
			So(len(out["data"].(string)), ShouldEqual, 1000)
		})

		Convey("a result over the limit fails the call and names the limit", func() {
			_, err := p.CallTool(context.Background(), "blob", mustJSON(t, map[string]any{"bytes": 8000}), nil)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "blob")
			So(err.Error(), ShouldContainSubstring, "4096")
		})
	})
}
