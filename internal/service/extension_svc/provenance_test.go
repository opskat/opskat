package extension_svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/pkg/appversion"
)

// memStateRepo is an in-memory extension_state table.
type memStateRepo struct {
	mu        sync.Mutex
	rows      map[string]extension_state_entity.ExtensionState
	failWrite error
}

func newMemStateRepo() *memStateRepo {
	return &memStateRepo{rows: map[string]extension_state_entity.ExtensionState{}}
}

func (r *memStateRepo) Find(_ context.Context, name string) (*extension_state_entity.ExtensionState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[name]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func (r *memStateRepo) FindAll(context.Context) ([]*extension_state_entity.ExtensionState, error) {
	return nil, nil
}

func (r *memStateRepo) Create(_ context.Context, s *extension_state_entity.ExtensionState) error {
	return r.put(s)
}

func (r *memStateRepo) Update(_ context.Context, s *extension_state_entity.ExtensionState) error {
	return r.put(s)
}

func (r *memStateRepo) put(s *extension_state_entity.ExtensionState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failWrite != nil {
		return r.failWrite
	}
	r.rows[s.Name] = *s
	return nil
}

func (r *memStateRepo) Delete(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rows, name)
	return nil
}

func (r *memStateRepo) get(name string) (extension_state_entity.ExtensionState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, ok := r.rows[name]
	return row, ok
}

func TestReviewerEligible(t *testing.T) {
	const sum = "abc"
	state := func(source, digest string) *extension_state_entity.ExtensionState {
		return &extension_state_entity.ExtensionState{Name: "jev", Source: source, PackageSHA256: digest}
	}
	cases := []struct {
		name    string
		state   *extension_state_entity.ExtensionState
		kind    appversion.Kind
		wantErr string // "" = eligible
		signed  bool
	}{
		{"store on release", state(InstallOriginStore, sum), appversion.KindRelease, "", true},
		{"store on nightly", state(InstallOriginStore, sum), appversion.KindNightly, "", true},
		{"local on release", state(InstallOriginLocal, sum), appversion.KindRelease, "store", false},
		{"dev on release", state(InstallOriginDev, sum), appversion.KindRelease, "store", false},
		{"local on nightly is unsigned", state(InstallOriginLocal, sum), appversion.KindNightly, "", false},
		{"dev on dev build is unsigned", state(InstallOriginDev, sum), appversion.KindDev, "", false},
		{"digest mismatch", state(InstallOriginStore, "other"), appversion.KindDev, "digest", false},
		{"no recorded digest", state(InstallOriginStore, ""), appversion.KindDev, "digest", false},
		{"no recorded source", state("", sum), appversion.KindDev, "source", false},
		{"no record", nil, appversion.KindDev, "digest", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signed, err := reviewerEligible(tc.state, sum, appversion.Info{Kind: tc.kind})
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
			assert.Equal(t, tc.signed, signed)
		})
	}
}

func TestPackageSHA256CoversWasmAndManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"a":1}`), 0o600))
	base, err := PackageSHA256(dir, "main.wasm")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"a":2}`), 0o600))
	changedManifest, err := PackageSHA256(dir, "main.wasm")
	require.NoError(t, err)
	assert.NotEqual(t, base, changedManifest, "a manifest change (permissions) must change the digest")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"a":1}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm2"), 0o600))
	changedWasm, err := PackageSHA256(dir, "main.wasm")
	require.NoError(t, err)
	assert.NotEqual(t, base, changedWasm)

	_, err = PackageSHA256(dir, "missing.wasm")
	assert.Error(t, err)
}

type provenanceEnv struct {
	svc    *Service
	repo   *memStateRepo
	src    string
	extDir string
}

func newProvenanceEnv(t *testing.T, name string) *provenanceEnv {
	t.Helper()
	stub := installDescribeStub(t)
	srcRoot := t.TempDir()
	writeTestExtension(stub, srcRoot, name)
	extDir := t.TempDir()
	repo := newMemStateRepo()
	svc := New(newTestManager(extDir), repo, nil, nil, nil, zap.NewNop(), nil, nil)
	t.Cleanup(func() { closeService(context.Background(), svc) })
	return &provenanceEnv{svc: svc, repo: repo, src: filepath.Join(srcRoot, name), extDir: extDir}
}

