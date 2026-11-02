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

func TestPackageSHA256CoversWasmAndManifest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"a":1}`), 0o600))
	base, err := PackageSHA256(dir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"a":2}`), 0o600))
	changedManifest, err := PackageSHA256(dir)
	require.NoError(t, err)
	assert.NotEqual(t, base, changedManifest, "a manifest change (permissions) must change the digest")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"a":1}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm2"), 0o600))
	changedWasm, err := PackageSHA256(dir)
	require.NoError(t, err)
	assert.NotEqual(t, base, changedWasm)

	// Installs copy only regular files; anything else in the directory cannot be vouched for.
	require.NoError(t, os.Symlink(filepath.Join(dir, "main.wasm"), filepath.Join(dir, "link.wasm")))
	_, err = PackageSHA256(dir)
	assert.Error(t, err)
}

// The guest can read any file of its own package (permissions.fs.read ${EXT_DIR}/**),
// and locales / SKILL.md change what the host shows: editing any of them after an
// install must change the digest, or a tampered reviewer would still read as signed.
func TestPackageSHA256CoversEveryPackageFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.wasm"), []byte("wasm"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"a":1}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "rules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rules", "review.yaml"), []byte("block: rm"), 0o600))
	base, err := PackageSHA256(dir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "rules", "review.yaml"), []byte("block: none"), 0o600))
	edited, err := PackageSHA256(dir)
	require.NoError(t, err)
	assert.NotEqual(t, base, edited, "a data file the guest reads changed")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "rules", "review.yaml"), []byte("block: rm"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("x"), 0o600))
	added, err := PackageSHA256(dir)
	require.NoError(t, err)
	assert.NotEqual(t, base, added, "a file added to the package")
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
			want, err := PackageSHA256(filepath.Join(e.extDir, "ext-prov"))
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

// The digest is recomputed from the files on disk each time the facts are asked
// for: swapping the wasm after install no longer matches the recorded digest.
func TestProvenanceRecomputesDigest(t *testing.T) {
	ctx := context.Background()
	e := newProvenanceEnv(t, "ext-prov")
	_, err := e.svc.InstallStorePackage(ctx, e.src, approveInstall)
	require.NoError(t, err)
	installed, err := PackageSHA256(filepath.Join(e.extDir, "ext-prov"))
	require.NoError(t, err)

	p, err := e.svc.Provenance(ctx, "ext-prov")
	require.NoError(t, err)
	assert.Equal(t, Provenance{Recorded: true, Source: InstallOriginStore, PackageSHA256: installed, DigestMatches: true}, p)

	require.NoError(t, os.WriteFile(filepath.Join(e.extDir, "ext-prov", "main.wasm"), []byte("swapped"), 0o600))
	p, err = e.svc.Provenance(ctx, "ext-prov")
	require.NoError(t, err)
	assert.False(t, p.DigestMatches)
	assert.NotEqual(t, installed, p.PackageSHA256, "the digest of what is on disk now")
}

func TestProvenanceWithoutRecord(t *testing.T) {
	e := newProvenanceEnv(t, "ext-prov")
	p, err := e.svc.Provenance(context.Background(), "unknown")
	require.NoError(t, err)
	assert.False(t, p.Recorded)
}
