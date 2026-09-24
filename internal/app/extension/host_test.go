package extension

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
)

type fixedLang string

func (l fixedLang) Lang() string { return string(l) }

var passwordSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"host":     map[string]any{"type": "string"},
		"password": map[string]any{"type": "string", "format": "password"},
	},
}

func registeredExt(name, assetType, credentials string) *extension.Extension {
	return &extension.Extension{
		Name: name,
		Manifest: &extension.Manifest{
			Name:         name,
			AssetTypes:   []extension.AssetTypeDef{{Type: assetType, ConfigSchema: passwordSchema}},
			Capabilities: extension.Capabilities{Credentials: credentials},
		},
	}
}

// newHostTestBinder wires a binder over a service whose asset repo is mocked
// and whose bridge has two extensions loaded: "acme" (type acme-store, may read
// plaintext credentials) and "other" (type other-store, may not).
func newHostTestBinder(t *testing.T) (*Extension, *mock_asset_repo.MockAssetRepo) {
	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	svc := extension_svc.New(nil, nil, nil, assets, zap.NewNop(), nil, nil)
	svc.Bridge().Register(registeredExt("acme", "acme-store", extension.CredentialAccessRead))
	svc.Bridge().Register(registeredExt("other", "other-store", ""))
	credential_svc.SetDefault(credential_svc.New("host-test-key", []byte("0123456789abcdef")))
	e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
	e.SetService(svc)
	return e, assets
}

func encryptedConfig(t *testing.T, password string) string {
	enc, err := credential_svc.Default().Encrypt(password)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"host": "h", "password": enc})
	return string(b)
}

func TestAssetConfigGetterScopesToCallingExtension(t *testing.T) {
	Convey("the host asset config getter serves only the calling extension's assets", t, func() {
		e, assets := newHostTestBinder(t)
		acme := e.NewAssetConfigGetter("acme")

		Convey("its own asset comes back, credentials per its own manifest", func() {
			assets.EXPECT().Find(gomock.Any(), int64(1)).
				Return(&asset_entity.Asset{ID: 1, Type: "acme-store", Config: encryptedConfig(t, "s3cret")}, nil)
			raw, err := acme.GetAssetConfig(1)
			So(err, ShouldBeNil)
			var cfg map[string]any
			So(json.Unmarshal(raw, &cfg), ShouldBeNil)
			So(cfg["password"], ShouldEqual, "s3cret")
		})

		Convey("a builtin asset is refused", func() {
			assets.EXPECT().Find(gomock.Any(), int64(2)).
				Return(&asset_entity.Asset{ID: 2, Type: "ssh", Config: `{"host":"prod"}`}, nil)
			raw, err := acme.GetAssetConfig(2)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "does not belong to extension")
			So(raw, ShouldBeNil)
		})

		Convey("another extension's asset is refused", func() {
			assets.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&asset_entity.Asset{ID: 3, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)
			raw, err := acme.GetAssetConfig(3)
			So(err, ShouldNotBeNil)
			So(raw, ShouldBeNil)
		})

		Convey("an extension without credentials=read gets a handle, not plaintext", func() {
			assets.EXPECT().Find(gomock.Any(), int64(4)).
				Return(&asset_entity.Asset{ID: 4, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)
			raw, err := e.NewAssetConfigGetter("other").GetAssetConfig(4)
			So(err, ShouldBeNil)
			So(string(raw), ShouldNotContainSubstring, "theirs")
			So(string(raw), ShouldContainSubstring, "__credential_handle")
		})
	})
}

func TestFrontendCallsScopeAssetToExtension(t *testing.T) {
	Convey("frontend-initiated calls may only name the extension's own assets", t, func() {
		e, assets := newHostTestBinder(t)

		Convey("assetRef refuses an asset of another type", func() {
			assets.EXPECT().Find(gomock.Any(), int64(2)).
				Return(&asset_entity.Asset{ID: 2, Name: "prod", Type: "ssh"}, nil)
			ref, err := e.assetRef("acme", 2)
			So(err, ShouldNotBeNil)
			So(ref, ShouldBeNil)
		})

		Convey("assetRef names an owned asset", func() {
			assets.EXPECT().Find(gomock.Any(), int64(1)).
				Return(&asset_entity.Asset{ID: 1, Name: "store", Type: "acme-store"}, nil)
			ref, err := e.assetRef("acme", 1)
			So(err, ShouldBeNil)
			So(ref, ShouldResemble, &extension.AssetRef{ID: 1, Name: "store", Type: "acme-store"})
		})

		Convey("GetDecryptedExtensionConfig refuses an asset the named extension does not own", func() {
			assets.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&asset_entity.Asset{ID: 3, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)
			cfg, err := e.GetDecryptedExtensionConfig(3, "acme")
			So(err, ShouldNotBeNil)
			So(cfg, ShouldEqual, "")
		})

		Convey("GetDecryptedExtensionConfig decrypts the extension's own asset for its form", func() {
			assets.EXPECT().Find(gomock.Any(), int64(1)).
				Return(&asset_entity.Asset{ID: 1, Type: "acme-store", Config: encryptedConfig(t, "s3cret")}, nil)
			cfg, err := e.GetDecryptedExtensionConfig(1, "acme")
			So(err, ShouldBeNil)
			So(cfg, ShouldContainSubstring, "s3cret")
		})
	})
}

func TestAssetConfigDecryptFailureFailsClosed(t *testing.T) {
	Convey("a password field that does not decrypt is an error, never ciphertext handed to the guest", t, func() {
		e, assets := newHostTestBinder(t)
		assets.EXPECT().Find(gomock.Any(), int64(5)).
			Return(&asset_entity.Asset{ID: 5, Type: "acme-store", Config: `{"host":"h","password":"not-a-ciphertext"}`}, nil)

		raw, err := e.NewAssetConfigGetter("acme").GetAssetConfig(5)

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "password")
		So(raw, ShouldBeNil)
	})
}
