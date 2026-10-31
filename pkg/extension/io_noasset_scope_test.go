package extension

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	cagologger "github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// noAssetFixture is the fixture extension over a host that resolves, for a call
// without an asset, the scope the host's extension management resolves from the
// extension's settings: endpoint (by permissions.network.settingsEndpoint), auth
// injected into requests to it, and config as the settings' read view. Its asset
// keeps its own scope (fixtureScopes): asset's endpoint, bearer auth. allowlisted
// is in permissions.network.http. resolves counts the no-asset resolutions.
type noAssetFixture struct {
	p        *Plugin
	resolves int
	mu       sync.Mutex
}

func newNoAssetFixture(t *testing.T, settingsEndpoint bool, endpoint, assetEndpoint, allowlisted string, auth []AuthValue, config json.RawMessage) *noAssetFixture {
	t.Helper()
	manifest := fixtureManifest(t)
	manifest.Permissions.Network.SettingsEndpoint = settingsEndpoint
	manifest.Permissions.Network.AllowPrivate = true // the allowlisted server is loopback
	manifest.Permissions.Network.HTTP = []string{allowlisted + "/"}
	configs := assetConfigs{fixtureAsset.ID: mustJSON(t, map[string]any{
		"endpoint": assetEndpoint, "authType": "bearer", "password": "asset-secret",
	})}
	f := &noAssetFixture{}
	host := NewDefaultHostProvider(DefaultHostConfig{
		ExtensionName: "fixture-ext",
		AssetConfigs:  configs,
		Scopes:        fixtureScopes(manifest, configs, nil),
		NoAssetScopes: func(_ context.Context, ext string) (*Scope, error) {
			f.mu.Lock()
			f.resolves++
			f.mu.Unlock()
			if ext != "fixture-ext" {
				t.Errorf("no-asset scope resolved for %q", ext)
			}
			ep, ok := ParseEndpoint(endpoint)
			if !ok {
				t.Fatalf("parse endpoint %q", endpoint)
			}
			return &Scope{
				Endpoints:          []Endpoint{ep},
				EndpointPermission: "settingsEndpoint",
				Auth:               func(context.Context) ([]AuthValue, error) { return auth, nil },
				Config:             config,
			}, nil
		},
	})
	f.p = loadDescribedFixture(t, manifest, host)
	return f
}

func (f *noAssetFixture) resolved() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resolves
}

func (f *noAssetFixture) get(t *testing.T, asset *AssetRef, target string) (map[string]any, error) {
	t.Helper()
	raw, err := f.p.CallTool(context.Background(), ToolCall{Name: "http_get", Args: mustJSON(t, map[string]any{"url": target}), Asset: asset})
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// settingsService is the service a settings endpoint names: it records what each
// request carried and redirects /redirect-in onto itself, /redirect-out to
// elsewhere and /echo-out to elsewhere echoing the request URI (query included).
func settingsService(t *testing.T, elsewhere string) *authRecorder {
	t.Helper()
	rec := &authRecorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.seen = append(rec.seen, authSeen{path: r.URL.Path, authorization: r.Header.Get("Authorization"), token: r.URL.Query().Get("token")})
		rec.mu.Unlock()
		switch r.URL.Path {
		case "/redirect-in":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/redirect-out":
			http.Redirect(w, r, elsewhere+"/landed", http.StatusFound)
		case "/echo-out":
			http.Redirect(w, r, elsewhere+r.URL.RequestURI(), http.StatusFound) //nolint:gosec // G710: the echoing redirect is the server behavior under test
		default:
			_, _ = w.Write([]byte("service"))
		}
	}))
	t.Cleanup(rec.Close)
	return rec
}

var bearerSettingsSecret = []AuthValue{{In: AuthInHeader, Name: "Authorization", Value: "Bearer settings-secret"}}

