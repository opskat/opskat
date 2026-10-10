package extstore_svc

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/pkg/ociclient"
	"github.com/opskat/opskat/pkg/extstore"
)

var release = appversion.Info{Version: "1.14.0", Kind: appversion.KindRelease}

func version(v, hostABI string) map[string]any {
	return map[string]any{
		"version": v, "hostABI": hostABI, "size": 2048,
		"capabilities": map[string]any{"credentials": "read", "network": map[string]any{"assetEndpoint": true}},
		"source":       map[string]any{"ref": "ghcr.io/opskat/extensions/x:" + v, "sha256": "abc"},
	}
}

func indexJSON(t *testing.T, format int, exts ...map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"format": format, "extensions": exts})
	require.NoError(t, err)
	return raw
}

// sampleIndex has one extension per store state for an app on release 1.14.0
// with "notebook" 1.0.0 and "kafka" 0.1.0 installed.
func sampleIndex(t *testing.T) []byte {
	ipfs := version("2.0.0", "2.2")
	ipfs["source"] = map[string]any{"type": "ipfs", "ref": "x"}
	return indexJSON(t, extstore.FormatVersion,
		map[string]any{
			"name": "es",
			"display": map[string]any{
				"en":    map[string]any{"name": "Elasticsearch", "description": "Browse indices"},
				"zh-CN": map[string]any{"name": "Elasticsearch 工具", "description": ""},
			},
			"icon":     "search",
			"versions": []any{version("0.1.0", "2.2"), version("0.2.0", "2.2")},
		},
		map[string]any{"name": "notebook", "versions": []any{version("1.0.0", "2.2")}},
		map[string]any{"name": "kafka", "versions": []any{version("0.1.0", "2.2"), version("0.4.1", "2.2")}},
		map[string]any{"name": "pgdoctor", "versions": []any{version("0.5.0", "9.0"), version("0.6.0", "9.1")}},
		map[string]any{"name": "future", "versions": []any{ipfs}},
	)
}

// indexServer serves index.json and its .sig at any path, and records the
// request URIs so a test can see whether the download mirror was used.
type indexServer struct {
	*httptest.Server
	mu       sync.Mutex
	index    []byte
	sig      []byte
	status   int
	requests []string
}

