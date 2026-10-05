package extension

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/service/extstore_svc"
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
