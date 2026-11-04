package extension_svc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_data_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_settings_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/extension_settings_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
	"github.com/opskat/opskat/pkg/extmanifest"
	"github.com/opskat/opskat/pkg/extwire"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/glebarez/sqlite"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
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
		Manifest: withAssetTypes(extmanifest.Manifest{Name: name, Permissions: extmanifest.Permissions{Credentials: credentials}},
			extwire.AssetType{Type: assetType, ConfigSchema: passwordSchema}),
	}
}

// withAssetTypes is the runtime manifest of an extension under contract whose
// describe() contributes types — what loading it produces.
func withAssetTypes(contract extmanifest.Manifest, types ...extwire.AssetType) *extension.Manifest {
	return extensiontest.Described(contract, extensiontest.Describe{Contributes: map[string]any{extwire.KeyAssetTypes: types}})
}

// viewOf is m's typed view, as the service reads it.
func viewOf(t testing.TB, m *extension.Manifest) *extreg.Manifest {
	t.Helper()
	view, err := extreg.ManifestOf(m)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

// newHostTestService is a service whose asset repo is mocked and whose bridge has
// two extensions loaded: "acme" (type acme-store, may read plaintext credentials)
// and "other" (type other-store, may not).
func newHostTestService(t *testing.T) (*Service, *mock_asset_repo.MockAssetRepo) {
	ctrl := gomock.NewController(t)
	assets := mock_asset_repo.NewMockAssetRepo(ctrl)
	svc := New(nil, nil, nil, nil, assets, zap.NewNop(), nil, nil)
	svc.Bridge().Register(registeredExt("acme", "acme-store", extmanifest.CredentialAccessRead))
	svc.Bridge().Register(registeredExt("other", "other-store", ""))
	credential_svc.SetDefault(credential_svc.New("host-test-key", []byte("0123456789abcdef")))
	return svc, assets
}

// configGetterOf is the config getter HostConfig hands extName's host; dialerOf
// the connection-path resolver behind the scopes it resolves (ResolveScope).
func configGetterOf(svc *Service, extName string) *assetConfigGetter {
	return svc.HostConfig(extName).AssetConfigs.(*assetConfigGetter)
}

func dialerOf(svc *Service, extName string) *assetDialer {
	return &assetDialer{svc: svc, extName: extName}
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

// A call without an asset (a host-only tool, a page's call made without one) has
// the scope of the extension's settings: the addresses its format:"endpoint"
// fields name, admitted by permissions.network.settingsEndpoint, its auth
// rendered from the decrypted settings on each request, and the read view as the
// scope's config — non-secret values, a secret only as set or unset.
func TestResolveNoAssetScopeIsTheSettingsScope(t *testing.T) {
	Convey("Given extension jev declaring settings", t, func() {
		f := newSettingsFixture(t)

		Convey("with settings saved, the scope names the endpoint, injects the key and reads the view", func() {
			So(f.save(t, map[string]any{"baseURL": "https://jev.example:8443/v1", "apiKey": "s3cret", "mode": "fast"}), ShouldBeEmpty)

			scope, err := f.svc.ResolveNoAssetScope(f.ctx, "jev")
			So(err, ShouldBeNil)
			So(scope.Asset, ShouldBeNil)
			So(scope.Ops, ShouldBeNil)
			So(scope.Route, ShouldBeNil)
			So(scope.Connect, ShouldBeNil)
			So(scope.Endpoints, ShouldResemble, []extension.Endpoint{{Scheme: "https", Host: "jev.example", Port: "8443"}})
			So(scope.EndpointPermission, ShouldEqual, "settingsEndpoint")
			So(string(scope.Config), ShouldEqual, `{"values":{"baseURL":"https://jev.example:8443/v1","mode":"fast"},"secrets":{"apiKey":true}}`)
			values, err := scope.Auth(f.ctx)
			So(err, ShouldBeNil)
			So(values, ShouldResemble, []extension.AuthValue{{In: "header", Name: "Authorization", Value: "Bearer s3cret"}})

			Convey("a key saved later is what the next request carries", func() {
				So(f.save(t, map[string]any{"baseURL": "https://jev.example:8443/v1", "apiKey": "rotated"}), ShouldBeEmpty)
				values, err := scope.Auth(f.ctx)
				So(err, ShouldBeNil)
				So(values, ShouldResemble, []extension.AuthValue{{In: "header", Name: "Authorization", Value: "Bearer rotated"}})
			})
		})

		Convey("with nothing saved, there is no endpoint and the view shows the key unset", func() {
			scope, err := f.svc.ResolveNoAssetScope(f.ctx, "jev")
			So(err, ShouldBeNil)
			So(scope.Endpoints, ShouldBeEmpty)
			So(string(scope.Config), ShouldEqual, `{"values":{},"secrets":{"apiKey":false}}`)
		})

		Convey("an extension declaring no settings gets an empty config and nothing to reach", func() {
			writeSettingsExtensionWithout(t, f, "plain")
			scope, err := f.svc.ResolveNoAssetScope(f.ctx, "plain")
			So(err, ShouldBeNil)
			So(scope.Endpoints, ShouldBeEmpty)
			So(scope.Auth, ShouldBeNil)
			So(string(scope.Config), ShouldEqual, `{"values":{},"secrets":{}}`)
		})

		Convey("the hosts the service builds resolve it for calls without an asset", func() {
			So(f.save(t, map[string]any{"baseURL": "https://jev.example", "apiKey": "s3cret"}), ShouldBeEmpty)
			resolve := f.svc.HostConfig("jev").NoAssetScopes
			So(resolve, ShouldNotBeNil)
			scope, err := resolve(f.ctx, "jev")
			So(err, ShouldBeNil)
			So(scope.Endpoints, ShouldResemble, []extension.Endpoint{{Scheme: "https", Host: "jev.example", Port: "443"}})
		})
	})
}

// A call on an asset keeps the asset's scope: settings neither widen its reach
// nor become its config.
func TestResolveScopeOfAnAssetCarriesNoSettings(t *testing.T) {
	Convey("an asset's scope has no config and needs network.assetEndpoint", t, func() {
		svc, assets := newHostTestService(t)
		svc.Bridge().Register(authedExt())
		assets.EXPECT().Find(gomock.Any(), int64(6)).Return(&asset_entity.Asset{
			ID: 6, Type: "authed-store", Config: `{"url":"https://es.internal:9200"}`,
		}, nil)

		scope, err := svc.ResolveScope(context.Background(), "authed", extension.AssetRef{ID: 6, Type: "authed-store"})
		So(err, ShouldBeNil)
		So(scope.Config, ShouldBeNil)
		So(scope.EndpointPermission, ShouldBeEmpty)
	})
}

// settingsService is the service a settings endpoint names: it records the
// Authorization each request carried, redirects /redirect-out to elsewhere and
// answers anything else with "service".
type settingsService struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newSettingsService(t *testing.T, elsewhere string) *settingsService {
	t.Helper()
	s := &settingsService{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.URL.Path+" "+r.Header.Get("Authorization"))
		s.mu.Unlock()
		if r.URL.Path == "/redirect-out" {
			http.Redirect(w, r, elsewhere+"/landed", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("service"))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *settingsService) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// newSettingsHost loads the settings fixture (extensiontest.InstallSettingsFixture)
// through the real manager over the hosts the service builds for it (HostConfig),
// its settings stored in a real database; allowlisted is in its
// permissions.network.http (with allowPrivate, as the test servers are loopback).
func newSettingsHost(t *testing.T, allowlisted string) (*Service, *extension.Extension) {
	t.Helper()
	ctx := context.Background()
	gdb, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "opskat.db")), &gorm.Config{})
	So(err, ShouldBeNil)
	So(gdb.AutoMigrate(&extension_settings_entity.ExtensionSettings{}, &extension_data_entity.ExtensionData{}), ShouldBeNil)
	db.SetDefault(gdb)
	prevCred := credential_svc.Default()
	credential_svc.SetDefault(credential_svc.New("settings-host-test", []byte("0123456789abcdef")))
	t.Cleanup(func() { credential_svc.SetDefault(prevCred) })

	root := t.TempDir()
	dir := extensiontest.InstallSettingsFixture(t, root)
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath) //nolint:gosec // the test's own temp dir
	So(err, ShouldBeNil)
	var manifest map[string]any
	So(json.Unmarshal(data, &manifest), ShouldBeNil)
	network := manifest["permissions"].(map[string]any)["network"].(map[string]any)
	network["http"] = []string{allowlisted + "/"}
	network["allowPrivate"] = true
	data, err = json.Marshal(manifest)
	So(err, ShouldBeNil)
	So(os.WriteFile(manifestPath, data, 0o600), ShouldBeNil)

	var svc *Service
	mgr := extension.NewManager(root, func(name string) extension.HostProvider {
		return extension.NewDefaultHostProvider(svc.HostConfig(name))
	}, extension.NewContributionRegistry(append(extreg.Points(), SettingsPoint())...), zap.NewNop())
	t.Cleanup(func() { mgr.Close(ctx) })
	svc = New(mgr, nil, nil, extension_settings_repo.NewExtensionSettings(), nil, zap.NewNop(), nil, nil)
	_, err = mgr.LoadExtension(ctx, dir)
	So(err, ShouldBeNil)
	ext := mgr.GetExtension(extensiontest.SettingsFixtureName)
	svc.Bridge().Register(ext)
	return svc, ext
}

