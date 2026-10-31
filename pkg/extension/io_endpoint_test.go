package extension

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/opskat/opskat/pkg/extwire"
	. "github.com/smartystreets/goconvey/convey"
)

// assetConfigs is an AssetConfigGetter over a fixed map — the host-side config
// reader the real app scopes to the calling extension.
type assetConfigs map[int64]json.RawMessage

func (a assetConfigs) GetAssetConfig(assetID int64) (json.RawMessage, error) {
	cfg, ok := a[assetID]
	if !ok {
		return nil, fmt.Errorf("no config for asset %d", assetID)
	}
	return cfg, nil
}

// AssetCredentialValues serves the named fields as stored: this fake keeps
// password fields in plaintext, standing in for the host's decryption.
func (a assetConfigs) AssetCredentialValues(_ context.Context, assetID int64, fields []string) (map[string]string, error) {
	raw, err := a.GetAssetConfig(assetID)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		if v, ok := cfg[f].(string); ok {
			out[f] = v
		}
	}
	return out, nil
}

var fixtureAsset = &AssetRef{ID: 1, Name: "fixture-1", Type: "fixture"}

// fixtureConfigs is what the fixture's scope resolver reads an asset from: its
// stored config, and its fields as credential injection sees them.
type fixtureConfigs interface {
	GetAssetConfig(assetID int64) (json.RawMessage, error)
	AssetCredentialValues(ctx context.Context, assetID int64, fields []string) (map[string]string, error)
}

// providerConfigs reads both off a host provider's config getter: a fake that
// keeps credentials in plaintext, like assetConfigs.
type providerConfigs struct{ HostProvider }

func (p providerConfigs) AssetCredentialValues(_ context.Context, assetID int64, fields []string) (map[string]string, error) {
	raw, err := p.GetAssetConfig(assetID)
	if err != nil {
		return nil, err
	}
	return fixtureConfigFieldValues(raw, fields)
}

// credentialValues reads the named fields of an asset's credentials.
type credentialValues func(ctx context.Context, fields []string) (map[string]string, error)

// fixtureScopes stands in for the asset-type side (internal/extreg AssetScope,
// fed by extension_svc's resolver): the scope of a call on a fixture asset is the
// addresses its type's format:"endpoint" fields name in its config, conn as its
// connection path (nil: direct), and its type's auth groups rendered from its
// credentials (fixtureAuth) — an ad-hoc call's all read from its AdHoc config.
func fixtureScopes(manifest *Manifest, configs fixtureConfigs, conn *fakeAssetDialer) ScopeResolver {
	return func(ctx context.Context, _ string, a AssetRef) (*Scope, error) {
		f, err := decodeFace(manifest)
		if err != nil {
			return nil, err
		}
		def := f.assetType(a.Type)
		if def == nil {
			return nil, fmt.Errorf("asset type %q is not the extension's", a.Type)
		}
		var raw json.RawMessage
		var values credentialValues
		if a.AdHoc != nil {
			raw = a.AdHoc.Config
			values = func(_ context.Context, fields []string) (map[string]string, error) {
				return fixtureConfigFieldValues(a.AdHoc.Credentials, fields)
			}
		} else {
			if raw, err = configs.GetAssetConfig(a.ID); err != nil {
				return nil, err
			}
			values = func(ctx context.Context, fields []string) (map[string]string, error) {
				return configs.AssetCredentialValues(ctx, a.ID, fields)
			}
		}
		scope := &Scope{Asset: &a, Route: &Route{Dial: (&net.Dialer{}).DialContext, Direct: true}}
		var cfg map[string]any
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
		for _, field := range schemaFieldsWithFormat(def.ConfigSchema, "endpoint") {
			if value, ok := cfg[field].(string); ok {
				if ep, ok := ParseEndpoint(value); ok {
					scope.Endpoints = append(scope.Endpoints, ep)
				}
			}
		}
		if conn != nil {
			// Built only when a stream takes it, as the host builds it.
			direct := scope.Route
			scope.Route = nil
			scope.Connect = func(context.Context) (Connection, error) {
				dial, tlsConfig, fingerprint, err := conn.connection(&a)
				if err != nil {
					return Connection{}, err
				}
				route := direct
				if dial != nil {
					route = &Route{Dial: dial}
				}
				return Connection{Route: route, TLS: tlsConfig, Fingerprint: fingerprint}, nil
			}
		}
		if def.Auth != nil {
			if scope.Auth, err = fixtureAuth(def.Auth, values); err != nil {
				return nil, err
			}
		}
		return scope, nil
	}
}

