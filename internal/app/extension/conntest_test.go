package extension

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/extension"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// connectionSchema mirrors passwordSchema (host_test.go) plus an endpoint
// field, so conntest tests can also exercise the reserved connection block.
var connectionPasswordSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"host":     map[string]any{"type": "string"},
		"password": map[string]any{"type": "string", "format": "password"},
	},
}

func acmeManifest() *extension.Manifest {
	return &extension.Manifest{
		Name:         "acme",
		AssetTypes:   []extension.AssetTypeDef{{Type: "acme-store", ConfigSchema: connectionPasswordSchema}},
		Capabilities: extension.Capabilities{Credentials: extension.CredentialAccessRead},
	}
}

func TestBuildAdHocTestConfig(t *testing.T) {
	Convey("Given the acme extension (credentials:read) with a password field", t, func() {
		e, assets := newHostTestBinder(t)
		manifest := acmeManifest()

		Convey("a new asset (no asset id) sends the submitted password as-is", func() {
			cfg := `{"host":"h","password":"typed"}`
			adhoc, err := e.buildAdHocTestConfig(context.Background(), "acme", manifest, "acme-store", 0, cfg)
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(adhoc.Config, &out), ShouldBeNil)
			So(out["password"], ShouldEqual, "typed")
		})

		Convey("editing, the password field omitted: merges the stored decrypted value", func() {
			assets.EXPECT().Find(gomock.Any(), int64(1)).
				Return(&asset_entity.Asset{ID: 1, Type: "acme-store", Config: encryptedConfig(t, "s3cret")}, nil)

			adhoc, err := e.buildAdHocTestConfig(context.Background(), "acme", manifest, "acme-store", 1, `{"host":"h"}`)
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(adhoc.Config, &out), ShouldBeNil)
			So(out["password"], ShouldEqual, "s3cret")
		})

		// The form leaves out only the fields the user did not touch; one it sends
		// empty is one the user cleared, and saving will clear it. Testing with the
		// stored secret instead would report on a configuration that is not the one
		// about to be saved.
		Convey("editing, the password field cleared: tests with it empty, not the stored value", func() {
			assets.EXPECT().Find(gomock.Any(), int64(2)).
				Return(&asset_entity.Asset{ID: 2, Type: "acme-store", Config: encryptedConfig(t, "s3cret")}, nil)

			adhoc, err := e.buildAdHocTestConfig(context.Background(), "acme", manifest, "acme-store", 2, `{"host":"h","password":""}`)
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(adhoc.Config, &out), ShouldBeNil)
			So(out["password"], ShouldEqual, "")
			So(json.Unmarshal(adhoc.Credentials, &out), ShouldBeNil)
			So(out["password"], ShouldEqual, "")
		})

		Convey("editing, a retyped password is sent as-is, never the stored one", func() {
			assets.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&asset_entity.Asset{ID: 3, Type: "acme-store", Config: encryptedConfig(t, "s3cret")}, nil)

			adhoc, err := e.buildAdHocTestConfig(context.Background(), "acme", manifest, "acme-store", 3, `{"host":"h","password":"new-one"}`)
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(adhoc.Config, &out), ShouldBeNil)
			So(out["password"], ShouldEqual, "new-one")
		})

		Convey("the asset id must belong to the asset type under test", func() {
			// The id names a real, owned asset, but of a different type than the
			// one this test call declares — a stale form or a copy-pasted id must
			// not merge in a password field foreign to the type being tested.
			assets.EXPECT().Find(gomock.Any(), int64(9)).
				Return(&asset_entity.Asset{ID: 9, Type: "some-other-store"}, nil)

			_, err := e.buildAdHocTestConfig(context.Background(), "acme", manifest, "acme-store", 9, `{}`)
			So(err, ShouldNotBeNil)
		})

		Convey("an asset type the extension does not register is refused", func() {
			_, err := e.buildAdHocTestConfig(context.Background(), "acme", manifest, "not-a-type", 0, `{}`)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not-a-type")
		})

		Convey("the reserved connection block is split off, never left in the guest-visible config", func() {
			cfg, _ := json.Marshal(map[string]any{
				"host": "h",
				extension.HostConnectionConfigKey: map[string]any{
					"sshTunnelId": 7,
					"proxyChain":  map[string]any{"layers": []any{}},
					"tls":         map[string]any{"enabled": true},
				},
			})
			adhoc, err := e.buildAdHocTestConfig(context.Background(), "acme", manifest, "acme-store", 0, string(cfg))
			So(err, ShouldBeNil)
			So(adhoc.SSHTunnelID, ShouldEqual, 7)
			So(adhoc.ProxyChain, ShouldNotBeNil)
			So(adhoc.TLS, ShouldNotBeNil)
			var guest map[string]any
			So(json.Unmarshal(adhoc.Config, &guest), ShouldBeNil)
			So(guest, ShouldNotContainKey, extension.HostConnectionConfigKey)
		})
	})

	Convey("Given an extension without credentials:read", t, func() {
		e, assets := newHostTestBinder(t)
		manifest := &extension.Manifest{
			Name:         "other",
			AssetTypes:   []extension.AssetTypeDef{{Type: "other-store", ConfigSchema: connectionPasswordSchema}},
			Capabilities: extension.Capabilities{},
		}

		Convey("the password field never reaches the guest, merged or not", func() {
			assets.EXPECT().Find(gomock.Any(), int64(5)).
				Return(&asset_entity.Asset{ID: 5, Type: "other-store", Config: encryptedConfig(t, "theirs")}, nil)

			adhoc, err := e.buildAdHocTestConfig(context.Background(), "other", manifest, "other-store", 5, `{"host":"h"}`)
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(adhoc.Config, &out), ShouldBeNil)
			So(out, ShouldNotContainKey, "password")

			// Even a freshly typed value in this call is withheld: the guest
			// authenticates via host-injected Auth, never a value it holds itself.
			adhoc, err = e.buildAdHocTestConfig(context.Background(), "other", manifest, "other-store", 0, `{"host":"h","password":"typed"}`)
			So(err, ShouldBeNil)
			So(json.Unmarshal(adhoc.Config, &out), ShouldBeNil)
			So(out, ShouldNotContainKey, "password")
		})
	})
}