func newIndexServer(t *testing.T, index, sig []byte) *indexServer {
	s := &indexServer{index: index, sig: sig, status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.RequestURI)
		if s.status != http.StatusOK {
			w.WriteHeader(s.status)
			return
		}
		switch {
		case strings.HasSuffix(r.RequestURI, "/index.json.sig"):
			_, _ = w.Write(s.sig)
		case strings.HasSuffix(r.RequestURI, "/index.json"):
			_, _ = w.Write(s.index)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func newKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(pub), priv
}

// e2eStore points a store at srv through the OPSKAT_E2E overrides, trusting key.
func e2eStore(t *testing.T, srv *indexServer, key string, mirror string) *Service {
	t.Setenv("OPSKAT_E2E", "1")
	t.Setenv(EnvIndexURL, srv.URL+"/index.json")
	if key != "" {
		t.Setenv(EnvPublicKeys, key)
	}
	return New(Options{
		DownloadMirror:    func() string { return mirror },
		InstalledVersions: func() map[string]string { return map[string]string{"notebook": "1.0.0", "kafka": "0.1.0"} },
		App:               func() appversion.Info { return release },
	})
}

func cardByName(t *testing.T, st State, name string) Card {
	t.Helper()
	for _, c := range st.Extensions {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no card %q in %+v", name, st.Extensions)
	return Card{}
}

func TestRefreshVerifiedIndex(t *testing.T) {
	key, priv := newKey(t)
	raw := sampleIndex(t)
	srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
	s := e2eStore(t, srv, key, srv.URL+"/mirror/")

	before := s.State("en")
	assert.Zero(t, before.UpdatedAt, "nothing is shown before the first refresh")
	assert.Nil(t, before.Error)
	_, _, ok := s.StoreIndex()
	assert.False(t, ok)

	require.NoError(t, s.Refresh(context.Background()))

	t.Run("fetches index and signature through the download mirror", func(t *testing.T) {
		require.Len(t, srv.requests, 2)
		for _, uri := range srv.requests {
			assert.True(t, strings.HasPrefix(uri, "/mirror/"+srv.URL+"/index.json"), uri)
		}
	})

	t.Run("caches the verified index", func(t *testing.T) {
		idx, fetchedAt, ok := s.StoreIndex()
		require.True(t, ok)
		assert.False(t, fetchedAt.IsZero())
		assert.Len(t, idx.Extensions, 5)
	})

	st := s.State("zh-CN")
	assert.True(t, st.Verified)
	assert.NotZero(t, st.UpdatedAt)
	assert.Nil(t, st.Error)
	require.Len(t, st.Extensions, 5)

	t.Run("not installed: install the newest compatible version", func(t *testing.T) {
		c := cardByName(t, st, "es")
		assert.Equal(t, extstore.ActionInstall, c.Action)
		assert.Equal(t, "0.2.0", c.Version)
		assert.Equal(t, "", c.InstalledVersion)
		assert.Equal(t, "search", c.Icon)
		assert.Equal(t, int64(2048), c.Size)
		assert.Equal(t, "read", c.Capabilities.Credentials)
		assert.True(t, c.Capabilities.Network.AssetEndpoint)
		assert.Nil(t, c.Unavailable)
	})

	t.Run("display follows the language, falling back to en per field", func(t *testing.T) {
		c := cardByName(t, st, "es")
		assert.Equal(t, "Elasticsearch 工具", c.DisplayName)
		assert.Equal(t, "Browse indices", c.Description)
		assert.Equal(t, "Elasticsearch", cardByName(t, s.State("en"), "es").DisplayName)
		assert.Equal(t, "Elasticsearch", cardByName(t, s.State("fr"), "es").DisplayName)
	})

	t.Run("language tags match case-insensitively", func(t *testing.T) {
		// The desktop's own language (System.Lang) is lowercased: "zh-cn".
		assert.Equal(t, "Elasticsearch 工具", cardByName(t, s.State("zh-cn"), "es").DisplayName)
	})

	t.Run("an extension without display names shows its name", func(t *testing.T) {
		assert.Equal(t, "notebook", cardByName(t, st, "notebook").DisplayName)
	})

	t.Run("installed and current", func(t *testing.T) {
		c := cardByName(t, st, "notebook")
		assert.Equal(t, extstore.ActionInstalled, c.Action)
		assert.Equal(t, "1.0.0", c.Version)
		assert.Equal(t, "1.0.0", c.InstalledVersion)
	})

	t.Run("installed with a newer compatible version: update", func(t *testing.T) {
		c := cardByName(t, st, "kafka")
		assert.Equal(t, extstore.ActionUpdate, c.Action)
		assert.Equal(t, "0.4.1", c.Version)
		assert.Equal(t, "0.1.0", c.InstalledVersion)
	})

	t.Run("no version runs here: unavailable, newest version and why", func(t *testing.T) {
		c := cardByName(t, st, "pgdoctor")
		assert.Equal(t, extstore.ActionUnavailable, c.Action)
		assert.Equal(t, "0.6.0", c.Version)
		require.NotNil(t, c.Unavailable)
		assert.Equal(t, ReasonHostABI, c.Unavailable.Reason)
		assert.Equal(t, "9.1", c.Unavailable.HostABI)
	})

	t.Run("unknown source type: unavailable, update OpsKat", func(t *testing.T) {
		c := cardByName(t, st, "future")
		assert.Equal(t, extstore.ActionUnavailable, c.Action)
		require.NotNil(t, c.Unavailable)
		assert.Equal(t, ReasonSource, c.Unavailable.Reason)
		assert.Equal(t, "ipfs", c.Unavailable.SourceType)
	})
}

func TestRefreshFailures(t *testing.T) {
	key, priv := newKey(t)
	otherKey, _ := newKey(t)
	raw := sampleIndex(t)

	refreshErr := func(t *testing.T, s *Service) ErrorKind {
		t.Helper()
		err := s.Refresh(context.Background())
		require.Error(t, err)
		st := s.State("en")
		require.NotNil(t, st.Error, "the failure is what the store shows")
		assert.Empty(t, st.Extensions, "a failed refresh shows no cards")
		assert.NotEmpty(t, st.Error.Message)
		return st.Error.Kind
	}

	t.Run("tampered index is a signature failure", func(t *testing.T) {
		tampered := append([]byte(nil), raw...)
		tampered[len(tampered)-2] = ' '
		srv := newIndexServer(t, tampered, extstore.Sign(raw, priv))
		assert.Equal(t, ErrorSignature, refreshErr(t, e2eStore(t, srv, key, "")))
	})

	t.Run("signature from an untrusted key is a signature failure", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		assert.Equal(t, ErrorSignature, refreshErr(t, e2eStore(t, srv, otherKey, "")))
	})

	t.Run("empty signature file is a signature failure", func(t *testing.T) {
		srv := newIndexServer(t, raw, []byte("\n"))
		assert.Equal(t, ErrorSignature, refreshErr(t, e2eStore(t, srv, key, "")))
	})

	t.Run("no trusted key at all trusts nothing", func(t *testing.T) {
		saved := officialPublicKeys
		officialPublicKeys = []string{}
		t.Cleanup(func() { officialPublicKeys = saved })
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		assert.Equal(t, ErrorSignature, refreshErr(t, e2eStore(t, srv, "", "")))
	})

	t.Run("unknown format after a valid signature", func(t *testing.T) {
		future := indexJSON(t, extstore.FormatVersion+1)
		srv := newIndexServer(t, future, extstore.Sign(future, priv))
		assert.Equal(t, ErrorFormat, refreshErr(t, e2eStore(t, srv, key, "")))
	})

	t.Run("signed but unreadable index", func(t *testing.T) {
		broken := []byte(`{"format": 1, "extensions": [{"name": ""}]}`)
		srv := newIndexServer(t, broken, extstore.Sign(broken, priv))
		assert.Equal(t, ErrorInvalid, refreshErr(t, e2eStore(t, srv, key, "")))
	})

	t.Run("server error is a fetch failure", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		srv.status = http.StatusBadGateway
		assert.Equal(t, ErrorFetch, refreshErr(t, e2eStore(t, srv, key, "")))
	})

	t.Run("unreachable host is a fetch failure", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		s := e2eStore(t, srv, key, "")
		srv.Close()
		assert.Equal(t, ErrorFetch, refreshErr(t, s))
	})

	t.Run("oversized index is a fetch failure", func(t *testing.T) {
		huge := make([]byte, maxIndexBytes+1)
		srv := newIndexServer(t, huge, extstore.Sign(huge, priv))
		assert.Equal(t, ErrorFetch, refreshErr(t, e2eStore(t, srv, key, "")))
	})

	t.Run("a failed refresh keeps the last verified index for StoreIndex", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		s := e2eStore(t, srv, key, "")
		require.NoError(t, s.Refresh(context.Background()))
		srv.status = http.StatusBadGateway
		assert.Equal(t, ErrorFetch, refreshErr(t, s))
		_, _, ok := s.StoreIndex()
		assert.True(t, ok)

		srv.status = http.StatusOK
		require.NoError(t, s.Refresh(context.Background()))
		assert.Nil(t, s.State("en").Error, "a later success clears the failure")
	})

	t.Run("a failed refresh still reports the updates the verified index offers", func(t *testing.T) {
		srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
		s := e2eStore(t, srv, key, "")
		require.NoError(t, s.Refresh(context.Background()))
		srv.status = http.StatusBadGateway
		require.Error(t, s.Refresh(context.Background()))

		st := s.State("en")
		require.NotNil(t, st.Error)
		require.Len(t, st.Updates, 1, "kafka 0.1.0 is installed and the verified index offers 0.4.1")
		assert.Equal(t, "kafka", st.Updates[0].Name)
		assert.Equal(t, "0.4.1", st.Updates[0].Version)
	})
}