func TestNoAssetScopeNetworkGate(t *testing.T) {
	Convey("Given a call without an asset whose scope names a loopback service endpoint", t, func() {
		elsewhere := newAuthRecorder(t) // allowlisted, not an endpoint
		stranger := newAuthRecorder(t)  // neither
		assetSide := newAuthRecorder(t) // the asset's own endpoint
		service := settingsService(t, elsewhere.URL)
		config := json.RawMessage(`{"values":{"model":"fast"},"secrets":{"apiKey":true}}`)

		Convey("with network.settingsEndpoint declared", func() {
			f := newNoAssetFixture(t, true, service.URL, assetSide.URL, elsewhere.URL, bearerSettingsSecret, config)

			Convey("a request to the endpoint is reached, carrying the injected credentials", func() {
				out, err := f.get(t, nil, service.URL+"/ok")
				So(err, ShouldBeNil)
				So(out["status"], ShouldEqual, 200)
				So(out["body"], ShouldEqual, "service")
				So(service.requests(), ShouldResemble, []authSeen{{path: "/ok", authorization: "Bearer settings-secret"}})
			})

			Convey("a redirect that stays on the endpoint is followed with the credentials", func() {
				out, err := f.get(t, nil, service.URL+"/redirect-in")
				So(err, ShouldBeNil)
				So(out["body"], ShouldEqual, "service")
				So(service.requests(), ShouldHaveLength, 2)
				So(service.requests()[1].authorization, ShouldEqual, "Bearer settings-secret")
			})

			Convey("a redirect off the endpoint fails, even to an allowlisted address, which sees nothing", func() {
				_, err := f.get(t, nil, service.URL+"/redirect-out")
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "is not an endpoint of this call's scope")
				So(elsewhere.requests(), ShouldBeEmpty)
			})

			Convey("an allowlisted address is reached as before, with nothing injected", func() {
				out, err := f.get(t, nil, elsewhere.URL+"/x")
				So(err, ShouldBeNil)
				So(out["status"], ShouldEqual, 200)
				So(elsewhere.requests(), ShouldResemble, []authSeen{{path: "/x"}})
			})

			Convey("any other address is refused by the allowlist", func() {
				_, err := f.get(t, nil, stranger.URL+"/x")
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not in permissions.network.http")
				So(stranger.requests(), ShouldBeEmpty)
			})

			Convey("TCP still needs an asset", func() {
				_, err := f.p.CallTool(context.Background(), ToolCall{Name: "tcp_echo", Args: mustJSON(t, map[string]any{"addr": service.Listener.Addr().String()})})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not scoped to an asset")
			})

			Convey("a call on an asset keeps the asset's scope: its endpoint with its credentials, and not the call-without-asset endpoint", func() {
				out, err := f.get(t, fixtureAsset, assetSide.URL+"/ok")
				So(err, ShouldBeNil)
				So(out["status"], ShouldEqual, 200)
				So(assetSide.requests(), ShouldResemble, []authSeen{{path: "/ok", authorization: "Bearer asset-secret"}})

				_, err = f.get(t, fixtureAsset, service.URL+"/ok")
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
				So(service.requests(), ShouldBeEmpty)
				So(f.resolved(), ShouldEqual, 0)
			})
		})

		Convey("a query credential echoed into a refused redirect never reaches the guest", func() {
			f := newNoAssetFixture(t, true, service.URL, assetSide.URL, elsewhere.URL,
				[]AuthValue{{In: AuthInQuery, Name: "token", Value: "query-secret"}}, config)

			_, err := f.get(t, nil, service.URL+"/echo-out?q=1")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "is not an endpoint of this call's scope")
			So(err.Error(), ShouldNotContainSubstring, "query-secret")
			So(service.requests(), ShouldResemble, []authSeen{{path: "/echo-out", token: "query-secret"}})
			So(elsewhere.requests(), ShouldBeEmpty)
		})

		Convey("without network.settingsEndpoint the endpoint is refused, logged naming the extension and the target", func() {
			core, logs := observer.New(zap.WarnLevel)
			prev := cagologger.Default()
			cagologger.SetLogger(zap.New(core))
			defer cagologger.SetLogger(prev)
			f := newNoAssetFixture(t, false, service.URL, assetSide.URL, elsewhere.URL, bearerSettingsSecret, config)

			_, err := f.get(t, nil, service.URL+"/ok")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `does not declare permissions.network.settingsEndpoint`)
			So(err.Error(), ShouldNotContainSubstring, "settings-secret")
			So(service.requests(), ShouldBeEmpty)

			denied := logs.FilterMessage("extension network target denied").All()
			So(denied, ShouldHaveLength, 1)
			fields := denied[0].ContextMap()
			So(fields["extension"], ShouldEqual, "fixture-ext")
			So(fields["target"], ShouldEqual, service.URL)
			So(fields, ShouldNotContainKey, "assetID")

			Convey("while the allowlist still admits its own addresses", func() {
				out, err := f.get(t, nil, elsewhere.URL+"/x")
				So(err, ShouldBeNil)
				So(out["status"], ShouldEqual, 200)
				So(elsewhere.requests(), ShouldResemble, []authSeen{{path: "/x"}})
			})
		})
	})
}

