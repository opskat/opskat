package extension_svc

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/repository/extension_state_repo/mock_extension_state_repo"
	"github.com/opskat/opskat/pkg/extension"
)

// writeVersionedExtension writes an extension source <parent>/<name> at version
// with caps, and seeds its describe() answer (display name key + icon + locale).
func writeVersionedExtension(t *testing.T, stub *describeCacheStub, parent, name, version string, caps map[string]any) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "locales"), 0o755))
	manifest := map[string]any{
		"name":         name,
		"version":      version,
		"hostABI":      extension.HostABIVersion,
		"backend":      map[string]any{"runtime": "wasm", "binary": "main.wasm"},
		"capabilities": caps,
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), minimalWASM, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "locales", "zh-CN.json"),
		[]byte(`{"ext.name":"示例扩展"}`), 0o600))
	stub.put(name, map[string]any{
		"icon": "database#336791",
		"i18n": map[string]any{"displayName": "ext.name", "description": "ext.name"},
		"assetTypes": []map[string]any{
			{"type": name, "i18n": map[string]any{"name": name}, "configSchema": testConfigSchema()},
		},
		"policies": map[string]any{"type": name},
	})
	return dir
}

func dirContentSize(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	require.NoError(t, filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	}))
	return total
}

func zipExtension(t *testing.T, srcDir string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "pkg.zip")
	f, err := os.Create(zipPath) //nolint:gosec // test TempDir
	require.NoError(t, err)
	w := zip.NewWriter(f)
	require.NoError(t, filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // test TempDir
		if err != nil {
			return err
		}
		fw, err := w.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	}))
	require.NoError(t, w.Close())
	require.NoError(t, f.Close())
	return zipPath
}

func newConfirmTestService(t *testing.T, extDir string) *Service {
	t.Helper()
	ctrl := gomock.NewController(t)
	stateRepo := mock_extension_state_repo.NewMockExtensionStateRepo(ctrl)
	stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil).AnyTimes()
	stateRepo.EXPECT().Find(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("not found")).AnyTimes()
	stateRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	svc := New(newTestManager(extDir), stateRepo, nil, nil, zap.NewNop(), nil, nil)
	require.NoError(t, svc.Init(context.Background()))
	t.Cleanup(func() {
		for _, name := range svc.Bridge().ListNames() {
			extreg.Unregister(name)
		}
		svc.Close(context.Background())
	})
	return svc
}

// extDirEntries lists what is in the extensions directory, ignoring the
// compilation cache.
func extDirEntries(t *testing.T, extDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(extDir)
	require.NoError(t, err)
	out := []string{}
	for _, e := range entries {
		if e.Name() != ".cache" {
			out = append(out, e.Name())
		}
	}
	return out
}

var langCtx = aictx.WithPolicyLang(context.Background(), "zh-CN")