// callHostTool calls one of the fixture's host-only tools as a call without an
// asset, answering its result as JSON.
func callHostTool(ext *extension.Extension, tool string, args map[string]any) (string, error) {
	raw, err := json.Marshal(args)
	So(err, ShouldBeNil)
	out, err := ext.Plugin.CallTool(context.Background(), extension.ToolCall{Name: tool, Args: raw})
	return string(out), err
}

// End to end: the settings fixture's host-only tools, through the hosts the
// service builds, against loopback services.
func TestSettingsReachAndReadEndToEnd(t *testing.T) {
	Convey("Given the settings fixture loaded over the service's hosts", t, func() {
		ctx := context.Background()
		elsewhere := newSettingsService(t, "http://127.0.0.1:1") // allowlisted
		service := newSettingsService(t, elsewhere.URL)
		svc, ext := newSettingsHost(t, elsewhere.URL)
		name := extensiontest.SettingsFixtureName

		Convey("before any settings are saved, the extension reads them unset and reaches no service", func() {
			out, err := callHostTool(ext, "settings", map[string]any{})
			So(err, ShouldBeNil)
			So(out, ShouldEqual, `{"values":{},"secrets":{"apiKey":false}}`)

			_, err = callHostTool(ext, "call_service", map[string]any{"url": service.URL + "/ok"})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "not in permissions.network.http")
			So(service.requests(), ShouldBeEmpty)
		})

		Convey("once saved", func() {
			errs, err := svc.SaveSettings(ctx, name, map[string]any{"baseURL": service.URL, "apiKey": "s3cret", "model": "fast"})
			So(err, ShouldBeNil)
			So(errs, ShouldBeEmpty)

			Convey("the extension reads the non-secret values and that the key is set, never the key", func() {
				out, err := callHostTool(ext, "settings", map[string]any{})
				So(err, ShouldBeNil)
				So(out, ShouldEqual, `{"values":{"baseURL":"`+service.URL+`","model":"fast"},"secrets":{"apiKey":true}}`)
				So(out, ShouldNotContainSubstring, "s3cret")
			})

			Convey("a request to the service carries the key the host injects, which the guest never sees", func() {
				out, err := callHostTool(ext, "call_service", map[string]any{"url": service.URL + "/ok"})
				So(err, ShouldBeNil)
				So(out, ShouldEqual, `{"body":"service","status":200}`)
				So(service.requests(), ShouldResemble, []string{"/ok Bearer s3cret"})
			})

			Convey("a redirect off the service fails before reaching where it points", func() {
				_, err := callHostTool(ext, "call_service", map[string]any{"url": service.URL + "/redirect-out"})
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, "is not an endpoint of this call's scope")
				So(err.Error(), ShouldNotContainSubstring, "s3cret")
				So(elsewhere.requests(), ShouldBeEmpty)
			})

			Convey("an allowlisted address is reached with nothing injected", func() {
				_, err := callHostTool(ext, "call_service", map[string]any{"url": elsewhere.URL + "/x"})
				So(err, ShouldBeNil)
				So(elsewhere.requests(), ShouldResemble, []string{"/x "})
			})
		})
	})
}
