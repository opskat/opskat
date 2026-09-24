package extension

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/extension"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// bearerAuth injects the asset's password field as a bearer token.
var bearerAuth = &extension.AuthDef{Groups: []extension.AuthGroup{{
	Bindings: []extension.AuthBinding{{In: "header", Name: "Authorization", Value: "Bearer {{password}}"}},
}}}

func TestAssetCredentialValuesServeInjectionOnly(t *testing.T) {
	Convey("credential injection reads decrypted fields host-side, whatever the extension may read itself", t, func() {
		e, assets := newHostTestBinder(t)

		Convey("an extension without credentials:read still gets the plaintext for injection", func() {
			assets.EXPECT().Find(gomock.Any(), int64(4)).
				Return(&asset_entity.Asset{ID: 4, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)
			values, err := e.NewAssetConfigGetter("other").AssetCredentialValues(context.Background(), 4, []string{"host", "password"})
			So(err, ShouldBeNil)
			So(values, ShouldResemble, map[string]string{"host": "h", "password": "theirs"})
		})

		Convey("only the named fields are decrypted", func() {
			assets.EXPECT().Find(gomock.Any(), int64(5)).
				Return(&asset_entity.Asset{ID: 5, Type: "other-store", Config: `{"host":"h","password":"not-a-ciphertext"}`}, nil)
			values, err := e.NewAssetConfigGetter("other").AssetCredentialValues(context.Background(), 5, []string{"host"})
			So(err, ShouldBeNil)
			So(values, ShouldResemble, map[string]string{"host": "h"})
		})

		Convey("another extension's asset is refused", func() {
			assets.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&asset_entity.Asset{ID: 3, Type: "acme-store", Config: encryptedConfig(t, "mine")}, nil)
			values, err := e.NewAssetConfigGetter("other").AssetCredentialValues(context.Background(), 3, []string{"password"})
			So(err, ShouldNotBeNil)
			So(values, ShouldBeNil)
		})
	})
}

func TestCredentialInjectionDecryptFailureSendsNothing(t *testing.T) {
	Convey("a referenced password that does not decrypt fails the request before anything is sent", t, func() {
		e, assets := newHostTestBinder(t)
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
		}))
		defer srv.Close()
		assets.EXPECT().Find(gomock.Any(), int64(5)).
			Return(&asset_entity.Asset{ID: 5, Type: "other-store", Config: `{"host":"h","password":"not-a-ciphertext"}`}, nil).AnyTimes()

		provider := extension.NewDefaultHostProvider(extension.DefaultHostConfig{AssetConfigs: e.NewAssetConfigGetter("other")})
		res, err := provider.OpenIO(context.Background(), &extension.AssetRef{ID: 5, Name: "store", Type: "other-store"}, extension.IOOpenParams{
			Type: "http", Method: "GET", URL: srv.URL + "/",
			Auth: &extension.HTTPAuth{Def: bearerAuth, IsEndpoint: func(*url.URL) bool { return true }},
		})

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "password")
		So(res, ShouldBeNil)
		So(hits.Load(), ShouldEqual, int32(0))
	})
}
