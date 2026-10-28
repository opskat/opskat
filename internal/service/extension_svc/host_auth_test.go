package extension_svc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// bearerAuth injects the asset's password field as a bearer token.
var bearerAuth = &extension.AuthDef{Groups: []extension.AuthGroup{{
	Bindings: []extension.AuthBinding{{In: "header", Name: "Authorization", Value: "Bearer {{password}}"}},
}}}

// authedExt is extension "authed": its type authed-store names its endpoint in
// "url" and injects the password as a bearer token.
func authedExt() *extension.Extension {
	return &extension.Extension{
		Name: "authed",
		Manifest: &extension.Manifest{
			Manifest: extmanifest.Manifest{Name: "authed"},
			AssetTypes: []extension.AssetTypeDef{{Type: "authed-store", Auth: bearerAuth, ConfigSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"host":     map[string]any{"type": "string"},
					"url":      map[string]any{"type": "string", "format": "endpoint"},
					"password": map[string]any{"type": "string", "format": "password"},
				},
			}}},
		},
	}
}

// ResolveScope is the asset-type side of a call's scope: the endpoints the type's
// endpoint fields name, the connection path, and the credential injection its
// auth groups render — for a stored asset of the extension's own type, or an
// ad-hoc one read from the call's own config.
func TestResolveScopeOfAnExtensionAsset(t *testing.T) {
	Convey("Given extension authed and its asset 6", t, func() {
		svc, assets := newHostTestService(t)
		svc.Bridge().Register(authedExt())
		enc, err := credential_svc.Default().Encrypt("theirs")
		So(err, ShouldBeNil)
		assets.EXPECT().Find(gomock.Any(), int64(6)).Return(&asset_entity.Asset{
			ID: 6, Type: "authed-store", Config: `{"host":"h","url":"https://ES.internal:9200/base","password":"` + enc + `"}`,
		}, nil).AnyTimes()
		ctx := context.Background()

		Convey("a stored asset's scope names its endpoints, a direct route and its rendered credentials", func() {
			scope, err := svc.ResolveScope(ctx, "authed", extension.AssetRef{ID: 6, Type: "authed-store"})
			So(err, ShouldBeNil)
			So(scope.Endpoints, ShouldResemble, []extension.Endpoint{{Scheme: "https", Host: "ES.internal", Port: "9200"}})
			So(scope.Route, ShouldNotBeNil)
			So(scope.Route.Direct, ShouldBeTrue)
			So(scope.TLS, ShouldBeNil)
			So(scope.Ops, ShouldBeNil)
			So(scope.Tickets, ShouldBeNil)
			values, err := scope.Auth(ctx)
			So(err, ShouldBeNil)
			So(values, ShouldResemble, []extension.AuthValue{{In: "header", Name: "Authorization", Value: "Bearer theirs"}})
		})

		Convey("an asset of another extension's type is refused", func() {
			_, err := svc.ResolveScope(ctx, "other", extension.AssetRef{ID: 6, Type: "authed-store"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "does not belong to extension")
		})

		Convey("an ad-hoc call reads endpoints and credentials from its own config", func() {
			scope, err := svc.ResolveScope(ctx, "authed", extension.AssetRef{Type: "authed-store", AdHoc: &extension.AdHocAssetConfig{
				Config:      []byte(`{"url":"10.0.0.5:9200"}`),
				Credentials: []byte(`{"url":"10.0.0.5:9200","password":"typed"}`),
			}})
			So(err, ShouldBeNil)
			So(scope.Endpoints, ShouldResemble, []extension.Endpoint{{Host: "10.0.0.5", Port: "9200"}})
			values, err := scope.Auth(ctx)
			So(err, ShouldBeNil)
			So(values, ShouldResemble, []extension.AuthValue{{In: "header", Name: "Authorization", Value: "Bearer typed"}})
		})

		Convey("an ad-hoc call of a type the extension does not register is refused", func() {
			_, err := svc.ResolveScope(ctx, "authed", extension.AssetRef{Type: "other-store", AdHoc: &extension.AdHocAssetConfig{}})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "does not register asset type")
		})
	})
}

