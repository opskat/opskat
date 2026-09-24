package extension

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// authSeen is what one server received on one request.
type authSeen struct {
	path, authorization, token string
}

// authRecorder is an HTTP server that records the credentials each request
// carried and never echoes them back.
type authRecorder struct {
	*httptest.Server
	mu   sync.Mutex
	seen []authSeen
}

func newAuthRecorder(t *testing.T) *authRecorder {
	t.Helper()
	rec := &authRecorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.seen = append(rec.seen, authSeen{path: r.URL.Path, authorization: r.Header.Get("Authorization"), token: r.URL.Query().Get("token")})
		rec.mu.Unlock()
		switch r.URL.Path {
		case "/redirect-in":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/hangup":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	t.Cleanup(rec.Close)
	return rec
}

func (r *authRecorder) requests() []authSeen {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]authSeen(nil), r.seen...)
}

// newAuthFixture loads the fixture extension (network.assetEndpoint declared,
// no credentials:read) whose asset names endpoint and picks authType. other is
// reachable through the static allowlist — a target that is not the endpoint.
func newAuthFixture(t *testing.T, endpoint, other, authType string) *Plugin {
	t.Helper()
	manifest := fixtureManifest(t)
	manifest.Capabilities.Network.AssetEndpoint = true
	manifest.Capabilities.Tunnel = true
	manifest.Capabilities.HTTP.Allowlist = []string{other + "/"}
	inner := NewDefaultHostProvider(DefaultHostConfig{
		AssetConfigs: assetConfigs{fixtureAsset.ID: mustJSON(t, map[string]any{
			"endpoint": endpoint, "authType": authType, "username": "elastic", "password": "s3cret",
		})},
	})
	return loadDescribedFixture(t, manifest, inner)
}

// undecryptable fails every credential read, as a stored secret that no longer
// decrypts does in the host.
type undecryptable struct{ assetConfigs }

func (undecryptable) AssetCredentialValues(context.Context, int64, []string) (map[string]string, error) {
	return nil, errors.New("decrypt password field \"password\": cipher: message authentication failed")
}

func TestAssetAuthInjection(t *testing.T) {
	Convey("Given an asset type declaring auth groups selected by authType", t, func() {
		ctx := context.Background()
		endpoint := newAuthRecorder(t)
		other := newAuthRecorder(t)
		basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("elastic:s3cret"))
		signed := base64.StdEncoding.EncodeToString([]byte("elastic:s3cret"))

		Convey("basic: the endpoint receives Authorization: Basic user:password", func() {
			p := newAuthFixture(t, endpoint.URL, other.URL, "basic")
			callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": endpoint.URL + "/ok"})
			So(endpoint.requests(), ShouldResemble, []authSeen{{path: "/ok", authorization: basic}})
		})

		Convey("bearer: the endpoint receives the rendered header", func() {
			p := newAuthFixture(t, endpoint.URL, other.URL, "bearer")
			callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": endpoint.URL + "/ok"})
			So(endpoint.requests(), ShouldResemble, []authSeen{{path: "/ok", authorization: "Bearer s3cret"}})
		})

		Convey("signed: the query parameter is injected on every hop that stays on the endpoint", func() {
			p := newAuthFixture(t, endpoint.URL, other.URL, "signed")
			out := callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": endpoint.URL + "/redirect-in?q=1"})
			So(out["body"], ShouldEqual, "ok")
			So(endpoint.requests(), ShouldResemble, []authSeen{
				{path: "/redirect-in", token: signed},
				{path: "/ok", token: signed},
			})
		})

		Convey("a selector value no group names injects nothing", func() {
			p := newAuthFixture(t, endpoint.URL, other.URL, "none")
			callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": endpoint.URL + "/ok"})
			So(endpoint.requests(), ShouldResemble, []authSeen{{path: "/ok"}})
		})

		Convey("a target that is not the endpoint receives no credentials", func() {
			p := newAuthFixture(t, endpoint.URL, other.URL, "basic")
			out := callToolOn(t, p, fixtureAsset, "http_get", map[string]any{"url": other.URL + "/ok"})
			So(out["body"], ShouldEqual, "ok")
			So(other.requests(), ShouldResemble, []authSeen{{path: "/ok"}})
			So(endpoint.requests(), ShouldBeEmpty)
		})

		Convey("credentials that cannot be decrypted fail the request before it is sent", func() {
			manifest := fixtureManifest(t)
			manifest.Capabilities.Network.AssetEndpoint = true
			inner := NewDefaultHostProvider(DefaultHostConfig{AssetConfigs: undecryptable{assetConfigs{
				fixtureAsset.ID: mustJSON(t, map[string]any{"endpoint": endpoint.URL, "authType": "basic", "username": "elastic", "password": "garbled"}),
			}}})
			p := loadDescribedFixture(t, manifest, inner)
			_, err := p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": endpoint.URL + "/ok"}), fixtureAsset)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "credentials of asset")
			So(endpoint.requests(), ShouldBeEmpty)
		})

		Convey("the guest never observes the injected values", func() {
			p := newAuthFixture(t, endpoint.URL, other.URL, "signed")
			raw, err := p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": endpoint.URL + "/ok"}), fixtureAsset)
			So(err, ShouldBeNil)
			So(endpoint.requests(), ShouldResemble, []authSeen{{path: "/ok", token: signed}})
			So(string(raw), ShouldNotContainSubstring, signed)

			// A failed request's error travels back to the guest: it must name
			// the URL the guest asked for, not the one carrying the credential.
			_, err = p.CallTool(ctx, "http_get", mustJSON(t, map[string]any{"url": endpoint.URL + "/hangup"}), fixtureAsset)
			So(err, ShouldNotBeNil)
			So(endpoint.requests()[1], ShouldResemble, authSeen{path: "/hangup", token: signed})
			So(err.Error(), ShouldNotContainSubstring, "token=")
			So(err.Error(), ShouldNotContainSubstring, "s3cret")
		})
	})
}
