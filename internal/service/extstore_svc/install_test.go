package extstore_svc

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

// packageZip is an extension package: a zip with manifest.json at its root.
func packageZip(t *testing.T, name, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("manifest.json")
	require.NoError(t, err)
	_, err = fmt.Fprintf(w, `{"name":%q,"version":%q,"hostABI":"2.2"}`, name, version)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func hexSum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// registry is a minimal anonymous OCI registry with a Bearer challenge, serving
// one single-layer package under any repository.
type registry struct {
	*httptest.Server
	mu          sync.Mutex
	blob        []byte
	requests    []string
	tokenStatus int
	manifest404 bool
}

func newRegistry(t *testing.T, blob []byte) *registry {
	r := &registry{blob: blob}
	r.Server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.Close)
	return r
}

func (r *registry) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req.URL.Path)
	if req.URL.Path == "/token" {
		if r.tokenStatus != 0 {
			w.WriteHeader(r.tokenStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "anon"})
		return
	}
	if req.Header.Get("Authorization") != "Bearer anon" {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="test"`, r.URL))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case strings.Contains(req.URL.Path, "/manifests/"):
		if r.manifest404 {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.manifest.v1+json",
			"artifactType":  "application/vnd.opskat.extension.v1",
			"config": map[string]any{
				"mediaType": "application/vnd.oci.empty.v1+json", "digest": "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a", "size": 2,
			},
			"layers": []map[string]any{{
				"mediaType": "application/zip", "digest": "sha256:" + hexSum(r.blob), "size": len(r.blob),
			}},
		})
	case strings.Contains(req.URL.Path, "/blobs/"):
		_, _ = w.Write(r.blob)
	default:
		http.NotFound(w, req)
	}
}

func (r *registry) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

// fakeInstaller stands in for extension_svc: it describes candidates the way
// NewInstallConfirm does, and on Install opens the downloaded package, asks
// confirm with the staged description (staged, or one built from the package
// when nil) and reports what landed.
type fakeInstaller struct {
	t         *testing.T
	installed map[string]string
	staged    func(extension_svc.InstallConfirm) extension_svc.InstallConfirm
	failWith  error

	installPath string
	installed2  []byte
	confirmErr  error
}

func (f *fakeInstaller) NewInstallConfirm(c extension_svc.InstallCandidate) extension_svc.InstallConfirm {
	return extension_svc.InstallConfirm{
		Name: c.Name, DisplayName: c.DisplayName, Icon: c.Icon, From: f.installed[c.Name], To: c.Version,
		Source: c.Source, Size: c.Size, Capabilities: grants(c.Capabilities), Added: []extension_svc.CapabilityGrant{},
	}
}

func grants(c extension.Capabilities) []extension_svc.CapabilityGrant {
	out := []extension_svc.CapabilityGrant{}
	if c.Credentials != "" {
		out = append(out, extension_svc.CapabilityGrant{Kind: extension_svc.CapCredentials, Value: c.Credentials})
	}
	if c.Network.AssetEndpoint {
		out = append(out, extension_svc.CapabilityGrant{Kind: extension_svc.CapAssetEndpoint})
	}
	return out
}

func (f *fakeInstaller) Install(ctx context.Context, path string, confirm extension_svc.ConfirmInstallFunc) (*extension.Manifest, error) {
	f.installPath = path
	data, err := os.ReadFile(path) //nolint:gosec // test temp path
	require.NoError(f.t, err)
	f.installed2 = data
	if f.failWith != nil {
		return nil, f.failWith
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(f.t, err)
	rc, err := zr.File[0].Open()
	require.NoError(f.t, err)
	var m extension.Manifest
	require.NoError(f.t, json.NewDecoder(rc).Decode(&m))
	_ = rc.Close()

	staged := extension_svc.InstallConfirm{
		Name: m.Name, To: m.Version, From: f.installed[m.Name], Source: extension_svc.InstallSourceFile,
		Capabilities: grants(storeCaps),
	}
	if f.staged != nil {
		staged = f.staged(staged)
	}
	if err := confirm(ctx, staged); err != nil {
		f.confirmErr = err
		return nil, err
	}
	return &m, nil
}

var storeCaps = extension.Capabilities{Credentials: "read", Network: extension.NetworkCapability{AssetEndpoint: true}}

// storeFixture is a verified store whose index offers "demo" 1.1.0 (installed:
// 1.0.0 when installedVersion is set), packaged by pkg and pulled from reg.
type storeFixture struct {
	svc       *Service
	reg       *registry
	installer *fakeInstaller
	tmp       string
}

type fixtureOpts struct {
	installed string
	sha       string // overrides the index sha256
	size      int64  // overrides the index size
	hostABI   string // overrides every version's hostABI
}

func newStoreFixture(t *testing.T, o fixtureOpts) *storeFixture {
	t.Helper()
	pkg := packageZip(t, "demo", "1.1.0")
	reg := newRegistry(t, pkg)
	sha, size, hostABI := hexSum(pkg), int64(len(pkg)), "2.2"
	if o.sha != "" {
		sha = o.sha
	}
	if o.size != 0 {
		size = o.size
	}
	if o.hostABI != "" {
		hostABI = o.hostABI
	}
	raw := indexJSON(t, extstore.FormatVersion,
		map[string]any{
			"name": "demo",
			"icon": "puzzle",
			"display": map[string]any{
				"en":    map[string]any{"name": "Demo", "description": "A demo"},
				"zh-CN": map[string]any{"name": "演示扩展"},
			},
			"versions": []any{
				map[string]any{"version": "1.0.0", "hostABI": hostABI, "size": size,
					"capabilities": map[string]any{},
					"source":       map[string]any{"ref": "ghcr.io/opskat/extensions/demo:1.0.0", "sha256": sha}},
				map[string]any{"version": "1.1.0", "hostABI": hostABI, "size": size,
					"capabilities": map[string]any{"credentials": "read", "network": map[string]any{"assetEndpoint": true}},
					"source":       map[string]any{"ref": "ghcr.io/opskat/extensions/demo:1.1.0", "sha256": sha}},
			},
		})
	key, priv := newKey(t)
	idx := newIndexServer(t, raw, extstore.Sign(raw, priv))
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("OPSKAT_E2E", "1")
	t.Setenv(EnvIndexURL, idx.URL+"/index.json")
	t.Setenv(EnvPublicKeys, key)
	t.Setenv(EnvRegistryHost, reg.URL) // http://127.0.0.1:port — the e2e passthrough

	installed := map[string]string{}
	if o.installed != "" {
		installed["demo"] = o.installed
	}
	svc := New(Options{
		DownloadMirror:    func() string { return "" },
		InstalledVersions: func() map[string]string { return installed },
		App:               func() appversion.Info { return release },
	})
	require.NoError(t, svc.Refresh(context.Background()))
	return &storeFixture{svc: svc, reg: reg, installer: &fakeInstaller{t: t, installed: installed}, tmp: tmp}
}

// run installs "demo" with confirm, recording progress and what confirm saw.
func (f *storeFixture) run(ctx context.Context, confirm extension_svc.ConfirmInstallFunc) (string, string, []Progress, error) {
	var progress []Progress
	name, version, err := f.svc.InstallFromStore(ctx, "demo", InstallOptions{
		Installer:  f.installer,
		Confirm:    confirm,
		OnProgress: func(p Progress) { progress = append(progress, p) },
	})
	return name, version, progress, err
}

func (f *storeFixture) assertNoTempLeft(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(f.tmp)
	require.NoError(t, err)
	assert.Empty(t, entries, "the download is removed whatever the outcome")
}

func accept(context.Context, extension_svc.InstallConfirm) error { return nil }

func TestInstallFromStoreConfirmsFromIndexBeforeDownloading(t *testing.T) {
	f := newStoreFixture(t, fixtureOpts{installed: "1.0.0"})
	var asked extension_svc.InstallConfirm
	// The desktop binder passes System.Lang, which is lowercased ("zh-cn"); the
	// index keys display text by "zh-CN".
	ctx := aictx.WithPolicyLang(context.Background(), "zh-cn")
	name, version, progress, err := f.run(ctx, func(_ context.Context, c extension_svc.InstallConfirm) error {
		asked = c
		assert.Empty(t, f.reg.calls(), "nothing is downloaded before the user confirms")
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "demo", name)
	assert.Equal(t, "1.1.0", version)

	assert.Equal(t, extension_svc.InstallSourceStore, asked.Source)
	assert.Equal(t, "演示扩展", asked.DisplayName)
	assert.Equal(t, "puzzle", asked.Icon)
	assert.Equal(t, "1.0.0", asked.From)
	assert.Equal(t, "1.1.0", asked.To)
	assert.Equal(t, int64(len(f.reg.blob)), asked.Size)
	assert.Equal(t, grants(storeCaps), asked.Capabilities)

	assert.Equal(t, f.reg.blob, f.installer.installed2, "the pulled package is what gets installed")
	assert.Contains(t, f.reg.calls(), "/v2/opskat/extensions/demo/manifests/1.1.0",
		"the ref's repository is pulled from the configured registry host, not the host in the ref")

	require.NotEmpty(t, progress)
	phases := []Phase{}
	for _, p := range progress {
		assert.Equal(t, "demo", p.Name)
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
	}
	assert.Equal(t, []Phase{PhaseDownloading, PhaseVerifying, PhaseInstalling}, phases)
	last := progress[len(progress)-1]
	assert.Equal(t, last.Total, last.Done)
	assert.Equal(t, int64(len(f.reg.blob)), last.Total)
	f.assertNoTempLeft(t)
}

func TestInstallFromStoreCancelDownloadsNothing(t *testing.T) {
	f := newStoreFixture(t, fixtureOpts{})
	_, _, progress, err := f.run(context.Background(), func(context.Context, extension_svc.InstallConfirm) error {
		return extension_svc.ErrInstallCanceled
	})
	assert.ErrorIs(t, err, extension_svc.ErrInstallCanceled)
	assert.Empty(t, f.reg.calls())
	assert.Empty(t, progress)
	assert.Empty(t, f.installer.installPath, "nothing reaches the installer")
	assert.True(t, InstallOutcome("demo", "", err).Canceled)
	assert.Nil(t, InstallOutcome("demo", "", err).Error, "a cancel is silent")
	f.assertNoTempLeft(t)
}

func TestInstallFromStoreDownloadFailures(t *testing.T) {
	cases := []struct {
		name  string
		opts  fixtureOpts
		setup func(*storeFixture)
		kind  InstallErrorKind
	}{
		{name: "sha256 mismatch", opts: fixtureOpts{sha: strings.Repeat("0", 64)}, kind: InstallErrDigest},
		{name: "larger than the index says", opts: fixtureOpts{size: 10}, kind: InstallErrSize},
		{name: "auth challenge fails", setup: func(f *storeFixture) { f.reg.tokenStatus = http.StatusInternalServerError }, kind: InstallErrAuth},
		{name: "registry has no such manifest", setup: func(f *storeFixture) { f.reg.manifest404 = true }, kind: InstallErrRegistry},
		{name: "registry unreachable", setup: func(f *storeFixture) { f.reg.Close() }, kind: InstallErrNetwork},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStoreFixture(t, tc.opts)
			if tc.setup != nil {
				tc.setup(f)
			}
			_, _, _, err := f.run(context.Background(), accept)
			var ie *InstallError
			require.ErrorAs(t, err, &ie)
			assert.Equal(t, tc.kind, ie.Kind)
			assert.Empty(t, f.installer.installPath, "a failed download never reaches the installer")
			f.assertNoTempLeft(t)

			out := InstallOutcome("demo", "", err)
			require.NotNil(t, out.Error)
			assert.Equal(t, tc.kind, out.Error.Kind)
			assert.NotEmpty(t, out.Error.Message)
		})
	}

	t.Run("sha256 mismatch names expected and actual", func(t *testing.T) {
		want := strings.Repeat("0", 64)
		f := newStoreFixture(t, fixtureOpts{sha: want})
		_, _, _, err := f.run(context.Background(), accept)
		out := InstallOutcome("demo", "", err)
		require.NotNil(t, out.Error)
		assert.Equal(t, want, out.Error.Expected)
		assert.Equal(t, hexSum(f.reg.blob), out.Error.Actual)
	})
}

func TestInstallFromStoreRefusesAPackageOtherThanConfirmed(t *testing.T) {
	cases := map[string]func(extension_svc.InstallConfirm) extension_svc.InstallConfirm{
		"other name":    func(c extension_svc.InstallConfirm) extension_svc.InstallConfirm { c.Name = "evil"; return c },
		"other version": func(c extension_svc.InstallConfirm) extension_svc.InstallConfirm { c.To = "9.9.9"; return c },
		"installed version changed meanwhile": func(c extension_svc.InstallConfirm) extension_svc.InstallConfirm {
			c.From = "1.0.5"
			return c
		},
		"other capabilities": func(c extension_svc.InstallConfirm) extension_svc.InstallConfirm {
			c.Capabilities = append(c.Capabilities, extension_svc.CapabilityGrant{Kind: extension_svc.CapTunnel})
			return c
		},
	}
	for name, staged := range cases {
		t.Run(name, func(t *testing.T) {
			f := newStoreFixture(t, fixtureOpts{installed: "1.0.0"})
			f.installer.staged = staged
			_, _, progress, err := f.run(context.Background(), accept)
			var ie *InstallError
			require.ErrorAs(t, err, &ie)
			assert.Equal(t, InstallErrMismatch, ie.Kind)
			assert.Same(t, f.installer.confirmErr, err, "the staged install is refused at its confirm step")
			for _, p := range progress {
				assert.NotEqual(t, PhaseInstalling, p.Phase)
			}
			f.assertNoTempLeft(t)
		})
	}
}

func TestInstallFromStoreStagingFailures(t *testing.T) {
	t.Run("incompatible package", func(t *testing.T) {
		f := newStoreFixture(t, fixtureOpts{})
		f.installer.failWith = fmt.Errorf("localized: %w", &extension.IncompatibleError{Reason: extension.ReasonHostABI, HostABI: "9.0"})
		_, _, _, err := f.run(context.Background(), accept)
		out := InstallOutcome("demo", "", err)
		require.NotNil(t, out.Error)
		assert.Equal(t, InstallErrIncompatible, out.Error.Kind)
		f.assertNoTempLeft(t)
	})

	t.Run("any other install failure", func(t *testing.T) {
		f := newStoreFixture(t, fixtureOpts{})
		f.installer.failWith = errors.New("zip exceeds entry limit")
		_, _, _, err := f.run(context.Background(), accept)
		out := InstallOutcome("demo", "", err)
		require.NotNil(t, out.Error)
		assert.Equal(t, InstallErrInstall, out.Error.Kind)
		assert.Contains(t, out.Error.Message, "zip exceeds entry limit")
		f.assertNoTempLeft(t)
	})

	t.Run("canceled mid-download is silent and leaves nothing", func(t *testing.T) {
		f := newStoreFixture(t, fixtureOpts{})
		ctx, cancel := context.WithCancel(context.Background())
		var progressSeen bool
		_, _, err := f.svc.InstallFromStore(ctx, "demo", InstallOptions{
			Installer: f.installer,
			Confirm:   accept,
			OnProgress: func(p Progress) {
				if p.Phase == PhaseDownloading && !progressSeen {
					progressSeen = true
					cancel()
				}
			},
		})
		require.ErrorIs(t, err, context.Canceled)
		assert.True(t, InstallOutcome("demo", "", err).Canceled)
		f.assertNoTempLeft(t)
	})
}

func TestInstallFromStoreOnlyInstallsTheOffer(t *testing.T) {
	never := func(context.Context, extension_svc.InstallConfirm) error {
		t.Fatal("no confirm without an offer")
		return nil
	}
	cases := []struct {
		name string
		opts fixtureOpts
		ext  string
		kind InstallErrorKind
	}{
		{name: "not in the index", ext: "missing", kind: InstallErrNotFound},
		{name: "installed version is the newest", opts: fixtureOpts{installed: "1.1.0"}, ext: "demo", kind: InstallErrInstalled},
		{name: "installed version is newer than the index", opts: fixtureOpts{installed: "2.0.0"}, ext: "demo", kind: InstallErrInstalled},
		{name: "no compatible version", opts: fixtureOpts{hostABI: "9.0"}, ext: "demo", kind: InstallErrIncompatible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStoreFixture(t, tc.opts)
			_, _, err := f.svc.InstallFromStore(context.Background(), tc.ext, InstallOptions{Installer: f.installer, Confirm: never, OnProgress: func(Progress) {}})
			var ie *InstallError
			require.ErrorAs(t, err, &ie)
			assert.Equal(t, tc.kind, ie.Kind)
			assert.Empty(t, f.reg.calls())
		})
	}
}

// The reason reads in the user's language alone, as a local install's does —
// not the localized sentence followed by the English one.
func TestInstallFromStoreIncompatibleReasonIsLocalized(t *testing.T) {
	f := newStoreFixture(t, fixtureOpts{hostABI: "9.0"})
	ctx := aictx.WithPolicyLang(context.Background(), "zh-cn")
	_, _, err := f.svc.InstallFromStore(ctx, "demo", InstallOptions{Installer: f.installer, Confirm: accept, OnProgress: func(Progress) {}})
	var inc *extension.IncompatibleError
	require.ErrorAs(t, err, &inc)
	assert.Equal(t, extension_svc.IncompatibleMessage(ctx, inc), InstallOutcome("demo", "", err).Error.Message)
}

func TestInstallFromStoreOneInstallPerExtension(t *testing.T) {
	f := newStoreFixture(t, fixtureOpts{})
	waiting, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, _, err := f.svc.InstallFromStore(context.Background(), "demo", InstallOptions{
			Installer: f.installer,
			Confirm: func(context.Context, extension_svc.InstallConfirm) error {
				close(waiting)
				<-release
				return extension_svc.ErrInstallCanceled
			},
			OnProgress: func(Progress) {},
		})
		done <- err
	}()
	<-waiting

	_, _, err := f.svc.InstallFromStore(context.Background(), "demo", InstallOptions{
		Installer: f.installer, Confirm: accept, OnProgress: func(Progress) {},
	})
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, InstallErrBusy, ie.Kind, "a second click while the first install is open is refused")

	close(release)
	require.ErrorIs(t, <-done, extension_svc.ErrInstallCanceled)
	_, _, _, err = f.run(context.Background(), accept)
	require.NoError(t, err, "once the first install ends the extension can be installed again")
}

func TestInstallFromStoreNeedsAVerifiedIndex(t *testing.T) {
	_, priv := newKey(t)
	otherKey, _ := newKey(t)
	raw := sampleIndex(t)
	srv := newIndexServer(t, raw, extstore.Sign(raw, priv))
	s := e2eStore(t, srv, otherKey, "")
	_, _, err := s.InstallFromStore(context.Background(), "es", InstallOptions{
		Installer: &fakeInstaller{t: t},
		Confirm: func(context.Context, extension_svc.InstallConfirm) error {
			t.Fatal("nothing is confirmed from an unverified index")
			return nil
		},
		OnProgress: func(Progress) {},
	})
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, InstallErrSignature, ie.Kind)
}

// Plain http is a verification-run passthrough only: an "Extension downloads"
// host written with http:// into config.json by hand (the settings page rejects
// it) is not a registry host, so nothing is pulled over plain http.
func TestInstallFromStoreNeverPullsPlainHTTPFromTheMirrorSetting(t *testing.T) {
	f := newStoreFixture(t, fixtureOpts{})
	_, err := bootstrap.LoadConfig(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, bootstrap.SaveConfig(&bootstrap.AppConfig{ExtensionMirror: f.reg.URL}))
	t.Cleanup(func() { _ = bootstrap.SaveConfig(&bootstrap.AppConfig{}) })
	t.Setenv(EnvRegistryHost, "")

	_, _, _, err = f.run(context.Background(), accept)
	var ie *InstallError
	require.ErrorAs(t, err, &ie)
	assert.Empty(t, f.reg.calls(), "no request reaches the plain-http registry")
	assert.Empty(t, f.installer.installPath)
	f.assertNoTempLeft(t)
}