// fakeTestConnCaller is the extreg.TestConnectionCaller a conntest.TestFunc
// built by connTestRegistrar dispatches to.
type fakeTestConnCaller struct {
	assetType string
	adhoc     *extension.AdHocAssetConfig
	err       error
}

func (f *fakeTestConnCaller) TestConnection(_ context.Context, assetType string, adhoc *extension.AdHocAssetConfig) error {
	f.assetType = assetType
	f.adhoc = adhoc
	return f.err
}

func TestConnTestRegistrarBuild(t *testing.T) {
	Convey("Build's tester parses the asset id out of plainPassword and dispatches to the plugin", t, func() {
		e, assets := newHostTestBinder(t)
		manifest := acmeManifest()
		reg := e.NewConnTestRegistrar()

		Convey("a new asset: empty plainPassword means asset id 0, no merge attempted", func() {
			caller := &fakeTestConnCaller{}
			fn := reg.Build("acme", manifest, "acme-store", caller)

			err := fn(context.Background(), `{"host":"h","password":"typed"}`, "")
			So(err, ShouldBeNil)
			So(caller.assetType, ShouldEqual, "acme-store")
			var out map[string]any
			So(json.Unmarshal(caller.adhoc.Config, &out), ShouldBeNil)
			So(out["password"], ShouldEqual, "typed")
		})

		Convey("editing: plainPassword is the asset id, and an omitted password is merged from storage", func() {
			assets.EXPECT().Find(gomock.Any(), int64(4)).
				Return(&asset_entity.Asset{ID: 4, Type: "acme-store", Config: encryptedConfig(t, "s3cret")}, nil)
			caller := &fakeTestConnCaller{}
			fn := reg.Build("acme", manifest, "acme-store", caller)

			err := fn(context.Background(), `{"host":"h"}`, "4")
			So(err, ShouldBeNil)
			var out map[string]any
			So(json.Unmarshal(caller.adhoc.Config, &out), ShouldBeNil)
			So(out["password"], ShouldEqual, "s3cret")
		})

		Convey("the plugin's own connection failure reaches the caller", func() {
			caller := &fakeTestConnCaller{err: assertErr}
			fn := reg.Build("acme", manifest, "acme-store", caller)

			err := fn(context.Background(), `{"host":"h"}`, "")
			So(err, ShouldEqual, assertErr)
		})
	})
}

var assertErr = &connRefusedError{}

type connRefusedError struct{}

func (*connRefusedError) Error() string { return "connection refused" }