// A local directory install shows the confirm after staging — the package's
// display name, icon and capabilities are known — and lands nothing until the
// user accepts. The service lock is not held while the user decides.
func TestInstallConfirmBeforeCommit_Directory(t *testing.T) {
	stub := installDescribeStub(t)
	extDir := t.TempDir()
	svc := newConfirmTestService(t, extDir)
	src := writeVersionedExtension(t, stub, t.TempDir(), "ext-confirm", "1.2.0", map[string]any{
		"http":        map[string]any{"allowlist": []string{"https://api.example.com/"}},
		"credentials": "read",
	})

	var got InstallConfirm
	calls := 0
	m, err := svc.Install(langCtx, src, func(_ context.Context, c InstallConfirm) error {
		calls++
		got = c
		// Nothing has landed yet: only the staging area exists.
		for _, name := range extDirEntries(t, extDir) {
			assert.NotEqual(t, "ext-confirm", name, "nothing may land before the user confirms")
		}
		// The service lock is free while the user decides.
		done := make(chan struct{})
		go func() { svc.ListInstalled("en"); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("service lock held while waiting for the user")
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "ext-confirm", m.Name)
	assert.Equal(t, 1, calls)

	assert.Equal(t, "ext-confirm", got.Name)
	assert.Equal(t, "示例扩展", got.DisplayName)
	assert.Equal(t, "database#336791", got.Icon)
	assert.Equal(t, "", got.From)
	assert.Equal(t, "1.2.0", got.To)
	assert.False(t, got.Downgrade)
	assert.Equal(t, InstallSourceDir, got.Source)
	assert.Equal(t, dirContentSize(t, src), got.Size)
	assert.ElementsMatch(t, []CapabilityGrant{
		{Kind: CapHTTP, Value: "https://api.example.com/"},
		{Kind: CapCredentials, Value: extension.CredentialAccessRead},
	}, got.Capabilities)
	assert.Empty(t, got.Added, "a fresh install has nothing to compare against")

	assert.NotNil(t, svc.Manager().GetExtension("ext-confirm"))
	assert.Equal(t, []string{"ext-confirm"}, extDirEntries(t, extDir))
}

// A ZIP is described by its file size and reported as a local file.
func TestInstallConfirm_ZipSourceAndSize(t *testing.T) {
	stub := installDescribeStub(t)
	extDir := t.TempDir()
	svc := newConfirmTestService(t, extDir)
	zipPath := zipExtension(t, writeVersionedExtension(t, stub, t.TempDir(), "ext-zip", "1.0.0", nil))
	info, err := os.Stat(zipPath)
	require.NoError(t, err)

	var got InstallConfirm
	_, err = svc.Install(langCtx, zipPath, func(_ context.Context, c InstallConfirm) error { got = c; return nil })
	require.NoError(t, err)
	assert.Equal(t, InstallSourceFile, got.Source)
	assert.Equal(t, info.Size(), got.Size)
	assert.Empty(t, got.Capabilities)
}

// An upgrade shows old → new and marks the grants the old version did not have;
// a downgrade is flagged.
func TestInstallConfirm_UpgradeAddedAndDowngrade(t *testing.T) {
	stub := installDescribeStub(t)
	extDir := t.TempDir()
	svc := newConfirmTestService(t, extDir)
	oldCaps := map[string]any{"http": map[string]any{"allowlist": []string{"https://a.example.com/"}}}
	_, err := svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-up", "1.0.0", oldCaps), nil)
	require.NoError(t, err)

	newCaps := map[string]any{
		"http":    map[string]any{"allowlist": []string{"https://a.example.com/", "https://b.example.com/"}},
		"fs":      map[string]any{"read": []string{"/tmp/x"}},
		"tunnel":  true,
		"network": map[string]any{"assetEndpoint": true},
	}
	var up InstallConfirm
	_, err = svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-up", "1.10.0", newCaps),
		func(_ context.Context, c InstallConfirm) error { up = c; return nil })
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", up.From)
	assert.Equal(t, "1.10.0", up.To)
	assert.False(t, up.Downgrade, "1.10.0 is newer than 1.0.0 by version order, not string order")
	assert.ElementsMatch(t, []CapabilityGrant{
		{Kind: CapHTTP, Value: "https://b.example.com/"},
		{Kind: CapFSRead, Value: "/tmp/x"},
		{Kind: CapTunnel},
		{Kind: CapAssetEndpoint},
	}, up.Added)

	var down InstallConfirm
	_, err = svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-up", "1.9.0", oldCaps),
		func(_ context.Context, c InstallConfirm) error { down = c; return nil })
	require.NoError(t, err)
	assert.Equal(t, "1.10.0", down.From)
	assert.Equal(t, "1.9.0", down.To)
	assert.True(t, down.Downgrade)
	assert.Empty(t, down.Added)
}