// Only a call the host scopes — a tool call, with or without an asset — resolves
// a scope. A describe, check_policy or validate_config call never does: it
// reaches the allowlist only, and reads no config.
func TestOnlyHostScopedCallsResolveTheNoAssetScope(t *testing.T) {
	Convey("Given a host that resolves a scope for calls without an asset", t, func() {
		resolves := 0
		host := NewDefaultHostProvider(DefaultHostConfig{NoAssetScopes: func(context.Context, string) (*Scope, error) {
			resolves++
			return &Scope{Config: json.RawMessage(`{"values":{},"secrets":{}}`)}, nil
		}})
		ch := NewCapabilityHost(host, endpointManifest(), "/tmp/ext")
		config := func(inv *invocation) ([]byte, error) {
			return hostCall(withInvocation(context.Background(), inv), ch, []byte(`{"op":"config.get"}`))
		}

		Convey("a tool call without an asset resolves it", func() {
			out, err := config(newInvocation("t#1", nil).scopedTo(nil))
			So(err, ShouldBeNil)
			So(string(out), ShouldEqual, `{"values":{},"secrets":{}}`)
			So(resolves, ShouldEqual, 1)
		})

		Convey("a call the host never scoped resolves nothing", func() {
			_, err := config(newInvocation("t#2", nil))
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "carries no config")
			So(resolves, ShouldEqual, 0)
		})
	})
}

func TestScopeConfigHostCall(t *testing.T) {
	Convey("Given the fixture over a host that gives a call without an asset a config", t, func() {
		ctx := context.Background()
		svc := newAuthRecorder(t)
		config := json.RawMessage(`{"values":{"model":"fast","threshold":0.8},"secrets":{"apiKey":true,"spare":false}}`)
		f := newNoAssetFixture(t, true, svc.URL, svc.URL, svc.URL, bearerSettingsSecret, config)

		Convey("a call without an asset reads it", func() {
			out := callToolOn(t, f.p, nil, "scope_config", map[string]any{})
			So(out, ShouldResemble, map[string]any{
				"values":  map[string]any{"model": "fast", "threshold": 0.8},
				"secrets": map[string]any{"apiKey": true, "spare": false},
			})
			So(f.resolved(), ShouldEqual, 1)
		})

		Convey("a call on an asset reads none: its scope carries no config", func() {
			_, err := f.p.CallTool(ctx, ToolCall{Name: "scope_config", Args: mustJSON(t, map[string]any{}), Asset: fixtureAsset})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "carries no config")
			So(err.Error(), ShouldContainSubstring, "asset.get_config")
			So(f.resolved(), ShouldEqual, 0)
		})
	})

	Convey("Given a host that resolves no scope for a call without an asset", t, func() {
		p := newEndpointFixture(t, true, map[string]any{"endpoint": "http://127.0.0.1:1"})

		_, err := p.CallTool(context.Background(), ToolCall{Name: "scope_config", Args: json.RawMessage(`{}`)})
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "carries no config")
	})
}
