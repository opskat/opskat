package extension

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

// newTestConnectionFixture loads the fixture extension (which declares a
// test-connection handler on its "fixture" asset type) with assetEndpoint
// gating and an optional ad-hoc dialer.
func newTestConnectionFixture(t *testing.T, dialer AssetDialer) *Plugin {
	t.Helper()
	manifest := fixtureManifest(t)
	manifest.Capabilities.Network.AssetEndpoint = true
	inner := NewDefaultHostProvider(DefaultHostConfig{AssetDialer: dialer})
	return loadDescribedFixture(t, manifest, inner)
}

func TestPluginTestConnection(t *testing.T) {
	Convey("Given a fixture whose asset type declares a test-connection handler", t, func() {
		ctx := context.Background()

		Convey("describe() reports the declaration", func() {
			p := newTestConnectionFixture(t, nil)
			So(p.manifest.AssetTypeDef("fixture").TestConnection, ShouldBeTrue)
		})

		Convey("a reachable endpoint succeeds", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			p := newTestConnectionFixture(t, nil)

			err := p.TestConnection(ctx, "fixture", &AdHocAssetConfig{
				Config:      mustJSON(t, map[string]any{"endpoint": srv.URL}),
				Credentials: mustJSON(t, map[string]any{"endpoint": srv.URL}),
			})
			So(err, ShouldBeNil)
		})

		Convey("the handler's own failure (a non-2xx response) reaches the caller", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			p := newTestConnectionFixture(t, nil)

			err := p.TestConnection(ctx, "fixture", &AdHocAssetConfig{
				Config:      mustJSON(t, map[string]any{"endpoint": srv.URL}),
				Credentials: mustJSON(t, map[string]any{"endpoint": srv.URL}),
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "503")
		})

		Convey("an unknown asset type is refused", func() {
			p := newTestConnectionFixture(t, nil)
			err := p.TestConnection(ctx, "not-a-type", &AdHocAssetConfig{Config: mustJSON(t, map[string]any{})})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "unknown asset type")
		})

		Convey("the submitted connection settings dial through the ad-hoc path, not a database row", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			dialer := &fakeAssetDialer{farSide: srv.Listener.Addr().String()}
			p := newTestConnectionFixture(t, dialer)

			// .invalid never resolves locally: a direct dial (skipping the ad-hoc
			// dialer) would fail outright rather than reach srv.
			err := p.TestConnection(ctx, "fixture", &AdHocAssetConfig{
				Config:      mustJSON(t, map[string]any{"endpoint": "http://es.internal.invalid:9200"}),
				Credentials: mustJSON(t, map[string]any{"endpoint": "http://es.internal.invalid:9200"}),
			})
			So(err, ShouldBeNil)
			assets, addrs := dialer.dialed()
			So(assets, ShouldResemble, []int64{-1})
			So(addrs, ShouldResemble, []string{"es.internal.invalid:9200"})
		})

		Convey("basic auth declared on the type is injected into the test request", func() {
			var gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			p := newTestConnectionFixture(t, nil)

			config := mustJSON(t, map[string]any{
				"endpoint": srv.URL,
				"authType": "basic",
				"username": "admin",
				"password": "s3cr3t",
			})
			err := p.TestConnection(ctx, "fixture", &AdHocAssetConfig{Config: config, Credentials: config})
			So(err, ShouldBeNil)
			So(gotAuth, ShouldNotBeEmpty)
		})

		Convey("host-only credentials that cannot be read fail the test instead of sending it unauthenticated", func() {
			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			p := newTestConnectionFixture(t, nil)

			err := p.TestConnection(ctx, "fixture", &AdHocAssetConfig{
				Config:      mustJSON(t, map[string]any{"endpoint": srv.URL, "authType": "bearer"}),
				Credentials: []byte(`not json`),
			})
			So(err, ShouldNotBeNil)
			So(called, ShouldBeFalse)
		})

		Convey("credentials withheld from the guest's config are still injected from the host-only values", func() {
			// What the desktop builds for an extension without credentials:read:
			// the guest-visible config carries no password, the host keeps it for
			// injection.
			var gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			p := newTestConnectionFixture(t, nil)

			err := p.TestConnection(ctx, "fixture", &AdHocAssetConfig{
				Config: mustJSON(t, map[string]any{"endpoint": srv.URL, "authType": "bearer"}),
				Credentials: mustJSON(t, map[string]any{
					"endpoint": srv.URL, "authType": "bearer", "password": "s3cr3t",
				}),
			})
			So(err, ShouldBeNil)
			So(gotAuth, ShouldEqual, "Bearer s3cr3t")
		})
	})
}