// fixtureAuth renders the auth groups the fixture declares — basic, bearer and
// signed, selected by authType — into what the endpoint receives. Rendering a
// declaration's templates is the asset-type side's (extreg's AuthDef.Inject, tested
// there with these same groups); the runtime only injects what a scope's Auth
// answers, so this renders the fixture's three groups by hand, refusing a
// declaration that has drifted from them.
func fixtureAuth(declared *extwire.Auth, values credentialValues) (RequestAuth, error) {
	render := map[string]func(v map[string]string) AuthValue{
		"basic": func(v map[string]string) AuthValue {
			return AuthValue{In: AuthInBasic, Value: v["username"] + ":" + v["password"]}
		},
		"bearer": func(v map[string]string) AuthValue {
			return AuthValue{In: AuthInHeader, Name: "Authorization", Value: "Bearer " + v["password"]}
		},
		"signed": func(v map[string]string) AuthValue {
			return AuthValue{In: AuthInQuery, Name: "token", Value: base64.StdEncoding.EncodeToString([]byte(v["username"] + ":" + v["password"]))}
		},
	}
	if declared.Selector != "authType" || len(declared.Groups) != len(render) {
		return nil, fmt.Errorf("the fixture's auth declaration changed: %+v", declared)
	}
	for _, g := range declared.Groups {
		if render[g.When] == nil {
			return nil, fmt.Errorf("the fixture's auth declaration changed: group %q", g.When)
		}
	}
	return func(ctx context.Context) ([]AuthValue, error) {
		v, err := values(ctx, []string{"authType", "username", "password"})
		if err != nil {
			return nil, err
		}
		if r := render[v["authType"]]; r != nil {
			return []AuthValue{r(v)}, nil
		}
		return nil, nil
	}, nil
}

// fixtureHost is a DefaultHostProvider over configs whose scopes fixtureScopes
// resolves.
func fixtureHost(manifest *Manifest, configs fixtureConfigs, conn *fakeAssetDialer) *DefaultHostProvider {
	return NewDefaultHostProvider(DefaultHostConfig{AssetConfigs: configs, Scopes: fixtureScopes(manifest, configs, conn)})
}

// scopedHost gives an inner provider that resolves no scopes the fixture's.
type scopedHost struct {
	HostProvider
	scopes ScopeResolver
}

func (h *scopedHost) resolveScope(ctx context.Context, a *AssetRef) (*Scope, error) {
	return h.scopes(ctx, "fixture-ext", *a)
}

// newEndpointFixture loads the fixture extension over a real DefaultHostProvider
// with capability enforcement.
func newEndpointFixture(t *testing.T, assetEndpoint bool, config map[string]any) *Plugin {
	t.Helper()
	manifest := fixtureManifest(t)
	manifest.Permissions.Network.AssetEndpoint = assetEndpoint
	inner := NewDefaultHostProvider(DefaultHostConfig{
		AssetConfigs: assetConfigs{fixtureAsset.ID: mustJSON(t, config)},
	})
	return loadDescribedFixture(t, manifest, inner)
}

// loadDescribedFixture loads the fixture extension over inner wrapped in
// capability enforcement, and merges describe() onto manifest the way
// Manager.LoadExtension does — the fixture's scope resolver reads the asset
// type's declaration from there. An inner provider that resolves no
// scopes of its own gets fixtureScopes over its own config getter.
func loadDescribedFixture(t *testing.T, manifest *Manifest, inner HostProvider, opts ...PluginOption) *Plugin {
	t.Helper()
	if d, ok := inner.(*DefaultHostProvider); !ok || d.cfg.Scopes == nil {
		inner = &scopedHost{HostProvider: inner, scopes: fixtureScopes(manifest, providerConfigs{inner}, nil)}
	}
	p, err := LoadPlugin(context.Background(), manifest, fixtureWasm(t), NewCapabilityHost(inner, manifest, t.TempDir()), nil, opts...)
	if err != nil {
		t.Fatalf("load fixture plugin: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Errorf("close plugin: %v", err)
		}
	})
	payload, err := p.Describe(context.Background())
	if err != nil {
		t.Fatalf("describe fixture: %v", err)
	}
	desc, err := ParseDescriptor(payload)
	if err != nil {
		t.Fatalf("parse fixture descriptor: %v", err)
	}
	manifest.apply(desc)
	return p
}

// echoListener accepts connections and echoes the first read back.
func echoListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				_, _ = c.Write(buf[:n]) // echo; a failed write surfaces as the guest's read error
			}()
		}
	}()
	return ln
}