// Each install path records where the package came from and its digest, over the
// installed files.
func TestInstallRecordsProvenancePerPath(t *testing.T) {
	ctx := context.Background()
	paths := map[string]func(e *provenanceEnv) error{
		InstallOriginLocal: func(e *provenanceEnv) error { _, err := e.svc.Install(ctx, e.src, approveInstall); return err },
		InstallOriginStore: func(e *provenanceEnv) error {
			_, err := e.svc.InstallStorePackage(ctx, e.src, approveInstall)
			return err
		},
		InstallOriginDev: func(e *provenanceEnv) error { _, err := e.svc.InstallDev(ctx, e.src, approveInstall); return err },
	}
	for origin, install := range paths {
		t.Run(origin, func(t *testing.T) {
			e := newProvenanceEnv(t, "ext-prov")
			require.NoError(t, install(e))

			row, ok := e.repo.get("ext-prov")
			require.True(t, ok)
			assert.Equal(t, origin, row.Source)
			assert.True(t, row.Enabled)
			want, err := PackageSHA256(filepath.Join(e.extDir, "ext-prov"), "main.wasm")
			require.NoError(t, err)
			assert.Equal(t, want, row.PackageSHA256)
		})
	}
}

// A failed install leaves the previous record (source and digest) as it was.
func TestFailedInstallRestoresProvenance(t *testing.T) {
	ctx := context.Background()
	e := newProvenanceEnv(t, "ext-prov")
	_, err := e.svc.InstallStorePackage(ctx, e.src, approveInstall)
	require.NoError(t, err)
	before, _ := e.repo.get("ext-prov")

	// The new build does not load: nothing may change in the record.
	require.NoError(t, os.WriteFile(filepath.Join(e.src, "main.wasm"), []byte("not wasm"), 0o600))
	_, err = e.svc.InstallDev(ctx, e.src, approveInstall)
	require.Error(t, err)
	after, ok := e.repo.get("ext-prov")
	require.True(t, ok)
	assert.Equal(t, before.Source, after.Source)
	assert.Equal(t, before.PackageSHA256, after.PackageSHA256)
}

func TestInstallFailsWhenProvenanceCannotBeRecorded(t *testing.T) {
	e := newProvenanceEnv(t, "ext-prov")
	e.repo.failWrite = errors.New("db locked")

	_, err := e.svc.Install(context.Background(), e.src, approveInstall)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db locked")
	assert.Nil(t, e.svc.Manager().GetExtension("ext-prov"), "no install record means no install")
}

// The digest is recomputed from the files on disk each time a reviewer is about to
// be used: swapping the wasm after install makes it ineligible.
func TestReviewerEligibilityRecomputesDigest(t *testing.T) {
	ctx := context.Background()
	useApp(t, appversion.Info{Kind: appversion.KindRelease})
	e := newProvenanceEnv(t, "ext-prov")
	_, err := e.svc.InstallStorePackage(ctx, e.src, approveInstall)
	require.NoError(t, err)

	signed, err := e.svc.ReviewerEligibility(ctx, "ext-prov")
	require.NoError(t, err)
	assert.True(t, signed)

	require.NoError(t, os.WriteFile(filepath.Join(e.extDir, "ext-prov", "main.wasm"), []byte("swapped"), 0o600))
	_, err = e.svc.ReviewerEligibility(ctx, "ext-prov")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "digest")
}

func TestReviewerEligibilityByBuildKind(t *testing.T) {
	ctx := context.Background()
	e := newProvenanceEnv(t, "ext-prov")
	_, err := e.svc.Install(ctx, e.src, approveInstall)
	require.NoError(t, err)

	useApp(t, appversion.Info{Kind: appversion.KindRelease})
	_, err = e.svc.ReviewerEligibility(ctx, "ext-prov")
	require.Error(t, err, "a local install is no reviewer on a release build")

	useApp(t, appversion.Info{Kind: appversion.KindNightly})
	signed, err := e.svc.ReviewerEligibility(ctx, "ext-prov")
	require.NoError(t, err)
	assert.False(t, signed, "usable on a non-release build, but unsigned")
}

func TestReviewerEligibilityWithoutRecord(t *testing.T) {
	e := newProvenanceEnv(t, "ext-prov")
	_, err := e.svc.ReviewerEligibility(context.Background(), "unknown")
	require.Error(t, err)
}
