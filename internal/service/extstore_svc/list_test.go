package extstore_svc

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/extstore"
)

func listingByName(t *testing.T, ls []Listing, name string) Listing {
	t.Helper()
	for _, l := range ls {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("no listing %q in %+v", name, ls)
	return Listing{}
}

func TestList(t *testing.T) {
	key, priv := newKey(t)
	raw := sampleIndex(t)
	ctx := context.Background()

	t.Run("refreshes first when no index has verified, then lists every extension", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		s := e2eStore(t, srv, key, "")

		got, err := s.List(ctx, "zh-CN")
		require.NoError(t, err)
		require.Len(t, got, 5)
		assert.Len(t, srv.requests, 2, "index and signature fetched once")

		es := listingByName(t, got, "es")
		assert.Equal(t, extstore.ActionInstall, es.Action)
		assert.Equal(t, "0.2.0", es.Latest)
		assert.Equal(t, "Elasticsearch 工具", es.DisplayName)

		kafka := listingByName(t, got, "kafka")
		assert.Equal(t, extstore.ActionUpdate, kafka.Action)
		assert.Equal(t, "0.4.1", kafka.Latest)
		assert.Equal(t, "0.1.0", kafka.InstalledVersion)

		pg := listingByName(t, got, "pgdoctor")
		assert.Equal(t, extstore.ActionUnavailable, pg.Action)
		assert.Empty(t, pg.Latest, "no version runs here")
		require.NotNil(t, pg.Unavailable)
		assert.Equal(t, ReasonHostABI, pg.Unavailable.Reason)

		_, err = s.List(ctx, "en")
		require.NoError(t, err)
		assert.Len(t, srv.requests, 2, "a verified index is listed without fetching again")
	})

	t.Run("installed above every compatible version: latest is still the newest compatible one", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		s := e2eStore(t, srv, key, "")
		s.opts.InstalledVersions = func() map[string]string { return map[string]string{"es": "0.9.0"} }

		got, err := s.List(ctx, "en")
		require.NoError(t, err)
		es := listingByName(t, got, "es")
		assert.Equal(t, extstore.ActionInstalled, es.Action)
		assert.Equal(t, "0.9.0", es.InstalledVersion)
		assert.Equal(t, "0.2.0", es.Latest)
	})

	t.Run("no verified index and the refresh fails: the refresh error", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		srv.status = http.StatusBadGateway
		s := e2eStore(t, srv, key, "")

		got, err := s.List(ctx, "en")
		require.Error(t, err)
		assert.Empty(t, got)
		var re *RefreshError
		require.True(t, errors.As(err, &re))
		assert.Equal(t, ErrorFetch, re.Kind)
	})

	t.Run("a later failed refresh still lists the verified index", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		s := e2eStore(t, srv, key, "")
		require.NoError(t, s.Refresh(ctx))
		srv.status = http.StatusBadGateway
		require.Error(t, s.Refresh(ctx))

		got, err := s.List(ctx, "en")
		require.NoError(t, err)
		assert.Len(t, got, 5)
	})
}