// Declining — or the wait ending for any other reason — discards the staged
// files: a fresh install leaves nothing on disk, an upgrade leaves the running
// version loaded, registered and in place.
func TestInstallConfirm_CancelLeavesNothing(t *testing.T) {
	stub := installDescribeStub(t)
	extDir := t.TempDir()
	svc := newConfirmTestService(t, extDir)

	_, err := svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-new", "1.0.0", nil),
		func(context.Context, InstallConfirm) error { return ErrInstallCanceled })
	require.ErrorIs(t, err, ErrInstallCanceled)
	assert.Empty(t, extDirEntries(t, extDir))
	assert.Nil(t, svc.Manager().GetExtension("ext-new"))
	assert.Nil(t, svc.Bridge().Get("ext-new"))

	_, err = svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-keep", "1.0.0", nil), nil)
	require.NoError(t, err)
	old := svc.Manager().GetExtension("ext-keep")

	waitErr := context.DeadlineExceeded
	_, err = svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-keep", "2.0.0", nil),
		func(context.Context, InstallConfirm) error { return waitErr })
	require.ErrorIs(t, err, waitErr)
	assert.Equal(t, []string{"ext-keep"}, extDirEntries(t, extDir))
	assert.Same(t, old, svc.Manager().GetExtension("ext-keep"))
	assert.Same(t, old, svc.Bridge().Get("ext-keep"))
	mi, err := extension.LoadManifestInfo(filepath.Join(extDir, "ext-keep"))
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", mi.Manifest.Version)
}

// What the user approved was "replace From with To". If the installed version
// changed while the dialog was open, that approval no longer describes the
// install, so it is refused and the newer state is kept.
func TestInstallConfirm_InstalledChangedWhileWaiting(t *testing.T) {
	stub := installDescribeStub(t)
	extDir := t.TempDir()
	svc := newConfirmTestService(t, extDir)
	_, err := svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-race", "1.0.0", nil), nil)
	require.NoError(t, err)

	_, err = svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-race", "2.0.0", nil),
		func(context.Context, InstallConfirm) error {
			_, err := svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-race", "3.0.0", nil), nil)
			require.NoError(t, err)
			return nil
		})
	require.Error(t, err)
	assert.Equal(t, "3.0.0", svc.Manager().GetExtension("ext-race").Manifest.Version)
	assert.Equal(t, []string{"ext-race"}, extDirEntries(t, extDir))
}

// The compatibility rule still runs first: an incompatible package never
// reaches the dialog.
func TestInstallConfirm_IncompatibleNeverAsks(t *testing.T) {
	useApp(t, appversion.Info{Version: "1.5.0", Kind: appversion.KindRelease})
	svc := New(newTestManager(t.TempDir()), nil, nil, nil, nil, nil, nil)
	src := sourceWithManifest(t, `{"name":"x","version":"1.0.0","hostABI":"9.0"}`)
	_, err := svc.Install(langCtx, src, func(context.Context, InstallConfirm) error {
		t.Error("an incompatible package must not reach the confirm")
		return nil
	})
	var inc *extension.IncompatibleError
	require.True(t, errors.As(err, &inc))
}

// The store builds its confirm from signed index data before downloading; the
// same builder compares against what is installed.
func TestNewInstallConfirm_FromStoreCandidate(t *testing.T) {
	stub := installDescribeStub(t)
	extDir := t.TempDir()
	svc := newConfirmTestService(t, extDir)
	_, err := svc.Install(langCtx, writeVersionedExtension(t, stub, t.TempDir(), "ext-store", "2.0.0", nil), nil)
	require.NoError(t, err)

	c := svc.NewInstallConfirm(InstallCandidate{
		Name: "ext-store", DisplayName: "Store Ext", Icon: "cloud", Version: "2.1.0",
		Capabilities: extension.Capabilities{Credentials: extension.CredentialAccessRead},
		Source:       InstallSourceStore, Size: 4096,
	})
	assert.Equal(t, InstallConfirm{
		Name: "ext-store", DisplayName: "Store Ext", Icon: "cloud", From: "2.0.0", To: "2.1.0",
		Source: InstallSourceStore, Size: 4096,
		Capabilities: []CapabilityGrant{{Kind: CapCredentials, Value: extension.CredentialAccessRead}},
		Added:        []CapabilityGrant{{Kind: CapCredentials, Value: extension.CredentialAccessRead}},
	}, c)
}