func TestAssetEndpointNetworkGate(t *testing.T) {
	Convey("Given an asset whose endpoint fields name loopback (private) servers", t, func() {
		ctx := context.Background()
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("other"))
		}))
		defer other.Close()
		endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/redirect-out":
				http.Redirect(w, r, other.URL+"/", http.StatusFound)
			case "/redirect-in":
				http.Redirect(w, r, "/ok", http.StatusFound)
			default:
				_, _ = w.Write([]byte("endpoint"))
			}
		}))
		defer endpoint.Close()
		broker := echoListener(t)
		stranger := echoListener(t)

		// The fixture's one endpoint field names the HTTP server for the HTTP
		// cases and the echo listener for the TCP cases.
		httpConfig := map[string]any{"endpoint": endpoint.URL}
		tcpConfig := map[string]any{"endpoint": broker.Addr().String()}

		Convey("with network.assetEndpoint declared", func() {
			p := newEndpointFixture(t, true, httpConfig)
			tp := newEndpointFixture(t, true, tcpConfig)

			Convey("an HTTP request to the endpoint is allowed although it is loopback", func() {
				out := callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": endpoint.URL + "/ok"})
				So(out["status"], ShouldEqual, 200)
				So(out["body"], ShouldEqual, "endpoint")
			})

			Convey("a redirect that stays on the endpoint is followed", func() {
				out := callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": endpoint.URL + "/redirect-in"})
				So(out["body"], ShouldEqual, "endpoint")
			})

			Convey("an HTTP request to a non-endpoint target is rejected", func() {
				_, err := p.CallTool(ctx, ToolCall{Name: "http_get", Args: mustJSON(t, map[string]any{"url": other.URL + "/"}), Asset: fixtureAsset})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			})

			Convey("a redirect to a non-endpoint target is rejected", func() {
				_, err := p.CallTool(ctx, ToolCall{Name: "http_get", Args: mustJSON(t, map[string]any{"url": endpoint.URL + "/redirect-out"}), Asset: fixtureAsset})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			})

			Convey("a TCP connection to a host:port endpoint is allowed", func() {
				out := callToolOn(t, tp, fixtureAsset, "tcp_echo", map[string]any{"addr": broker.Addr().String()})
				So(out["echo"], ShouldEqual, "ping")
			})

			Convey("a TCP connection to a non-endpoint address is rejected", func() {
				_, err := tp.CallTool(ctx, ToolCall{Name: "tcp_echo", Args: mustJSON(t, map[string]any{"addr": stranger.Addr().String()}), Asset: fixtureAsset})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			})

			Convey("a call not scoped to an asset reaches no endpoint", func() {
				_, err := p.CallTool(ctx, ToolCall{Name: "http_get", Args: mustJSON(t, map[string]any{"url": endpoint.URL + "/ok"})})
				So(err, ShouldNotBeNil)
				_, err = tp.CallTool(ctx, ToolCall{Name: "tcp_echo", Args: mustJSON(t, map[string]any{"addr": broker.Addr().String()})})
				So(err, ShouldNotBeNil)
			})
		})

		Convey("an earlier allowlisted request on the same asset does not loosen the endpoint's redirect rule", func() {
			// Both requests are scoped to the same asset. The allowlisted one is not
			// an endpoint request, so whatever client serves it must not be the one
			// the endpoint's later requests reuse.
			manifest := fixtureManifest(t)
			manifest.Permissions.Network.AssetEndpoint = true
			manifest.Permissions.Network.HTTP = []string{other.URL + "/"}
			manifest.Permissions.Network.AllowPrivate = true // the allowlisted server is loopback
			p := loadDescribedFixture(t, manifest, NewDefaultHostProvider(DefaultHostConfig{
				AssetConfigs: assetConfigs{fixtureAsset.ID: mustJSON(t, httpConfig)},
			}))

			out := callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": other.URL + "/"})
			So(out["body"], ShouldEqual, "other")

			_, err := p.CallTool(ctx, ToolCall{Name: "http_get", Args: mustJSON(t, map[string]any{"url": endpoint.URL + "/redirect-out"}), Asset: fixtureAsset})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
		})

		Convey("without network.assetEndpoint, reach is the static allowlist as before", func() {
			p := newEndpointFixture(t, false, httpConfig)
			tp := newEndpointFixture(t, false, tcpConfig)

			Convey("the endpoint URL is refused by the allowlist", func() {
				_, err := p.CallTool(ctx, ToolCall{Name: "http_get", Args: mustJSON(t, map[string]any{"url": endpoint.URL + "/ok"}), Asset: fixtureAsset})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not in permissions.network.http")
			})

			Convey("TCP to the endpoint is refused naming the missing permission", func() {
				_, err := tp.CallTool(ctx, ToolCall{Name: "tcp_echo", Args: mustJSON(t, map[string]any{"addr": broker.Addr().String()}), Asset: fixtureAsset})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "does not declare permissions.network.assetEndpoint")
			})

			Convey("TCP to any other address is refused too: TCP is no longer ungated", func() {
				_, err := tp.CallTool(ctx, ToolCall{Name: "tcp_echo", Args: mustJSON(t, map[string]any{"addr": stranger.Addr().String()}), Asset: fixtureAsset})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
				_, err = tp.CallTool(ctx, ToolCall{Name: "tcp_echo", Args: mustJSON(t, map[string]any{"addr": stranger.Addr().String()})})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not scoped to an asset")
			})
		})
	})
}

// fixtureConfigFieldValues reads the named config fields as strings, the way the
// asset-type side (internal/extreg) renders credential values.
func fixtureConfigFieldValues(config json.RawMessage, fields []string) (map[string]string, error) {
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		raw, ok := cfg[field]
		if !ok || string(raw) == "null" {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			s = string(raw)
		}
		values[field] = s
	}
	return values, nil
}

// schemaPropertyNames lists a configSchema's declared properties, sorted.
func schemaPropertyNames(schema map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// schemaFieldsWithFormat lists the properties of a configSchema declaring format, sorted.
func schemaFieldsWithFormat(schema map[string]any, format string) []string {
	props, _ := schema["properties"].(map[string]any)
	var fields []string
	for name, v := range props {
		if prop, ok := v.(map[string]any); ok && prop["format"] == format {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}
