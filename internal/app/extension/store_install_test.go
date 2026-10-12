package extension

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	"github.com/opskat/opskat/pkg/extstore"
)

// zipDir packs dir's files into an extension package.
func zipDir(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, en := range entries {
		data, err := os.ReadFile(filepath.Join(dir, en.Name())) //nolint:gosec // test temp dir
		require.NoError(t, err)
		w, err := zw.Create(en.Name())
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// storeWorld is a real extension service plus a signed index and an anonymous
// registry serving name's package as version 2.0.0 (a stub extension whose
// installed copy, when installed first, is 1.0.0).
type storeWorld struct {
	e      *Extension
	rec    *eventRecorder
	src    string // the 1.0.0 stub, unpacked
	svc    *extension_svc.Service
	pulls  *int
	extDir string
}

func newStoreWorld(t *testing.T, name, indexSHA string) *storeWorld {
	t.Helper()
	src, stub := writeStubExtension(t, name)
	extension.SetDescribeCache(stub)
	t.Cleanup(func() { extension.SetDescribeCache(nil) })

	// The store's package: the same stub at 2.0.0.
	pkgDir := t.TempDir()
	manifest := fmt.Sprintf(`{"name":%q,"version":"2.0.0","hostABI":%q,"backend":{"runtime":"wasm","binary":"main.wasm"}}`,
		name, extension.HostABIVersion)
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "manifest.json"), []byte(manifest), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.wasm"), stubWASM, 0o600))
	pkg := zipDir(t, pkgDir)
	sum := sha256.Sum256(pkg)
	if indexSHA == "" {
		indexSHA = hex.EncodeToString(sum[:])
	}

	pulls := 0
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/2.0.0"):
			pulls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"layers": []map[string]any{{
					"mediaType": "application/zip", "digest": "sha256:" + hex.EncodeToString(sum[:]), "size": len(pkg),
				}},
			})
		case strings.Contains(r.URL.Path, "/blobs/"):
			_, _ = w.Write(pkg)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(reg.Close)

	raw, err := json.Marshal(map[string]any{"format": extstore.FormatVersion, "extensions": []any{map[string]any{
		"name": name, "display": map[string]any{"en": map[string]any{"name": "Stub"}},
		"versions": []any{map[string]any{
			"version": "2.0.0", "hostABI": extension.HostABIVersion, "size": len(pkg),
			"capabilities": map[string]any{},
			"source":       map[string]any{"ref": "ghcr.io/opskat/extensions/" + name + ":2.0.0", "sha256": indexSHA},
		}},
	}}})
	require.NoError(t, err)
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sig") {
			_, _ = w.Write(extstore.Sign(raw, priv))
			return
		}
		_, _ = w.Write(raw)
	}))
	t.Cleanup(idx.Close)
	t.Setenv("OPSKAT_E2E", "1")
	t.Setenv(extstore_svc.EnvIndexURL, idx.URL+"/index.json")
	t.Setenv(extstore_svc.EnvPublicKeys, base64.StdEncoding.EncodeToString(pub))
	t.Setenv(extstore_svc.EnvRegistryHost, reg.URL)

	extDir := t.TempDir()
	ctrl := gomock.NewController(t)
	stateRepo := mock_extension_state_repo.NewMockExtensionStateRepo(ctrl)
	stateRepo.EXPECT().Find(gomock.Any(), name).Return(nil, fmt.Errorf("not found")).AnyTimes()
	stateRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	manager := extension.NewManager(extDir, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{Logger: zap.NewNop()})
	}, zap.NewNop())
	svc := extension_svc.New(manager, stateRepo, nil, nil, zap.NewNop(), nil, nil)
	t.Cleanup(func() {
		extreg.Unregister(name)
		svc.Close(context.Background())
	})

	e, rec := newConfirmBinder(context.Background())
	e.SetService(svc)
	e.SetStoreService(extstore_svc.New(extstore_svc.Options{
		DownloadMirror:    func() string { return "" },
		InstalledVersions: func() map[string]string { return InstalledVersions(e) },
		App:               func() appversion.Info { return appversion.Info{Version: "1.0.0", Kind: appversion.KindDev} },
	}))
	return &storeWorld{e: e, rec: rec, src: src, svc: svc, pulls: &pulls, extDir: extDir}
}