// The "Extension downloads" setting is a registry host, optionally followed by
// the path a mirror keeps the upstream registry under.
func TestPullRegistryFollowsTheMirrorSetting(t *testing.T) {
	t.Setenv("OPSKAT_E2E", "")
	// LoadConfig binds the config path once per process, so the directory has to
	// outlive this test: later tests in the package save to it too.
	dir, err := os.MkdirTemp("", "opskat-extstore-test-*")
	require.NoError(t, err)
	_, err = bootstrap.LoadConfig(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, bootstrap.SaveConfig(&bootstrap.AppConfig{})) })
	for mirror, want := range map[string]ociclient.Registry{
		"":                                 {Host: "ghcr.io"},
		"ghcr.nju.edu.cn":                  {Host: "ghcr.nju.edu.cn"},
		"registry.example.com:5000":        {Host: "registry.example.com:5000"},
		"mirror.example.com/ghcr.io":       {Host: "mirror.example.com", Prefix: "ghcr.io"},
		"harbor.example.com:8443/proxy/gh": {Host: "harbor.example.com:8443", Prefix: "proxy/gh"},
		"[::1]:5000/ghcr.io":               {Host: "[::1]:5000", Prefix: "ghcr.io"},
	} {
		require.NoError(t, bootstrap.SaveConfig(&bootstrap.AppConfig{ExtensionMirror: mirror}))
		assert.Equal(t, want, PullRegistry(), "mirror %q", mirror)
	}
}

