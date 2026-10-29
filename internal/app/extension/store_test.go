package extension

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/repository/extension_state_repo/mock_extension_state_repo"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/internal/service/extstore_svc"
	"github.com/opskat/opskat/pkg/extension"
)

func TestStoreBindings(t *testing.T) {
	t.Run("without the store service both calls fail", func(t *testing.T) {
		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		_, err := e.ListStore("en")
		require.Error(t, err)
		_, err = e.RefreshStore("en")
		require.Error(t, err)
	})

	t.Run("a failed refresh comes back as store state with its kind", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()
		t.Setenv("OPSKAT_E2E", "1")
		t.Setenv(extstore_svc.EnvIndexURL, srv.URL+"/index.json")

		e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
		e.SetStoreService(extstore_svc.New(extstore_svc.Options{
			DownloadMirror:    func() string { return "" },
			InstalledVersions: func() map[string]string { return nil },
			App:               func() appversion.Info { return appversion.Info{Version: "1.0.0", Kind: appversion.KindDev} },
		}))

		before, err := e.ListStore("en")
		require.NoError(t, err)
		assert.Nil(t, before.Error)
		assert.Zero(t, before.UpdatedAt)

		st, err := e.RefreshStore("en")
		require.NoError(t, err)
		require.NotNil(t, st.Error)
		assert.Equal(t, extstore_svc.ErrorFetch, st.Error.Kind)

		listed, err := e.ListStore("en")
		require.NoError(t, err)
		assert.Equal(t, st, listed, "ListStore shows the latest refresh without fetching")
	})
}

// The store compares against what this host runs: a copy it refuses (a retired
// hostABI) is not an installed version, so the store offers the compatible build
// of the same version instead of calling it installed — as the install confirm
// describes installing over it as a fresh install.
func TestInstalledVersionsLeaveOutRefusedCopies(t *testing.T) {
	extDir := t.TempDir()
	legacy := filepath.Join(extDir, "ext-legacy")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "manifest.json"),
		[]byte(`{"name":"ext-legacy","version":"2.0.0","hostABI":"2.3","backend":{"runtime":"wasm","binary":"main.wasm"}}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "main.wasm"), stubWASM, 0o600))

	ctrl := gomock.NewController(t)
	stateRepo := mock_extension_state_repo.NewMockExtensionStateRepo(ctrl)
	stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)
	manager := extension.NewManager(extDir, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{Logger: zap.NewNop()})
	}, extension.NewContributionRegistry(extreg.Points()...), zap.NewNop())
	svc := extension_svc.New(manager, stateRepo, nil, nil, zap.NewNop(), nil, nil)
	t.Cleanup(func() { svc.Close(context.Background()) })
	require.NoError(t, svc.Init(context.Background()))

	e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
	e.SetService(svc)
	require.Len(t, svc.ListInstalled("en"), 1, "the settings list still shows the refused copy")
	assert.Empty(t, InstalledVersions(e))
}
