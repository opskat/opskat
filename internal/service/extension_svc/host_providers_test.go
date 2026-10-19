package extension_svc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

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

// newHostTestService is a service whose asset repo is mocked and whose bridge has
// two extensions loaded: "acme" (type acme-store, may read plaintext credentials)
// and "other" (type other-store, may not).
func newHostTestService(t *testing.T) (*Service, *mock_asset_repo.MockAssetRepo) {
	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	svc := New(nil, nil, nil, assets, zap.NewNop(), nil, nil)
	svc.Bridge().Register(registeredExt("acme", "acme-store", extension.CredentialAccessRead))
	svc.Bridge().Register(registeredExt("other", "other-store", ""))
	credential_svc.SetDefault(credential_svc.New("host-test-key", []byte("0123456789abcdef")))
	return svc, assets
}

// configGetterOf and dialerOf are the providers HostConfig hands extName's host.
func configGetterOf(svc *Service, extName string) *assetConfigGetter {
	return svc.HostConfig(extName).AssetConfigs.(*assetConfigGetter)
}

func dialerOf(svc *Service, extName string) *assetDialer {
	return svc.HostConfig(extName).AssetDialer.(*assetDialer)
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
		svc, assets := newHostTestService(t)
		acme := configGetterOf(svc, "acme")

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
			raw, err := configGetterOf(svc, "other").GetAssetConfig(4)
			So(err, ShouldBeNil)
			So(string(raw), ShouldNotContainSubstring, "theirs")
			So(string(raw), ShouldContainSubstring, "__credential_handle")
		})
	})
}

func TestAssetConfigGetterStripsHostConnectionConfig(t *testing.T) {
	Convey("ctx.AssetConfig() never sees the host's reserved connection key", t, func() {
		svc, assets := newHostTestService(t)
		acme := configGetterOf(svc, "acme")

		cfg, err := json.Marshal(map[string]any{
			"host": "h",
			extension.HostConnectionConfigKey: map[string]any{
				"tls": map[string]any{"enabled": true, "caCert": "ca-pem"},
			},
		})
		So(err, ShouldBeNil)
		assets.EXPECT().Find(gomock.Any(), int64(6)).Return(&asset_entity.Asset{ID: 6, Type: "acme-store", Config: string(cfg)}, nil)

		raw, err := acme.GetAssetConfig(6)
		So(err, ShouldBeNil)
		var parsed map[string]any
		So(json.Unmarshal(raw, &parsed), ShouldBeNil)
		So(parsed, ShouldContainKey, "host")
		So(parsed, ShouldNotContainKey, extension.HostConnectionConfigKey)
	})
}

func TestAssetConfigDecryptFailureFailsClosed(t *testing.T) {
	Convey("a password field that does not decrypt is an error, never ciphertext handed to the guest", t, func() {
		svc, assets := newHostTestService(t)
		assets.EXPECT().Find(gomock.Any(), int64(5)).
			Return(&asset_entity.Asset{ID: 5, Type: "acme-store", Config: `{"host":"h","password":"not-a-ciphertext"}`}, nil)

		raw, err := configGetterOf(svc, "acme").GetAssetConfig(5)

		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "password")
		So(raw, ShouldBeNil)
	})
}

func TestAssetConfigAccessIsAuditedThroughAppLogger(t *testing.T) {
	Convey("reading an asset config leaves an audit entry in the app's logger", t, func() {
		core, logs := observer.New(zap.InfoLevel)
		old := logger.Default()
		logger.SetLogger(zap.New(core))
		t.Cleanup(func() { logger.SetLogger(old) })

		svc, assets := newHostTestService(t)
		assets.EXPECT().Find(gomock.Any(), int64(1)).
			Return(&asset_entity.Asset{ID: 1, Type: "acme-store", Config: encryptedConfig(t, "s3cret")}, nil)

		_, err := configGetterOf(svc, "acme").GetAssetConfig(1)
		So(err, ShouldBeNil)

		entries := logs.FilterMessage("extension accessed asset config").All()
		So(entries, ShouldHaveLength, 1)
		fields := entries[0].ContextMap()
		So(fields["extension"], ShouldEqual, "acme")
		So(fields["asset_id"], ShouldEqual, int64(1))
		So(fields["plaintext_allowed"], ShouldEqual, true)
	})
}

// The asset configuration form shares its webview with extension pages, which can
// read it themselves, so it gets exactly what the extension may see.
func TestFormConfigFollowsTheCredentialsDecision(t *testing.T) {
	Convey("FormConfig serves an extension's own asset to its configuration form", t, func() {
		svc, assets := newHostTestService(t)
		ctx := context.Background()

		Convey("an asset the named extension does not own is refused", func() {
			assets.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&asset_entity.Asset{ID: 3, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)
			cfg, err := svc.FormConfig(ctx, "acme", 3)
			So(err, ShouldNotBeNil)
			So(cfg, ShouldEqual, "")
		})

		Convey("the extension's own asset is decrypted when it may read credentials", func() {
			assets.EXPECT().Find(gomock.Any(), int64(1)).
				Return(&asset_entity.Asset{ID: 1, Type: "acme-store", Config: encryptedConfig(t, "s3cret")}, nil)
			cfg, err := svc.FormConfig(ctx, "acme", 1)
			So(err, ShouldBeNil)
			So(cfg, ShouldContainSubstring, "s3cret")
		})

		// Neither plaintext nor the guest's opaque handle, which the form would
		// otherwise render (and save back) as a value.
		Convey("stored passwords are withheld from an extension without credentials=read", func() {
			assets.EXPECT().Find(gomock.Any(), int64(4)).
				Return(&asset_entity.Asset{ID: 4, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)
			raw, err := svc.FormConfig(ctx, "other", 4)
			So(err, ShouldBeNil)
			var cfg map[string]any
			So(json.Unmarshal([]byte(raw), &cfg), ShouldBeNil)
			So(cfg, ShouldResemble, map[string]any{"host": "h"})
		})
	})
}