func TestE2EOverrides(t *testing.T) {
	t.Run("ignored outside a verification run", func(t *testing.T) {
		t.Setenv("OPSKAT_E2E", "")
		t.Setenv(EnvIndexURL, "http://127.0.0.1:1/index.json")
		t.Setenv(EnvPublicKeys, "a2V5")
		t.Setenv(EnvRegistryHost, "127.0.0.1:5000")
		assert.Equal(t, OfficialIndexURL, indexURL())
		assert.Equal(t, officialPublicKeys, trustedKeyText())
		assert.Equal(t, ociclient.Registry{Host: bootstrap.DefaultExtensionRegistryHost}, PullRegistry())
	})

	t.Run("OPSKAT_E2E=1 honors each override", func(t *testing.T) {
		t.Setenv("OPSKAT_E2E", "1")
		t.Setenv(EnvIndexURL, "http://127.0.0.1:1/index.json")
		t.Setenv(EnvPublicKeys, "a2V5, b3RoZXI=")
		t.Setenv(EnvRegistryHost, "127.0.0.1:5000")
		assert.Equal(t, "http://127.0.0.1:1/index.json", indexURL())
		assert.Equal(t, []string{"a2V5", "b3RoZXI="}, trustedKeyText())
		assert.Equal(t, ociclient.Registry{Host: "127.0.0.1:5000"}, PullRegistry())

		t.Setenv(EnvRegistryHost, "http://127.0.0.1:5000")
		assert.Equal(t, ociclient.Registry{Host: "127.0.0.1:5000", PlainHTTP: true}, PullRegistry())
	})

	t.Run("OPSKAT_E2E=1 without overrides uses the official values", func(t *testing.T) {
		t.Setenv("OPSKAT_E2E", "1")
		t.Setenv(EnvIndexURL, "")
		t.Setenv(EnvPublicKeys, "")
		t.Setenv(EnvRegistryHost, "")
		assert.Equal(t, OfficialIndexURL, indexURL())
		assert.Equal(t, officialPublicKeys, trustedKeyText())
		assert.Equal(t, ociclient.Registry{Host: bootstrap.DefaultExtensionRegistryHost}, PullRegistry())
	})
}

func TestOfficialPublicKeys(t *testing.T) {
	t.Setenv("OPSKAT_E2E", "")
	keys, err := trustedKeys()
	require.NoError(t, err)
	assert.NotEmpty(t, keys, "a release must trust at least one official index key, or the store rejects every index")
}
