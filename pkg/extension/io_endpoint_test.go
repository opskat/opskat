package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

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

var fixtureAsset = &AssetRef{ID: 1, Name: "fixture-1", Type: "fixture"}

// newEndpointFixture loads the fixture extension over a real DefaultHostProvider
// with capability enforcement, and fills its functional face from describe() the
// way Manager.LoadExtension does — the endpoint gate reads the asset type's
// configSchema from there.
func newEndpointFixture(t *testing.T, assetEndpoint bool, config map[string]any) *Plugin {
	t.Helper()
	manifest := fixtureManifest(t)
	manifest.Capabilities.Network.AssetEndpoint = assetEndpoint
	inner := NewDefaultHostProvider(DefaultHostConfig{
		AssetConfigs: assetConfigs{fixtureAsset.ID: mustJSON(t, config)},
	})
	p, err := LoadPlugin(context.Background(), manifest, fixtureWasm(t), NewCapabilityHost(inner, manifest, t.TempDir()), nil)
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

		config := map[string]any{
			"endpoint": endpoint.URL,
			"broker":   broker.Addr().String(),
			"note":     other.URL, // not an endpoint field: must not widen reach
		}

		Convey("with network.assetEndpoint declared", func() {
			p := newEndpointFixture(t, true, config)

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
				_, err := p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": other.URL + "/"}), fixtureAsset)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			})

			Convey("a redirect to a non-endpoint target is rejected", func() {
				_, err := p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": endpoint.URL + "/redirect-out"}), fixtureAsset)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			})

			Convey("a TCP connection to a host:port endpoint is allowed", func() {
				out := callToolOn(t, p, fixtureAsset, "tcp_echo", map[string]any{"addr": broker.Addr().String()})
				So(out["echo"], ShouldEqual, "ping")
			})

			Convey("a TCP connection to a non-endpoint address is rejected", func() {
				_, err := p.CallTool(ctx, "tcp_echo", mustJSON(t, map[string]any{"addr": stranger.Addr().String()}), fixtureAsset)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not an endpoint of the asset")
			})

			Convey("a call not scoped to an asset reaches no endpoint", func() {
				_, err := p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": endpoint.URL + "/ok"}), nil)
				So(err, ShouldNotBeNil)
				_, err = p.CallTool(ctx, "tcp_echo", mustJSON(t, map[string]any{"addr": broker.Addr().String()}), nil)
				So(err, ShouldNotBeNil)
			})
		})

		Convey("without network.assetEndpoint, reach is the static allowlist as before", func() {
			p := newEndpointFixture(t, false, config)

			Convey("the endpoint URL is refused by the allowlist", func() {
				_, err := p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": endpoint.URL + "/ok"}), fixtureAsset)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "not in capabilities.http.allowlist")
			})

			Convey("TCP stays ungated", func() {
				out := callToolOn(t, p, fixtureAsset, "tcp_echo", map[string]any{"addr": stranger.Addr().String()})
				So(out["echo"], ShouldEqual, "ping")
			})
		})
	})
}