func TestAssetCredentialValuesServeInjectionOnly(t *testing.T) {
	Convey("credential injection reads decrypted fields host-side, whatever the extension may read itself", t, func() {
		svc, assets := newHostTestService(t)

		Convey("an extension without credentials:read still gets the plaintext for injection", func() {
			assets.EXPECT().Find(gomock.Any(), int64(4)).
				Return(&asset_entity.Asset{ID: 4, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)
			values, err := configGetterOf(svc, "other").AssetCredentialValues(context.Background(), 4, []string{"host", "password"})
			So(err, ShouldBeNil)
			So(values, ShouldResemble, map[string]string{"host": "h", "password": "theirs"})
		})

		Convey("only the named fields are decrypted", func() {
			assets.EXPECT().Find(gomock.Any(), int64(5)).
				Return(&asset_entity.Asset{ID: 5, Type: "other-store", Config: `{"host":"h","password":"not-a-ciphertext"}`}, nil)
			values, err := configGetterOf(svc, "other").AssetCredentialValues(context.Background(), 5, []string{"host"})
			So(err, ShouldBeNil)
			So(values, ShouldResemble, map[string]string{"host": "h"})
		})

		Convey("another extension's asset is refused", func() {
			assets.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&asset_entity.Asset{ID: 3, Type: "acme-store", Config: encryptedConfig(t, "mine")}, nil)
			values, err := configGetterOf(svc, "other").AssetCredentialValues(context.Background(), 3, []string{"password"})
			So(err, ShouldNotBeNil)
			So(values, ShouldBeNil)
		})
	})
}

func TestCredentialInjectionDecryptFailureSendsNothing(t *testing.T) {
	Convey("a referenced password that does not decrypt fails the request before anything is sent", t, func() {
		svc, assets := newHostTestService(t)
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
		}))
		defer srv.Close()
		svc.Bridge().Register(authedExt())
		assets.EXPECT().Find(gomock.Any(), int64(5)).
			Return(&asset_entity.Asset{ID: 5, Type: "authed-store", Config: `{"host":"h","password":"not-a-ciphertext"}`}, nil).AnyTimes()

		asset := extension.AssetRef{ID: 5, Name: "store", Type: "authed-store"}
		scope, err := svc.ResolveScope(context.Background(), "authed", asset)
		So(err, ShouldBeNil)
		So(scope.Auth, ShouldNotBeNil)
		provider := extension.NewDefaultHostProvider(svc.HostConfig("authed"))
		res, err := provider.OpenIO(context.Background(), &asset, extension.IOOpenParams{
			Type: "http", Method: "GET", URL: srv.URL + "/",
			Auth: &extension.HTTPAuth{Credentials: scope.Auth, IsEndpoint: func(*url.URL) bool { return true }},
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "password")
		So(res, ShouldBeNil)
		So(hits.Load(), ShouldEqual, int32(0))
	})
}

// Test connection runs the asset type's declared auth exactly like a real call:
// the form's password — freshly typed, or the stored one merged for an unchanged
// field — must reach host-side injection even though an extension without
// credentials:read never sees it in its own config.
func TestTestConnectionKeepsCredentialsForInjectionWithoutCredentialsRead(t *testing.T) {
	Convey("given an extension without credentials:read", t, func() {
		svc, assets := newHostTestService(t)
		manifest := &extension.Manifest{
			Manifest:   extmanifest.Manifest{Name: "other"},
			AssetTypes: []extension.AssetTypeDef{{Type: "other-store", ConfigSchema: connectionPasswordSchema, Auth: bearerAuth}},
		}

		Convey("a password typed into the new-asset form is kept for injection, withheld from the guest", func() {
			adhoc, err := svc.buildAdHocTestConfig(context.Background(), "other", manifest, "other-store", 0, `{"host":"h","password":"typed"}`)
			So(err, ShouldBeNil)
			So(string(adhoc.Config), ShouldNotContainSubstring, "typed")
			So(string(adhoc.Credentials), ShouldEqual, `{"host":"h","password":"typed"}`)
		})

		Convey("an unchanged password of the edited asset is kept for injection from storage", func() {
			assets.EXPECT().Find(gomock.Any(), int64(5)).
				Return(&asset_entity.Asset{ID: 5, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)
			adhoc, err := svc.buildAdHocTestConfig(context.Background(), "other", manifest, "other-store", 5, `{"host":"h"}`)
			So(err, ShouldBeNil)
			So(string(adhoc.Config), ShouldNotContainSubstring, "theirs")
			So(string(adhoc.Credentials), ShouldEqual, `{"host":"h","password":"theirs"}`)
		})
	})
}
