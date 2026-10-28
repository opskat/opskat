package extension

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"testing"

	"github.com/opskat/opskat/pkg/extmanifest"

	. "github.com/smartystreets/goconvey/convey"
)

// recordingHost captures OpenIO calls to verify delegation through capHost.
// Non-OpenIO methods embed HostProvider (nil) and panic if called; the tests
// below only exercise OpenIO.
type recordingHost struct {
	HostProvider
	lastParams IOOpenParams
	opened     int
}

func (r *recordingHost) OpenIO(_ context.Context, _ *AssetRef, params IOOpenParams) (*IOResource, error) {
	r.opened++
	r.lastParams = params
	return &IOResource{}, nil
}

// scopedCtx is the context of a host call made inside a call bound to scope.
func scopedCtx(scope *Scope) context.Context {
	return withInvocation(context.Background(), newInvocation("cap-test", nil).bindScope(scope))
}

// A TCP stream used to be ungated for an extension that did not declare
// network.assetEndpoint. It now needs an endpoint of the call's scope or an
// asset route, like every other network target.
func TestCapHostRefusesTCPOutsideTheScope(t *testing.T) {
	Convey("Given a capHost wrapping a manifest with no capabilities", t, func() {
		inner := &recordingHost{}
		manifest := &Manifest{Manifest: extmanifest.Manifest{Name: "no-caps", Version: "1.0.0"}}
		ch := NewCapabilityHost(inner, manifest, "/tmp/ext")

		Convey("OpenIO(type=tcp) from a call with no asset is refused and never reaches the inner host", func() {
			_, err := ch.OpenIO(scopedCtx(&Scope{}), nil, IOOpenParams{Type: "tcp", Addr: "example.com:9092"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not scoped to an asset")
			So(inner.opened, ShouldEqual, 0)
		})

		Convey("OpenIO(type=http) is still gated by the allowlist", func() {
			_, err := ch.OpenIO(scopedCtx(&Scope{}), nil, IOOpenParams{Type: "http", URL: "https://example.com/foo"})
			So(err, ShouldNotBeNil) // allowlist is empty, so reject
			So(inner.opened, ShouldEqual, 0)
		})
	})
}

// Compile-time assertion that recordingHost satisfies HostProvider.
var _ HostProvider = (*recordingHost)(nil)

// endpointScope is the scope of a call on asset 7 whose type names these
// endpoints, reached directly.
func endpointScope(values ...string) *Scope {
	scope := &Scope{
		Asset: &AssetRef{ID: 7, Name: "a", Type: "ep"},
		Route: &Route{Dial: (&net.Dialer{}).DialContext, Direct: true},
	}
	for _, v := range values {
		if ep, ok := ParseEndpoint(v); ok {
			scope.Endpoints = append(scope.Endpoints, ep)
		}
	}
	return scope
}

func endpointManifest() *Manifest {
	return &Manifest{Manifest: extmanifest.Manifest{
		Name:        "ep",
		Version:     "1.0.0",
		Permissions: extmanifest.Permissions{Network: extmanifest.NetworkPermission{AssetEndpoint: true}},
	}}
}

func TestCapHostAssetEndpointGate(t *testing.T) {
	Convey("Given an extension declaring network.assetEndpoint and a call scoped to an asset", t, func() {
		inner := &recordingHost{}
		manifest := endpointManifest()
		ch := NewCapabilityHost(inner, manifest, "/tmp/ext")
		scope := endpointScope("https://ES.internal:9200/base", "https://api.example.com", "10.0.0.5:9092", "[fd00::1]:9300", "")
		scope.TLS = &tls.Config{ServerName: "es.internal"}
		scope.Fingerprint = "fp"
		open := func(p IOOpenParams) error {
			_, err := ch.OpenIO(scopedCtx(scope), scope.Asset, p)
			return err
		}

		Convey("HTTP to an endpoint matches on scheme, case-insensitive host and port, and takes the asset's path", func() {
			for _, u := range []string{
				"https://es.internal:9200/_search",
				"https://es.internal:9200",
				"https://api.example.com/v1",
				"https://api.example.com:443/v1",
				"http://10.0.0.5:9092/",
				"https://[fd00::1]:9300/",
			} {
				So(open(IOOpenParams{Type: "http", URL: u}), ShouldBeNil)
				So(inner.lastParams.AllowPrivate, ShouldBeTrue)
				So(inner.lastParams.RedirectGuard, ShouldNotBeNil)
				So(inner.lastParams.Dial, ShouldNotBeNil)
				So(inner.lastParams.TLS, ShouldEqual, scope.TLS)
				So(inner.lastParams.Fingerprint, ShouldEqual, "fp")
			}
		})

		Convey("HTTP to anything else is rejected as not an endpoint", func() {
			for _, u := range []string{
				"http://es.internal:9200/",  // scheme differs
				"https://es.internal:9201/", // port differs
				"https://es.internal/",      // default port differs
				"http://api.example.com/",   // scheme differs
				"https://10.0.0.9:22/",
				"https://evil.example.com/",
			} {
				err := open(IOOpenParams{Type: "http", URL: u})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			}
			So(inner.opened, ShouldEqual, 0)
		})

		Convey("the redirect guard admits endpoints only", func() {
			So(open(IOOpenParams{Type: "http", URL: "https://es.internal:9200/"}), ShouldBeNil)
			guard := inner.lastParams.RedirectGuard
			ok, _ := url.Parse("https://es.internal:9200/other")
			bad, _ := url.Parse("http://169.254.169.254/latest")
			So(guard(ok), ShouldBeNil)
			So(guard(bad).Error(), ShouldContainSubstring, "not an endpoint of the asset")
		})

		Convey("the scope's credential injection rides only on endpoint requests", func() {
			scope.Auth = func(context.Context) ([]AuthValue, error) { return nil, nil }
			So(open(IOOpenParams{Type: "http", URL: "https://es.internal:9200/"}), ShouldBeNil)
			So(inner.lastParams.Auth, ShouldNotBeNil)
			manifest.Permissions.Network.HTTP = []string{"https://public.example.com/"}
			So(open(IOOpenParams{Type: "http", URL: "https://public.example.com/x"}), ShouldBeNil)
			So(inner.lastParams.Auth, ShouldBeNil)
		})

		Convey("a guest cannot pre-set allowPrivate for a non-endpoint target", func() {
			err := open(IOOpenParams{Type: "http", URL: "http://10.0.0.9:22/", AllowPrivate: true})
			So(err, ShouldNotBeNil)
		})

		Convey("TCP to an endpoint's host and port is allowed, anything else rejected", func() {
			So(open(IOOpenParams{Type: "tcp", Addr: "10.0.0.5:9092"}), ShouldBeNil)
			So(inner.lastParams.TLS, ShouldEqual, scope.TLS)
			So(open(IOOpenParams{Type: "tcp", Addr: "es.internal:9200"}), ShouldBeNil)
			So(open(IOOpenParams{Type: "tcp", Addr: "api.example.com:443"}), ShouldBeNil)
			So(open(IOOpenParams{Type: "tcp", Addr: "[fd00::1]:9300"}), ShouldBeNil)
			for _, addr := range []string{"10.0.0.5:9093", "10.0.0.9:22", "es.internal:9201", "garbage"} {
				err := open(IOOpenParams{Type: "tcp", Addr: addr})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			}
		})

		Convey("a call not scoped to an asset has no endpoints", func() {
			_, err := ch.OpenIO(scopedCtx(&Scope{}), nil, IOOpenParams{Type: "tcp", Addr: "10.0.0.5:9092"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not scoped to an asset")
			_, err = ch.OpenIO(scopedCtx(&Scope{}), nil, IOOpenParams{Type: "http", URL: "https://es.internal:9200/"})
			So(err, ShouldNotBeNil)
		})

		Convey("the static allowlist still admits its URLs, directly and without private reach", func() {
			manifest.Permissions.Network.HTTP = []string{"https://public.example.com/"}
			So(open(IOOpenParams{Type: "http", URL: "https://public.example.com/x", AllowPrivate: true}), ShouldBeNil)
			So(inner.lastParams.AllowPrivate, ShouldBeFalse)
			So(inner.lastParams.RedirectGuard, ShouldBeNil)
			So(inner.lastParams.Dial, ShouldBeNil)
			So(inner.lastParams.TLS, ShouldBeNil)
		})
	})
}

// network.assetRoute opens every other address of a call that has a connection
// path — along that path, never with the asset's TLS settings or credentials —
// and still nothing for a call without one.
func TestCapHostAssetRouteGate(t *testing.T) {
	Convey("Given an extension declaring network.assetRoute", t, func() {
		inner := &recordingHost{}
		manifest := &Manifest{Manifest: extmanifest.Manifest{Name: "route", Version: "1.0.0",
			Permissions: extmanifest.Permissions{Network: extmanifest.NetworkPermission{AssetRoute: true}}}}
		ch := NewCapabilityHost(inner, manifest, "/tmp/ext")
		scope := endpointScope("es.internal:9200")
		scope.TLS = &tls.Config{}
		scope.Auth = func(context.Context) ([]AuthValue, error) { return nil, nil }

		Convey("TCP and HTTP to another address go along the route, without the asset's TLS or credentials", func() {
			_, err := ch.OpenIO(scopedCtx(scope), scope.Asset, IOOpenParams{Type: "tcp", Addr: "node2.internal:9300"})
			So(err, ShouldBeNil)
			So(inner.lastParams.Dial, ShouldNotBeNil)
			So(inner.lastParams.TLS, ShouldBeNil)
			_, err = ch.OpenIO(scopedCtx(scope), scope.Asset, IOOpenParams{Type: "http", URL: "https://node2.internal:9200/"})
			So(err, ShouldBeNil)
			So(inner.lastParams.Dial, ShouldNotBeNil)
			So(inner.lastParams.Auth, ShouldBeNil)
			So(inner.lastParams.TLS, ShouldBeNil)
		})

		Convey("the asset's own endpoint still needs network.assetEndpoint", func() {
			_, err := ch.OpenIO(scopedCtx(scope), scope.Asset, IOOpenParams{Type: "tcp", Addr: "es.internal:9200"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "permissions.network.assetEndpoint")
			_, err = ch.OpenIO(scopedCtx(scope), scope.Asset, IOOpenParams{Type: "http", URL: "http://es.internal:9200/"})
			So(err, ShouldNotBeNil)
			So(inner.opened, ShouldEqual, 0)
		})

		Convey("a call without a connection path dials nothing along one", func() {
			_, err := ch.OpenIO(scopedCtx(&Scope{Asset: &AssetRef{ID: 9}}), nil, IOOpenParams{Type: "tcp", Addr: "node2.internal:9300"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "no connection path")
			So(inner.opened, ShouldEqual, 0)
		})
	})
}

// A direct route reaches this machine and its link only when the asset itself
// is there: an asset the user reaches directly cannot point the extension at
// the user's own machine.
func TestCapHostRouteDialDirectRefusesThisMachine(t *testing.T) {
	Convey("Given a direct route to a public asset", t, func() {
		var dialed []string
		ch := NewCapabilityHost(&recordingHost{}, &Manifest{Manifest: extmanifest.Manifest{Name: "route"}}, "/tmp/ext").(*capHost)
		scope := &Scope{
			Asset:     &AssetRef{ID: 9},
			Endpoints: []Endpoint{{Host: "203.0.113.10", Port: "5432"}},
			Route: &Route{Direct: true, Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
				dialed = append(dialed, addr)
				return nil, errors.New("not really dialing")
			}},
		}
		dial := ch.routeDial(newInvocation("t", nil).bindScope(scope), scope)

		Convey("loopback and link-local targets are refused by name, undialed", func() {
			for _, addr := range []string{"127.0.0.1:1", "[::1]:1", "169.254.1.1:1", "0.0.0.0:1"} {
				_, err := dial(context.Background(), "tcp", addr)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, addr)
				So(err.Error(), ShouldContainSubstring, "loopback or link-local")
			}
			So(dialed, ShouldBeEmpty)
		})

		Convey("a public target is dialed by its address", func() {
			_, err := dial(context.Background(), "tcp", "203.0.113.20:5432")
			So(err.Error(), ShouldContainSubstring, "not really dialing")
			So(dialed, ShouldResemble, []string{"203.0.113.20:5432"})
		})

		Convey("on a loopback asset, loopback targets are allowed", func() {
			scope.Endpoints = []Endpoint{{Host: "127.0.0.1", Port: "5432"}}
			_, _ = dial(context.Background(), "tcp", "127.0.0.1:7401")
			So(dialed, ShouldResemble, []string{"127.0.0.1:7401"})
		})
	})
}

// A call's scope is resolved once, on its first network or ticket host call —
// a failure fails only those, never the call's other host calls.
func TestCapHostResolvesTheScopeOnDemand(t *testing.T) {
	Convey("Given a call on an asset whose scope cannot be resolved", t, func() {
		host := newRecordedHost()
		host.configs[7] = json.RawMessage(`{"own":true}`)
		resolves := 0
		inner := &scopedHost{HostProvider: host, scopes: func(context.Context, string, AssetRef) (*Scope, error) {
			resolves++
			return nil, errors.New("ssh tunnel asset 3 unreachable")
		}}
		ch := NewCapabilityHost(inner, endpointManifest(), "/tmp/ext")
		inv := newInvocation("t", nil).scopedTo(&AssetRef{ID: 7, Name: "a", Type: "ep"})
		call := func(req string) ([]byte, error) {
			return hostCall(withInvocation(context.Background(), inv), ch, []byte(req))
		}

		Convey("log, KV and asset config still work, and resolve nothing", func() {
			_, err := call(`{"op":"log","params":{"level":"info","msg":"hi"}}`)
			So(err, ShouldBeNil)
			_, err = call(`{"op":"kv.get","params":{"key":"k"}}`)
			So(err, ShouldBeNil)
			out, err := call(`{"op":"asset.get_config","params":{}}`)
			So(err, ShouldBeNil)
			So(string(out), ShouldEqual, `{"own":true}`)
			So(resolves, ShouldEqual, 0)
		})

		Convey("network and ticket calls fail with the reason, resolved only once", func() {
			_, err := call(`{"op":"io.open","params":{"type":"tcp","addr":"10.0.0.5:9092"}}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ssh tunnel asset 3 unreachable")
			_, err = call(`{"op":"auth.kerberos_ticket","params":{"spn":"a/b"}}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "ssh tunnel asset 3 unreachable")
			So(resolves, ShouldEqual, 1)
		})
	})
}

func TestEndpointFieldsFromSchema(t *testing.T) {
	Convey("EndpointFieldsFromSchema lists format:endpoint properties, sorted", t, func() {
		schema := map[string]any{"properties": map[string]any{
			"z":    map[string]any{"format": "endpoint"},
			"a":    map[string]any{"format": "endpoint"},
			"pass": map[string]any{"format": "password"},
			"name": map[string]any{"type": "string"},
		}}
		So(EndpointFieldsFromSchema(schema), ShouldResemble, []string{"a", "z"})
		So(EndpointFieldsFromSchema(nil), ShouldBeEmpty)
	})
}