// installLocal puts the 1.0.0 stub in place, as if installed earlier.
func (w *storeWorld) installLocal(t *testing.T) {
	t.Helper()
	_, _, err := InstallExtensionDir(w.e, context.Background(), w.src)
	require.NoError(t, err)
}

// install runs the binding and answers the confirm with answer, returning the
// result and every event emitted.
func (w *storeWorld) install(t *testing.T, name string, answer bool) (extstore_svc.InstallResult, []emitted) {
	t.Helper()
	done := make(chan extstore_svc.InstallResult, 1)
	go func() {
		res, err := w.e.InstallStoreExtension(name)
		require.NoError(t, err)
		done <- res
	}()
	ev := w.rec.next(t)
	require.Equal(t, "ext:install-confirm", ev.name)
	assert.Zero(t, *w.pulls, "nothing is pulled before the confirm is answered")
	require.NoError(t, w.e.RespondExtensionInstallConfirm(ev.payload["id"].(string), answer))
	select {
	case res := <-done:
		w.rec.mu.Lock()
		defer w.rec.mu.Unlock()
		return res, append([]emitted(nil), w.rec.events...)
	case <-time.After(10 * time.Second):
		t.Fatal("store install did not return")
		return extstore_svc.InstallResult{}, nil
	}
}

func TestInstallStoreExtension_UpdatesAfterConfirm(t *testing.T) {
	w := newStoreWorld(t, "ext-store", "")
	w.installLocal(t)

	res, events := w.install(t, "ext-store", true)
	require.Nil(t, res.Error)
	assert.False(t, res.Canceled)
	assert.Equal(t, "ext-store", res.Name)
	assert.Equal(t, "2.0.0", res.Version)
	assert.Equal(t, "2.0.0", w.svc.Manager().GetExtension("ext-store").Manifest.Version)

	confirm := events[0].payload
	assert.Equal(t, "store", confirm["source"])
	assert.Equal(t, "1.0.0", confirm["from"])
	assert.Equal(t, "2.0.0", confirm["to"])
	assert.Equal(t, "Stub", confirm["displayName"])

	var phases []string
	for _, ev := range events[1:] {
		require.Equal(t, "ext:store-progress", ev.name)
		assert.Equal(t, "ext-store", ev.payload["name"])
		if p := ev.payload["phase"].(string); len(phases) == 0 || phases[len(phases)-1] != p {
			phases = append(phases, p)
		}
	}
	assert.Equal(t, []string{"downloading", "verifying", "installing"}, phases)
}

func TestInstallStoreExtension_CancelIsSilentAndPullsNothing(t *testing.T) {
	w := newStoreWorld(t, "ext-store-cancel", "")
	res, events := w.install(t, "ext-store-cancel", false)
	assert.True(t, res.Canceled)
	assert.Nil(t, res.Error)
	assert.Len(t, events, 1, "no progress without a confirm")
	assert.Zero(t, *w.pulls)
	assert.Nil(t, w.svc.Manager().GetExtension("ext-store-cancel"))
}

func TestInstallStoreExtension_DigestMismatchLeavesTheInstalledVersion(t *testing.T) {
	want := strings.Repeat("ab", 32)
	w := newStoreWorld(t, "ext-store-bad", want)
	w.installLocal(t)

	res, _ := w.install(t, "ext-store-bad", true)
	require.NotNil(t, res.Error)
	assert.Equal(t, extstore_svc.InstallErrDigest, res.Error.Kind)
	assert.Equal(t, want, res.Error.Expected)
	assert.NotEmpty(t, res.Error.Actual)
	assert.NotEqual(t, want, res.Error.Actual)

	ext := w.svc.Manager().GetExtension("ext-store-bad")
	require.NotNil(t, ext, "the installed version keeps running")
	assert.Equal(t, "1.0.0", ext.Manifest.Version)
	data, err := os.ReadFile(filepath.Join(w.extDir, "ext-store-bad", "manifest.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"version":"1.0.0"`)
}

func TestInstallStoreExtension_WithoutServices(t *testing.T) {
	e := &Extension{ctx: context.Background(), lang: fixedLang("en")}
	_, err := e.InstallStoreExtension("x")
	require.Error(t, err)
}
