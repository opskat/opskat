package extension_svc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countDigests makes the service count how often it walks and hashes a package.
func countDigests(svc *Service) *int {
	n := 0
	inner := svc.digests.sum
	svc.digests.sum = func(dir string) (string, error) {
		n++
		return inner(dir)
	}
	return &n
}

// The facts are asked for on every command review; the package is hashed once until
// a file in it changes.
func TestProvenanceHashesPackageOnce(t *testing.T) {
	ctx := context.Background()
	e := newProvenanceEnv(t, "ext-prov")
	_, err := e.svc.InstallStorePackage(ctx, e.src, approveInstall)
	require.NoError(t, err)
	hashed := countDigests(e.svc)

	first, err := e.svc.Provenance(ctx, "ext-prov")
	require.NoError(t, err)
	for range 5 {
		again, err := e.svc.Provenance(ctx, "ext-prov")
		require.NoError(t, err)
		assert.Equal(t, first, again)
	}
	assert.Equal(t, 1, *hashed, "package files read once for repeated asks")
	assert.True(t, first.DigestMatches)

	// Same size, later mtime: still noticed.
	wasm := filepath.Join(e.extDir, "ext-prov", "main.wasm")
	info, err := os.Stat(wasm)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(wasm, make([]byte, info.Size()), 0o600))
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(wasm, later, later))
	changed, err := e.svc.Provenance(ctx, "ext-prov")
	require.NoError(t, err)
	assert.Equal(t, 2, *hashed)
	assert.NotEqual(t, first.PackageSHA256, changed.PackageSHA256)
	assert.False(t, changed.DigestMatches)
}

// BenchmarkPackageDigest compares hashing a 16 MB package on every ask (before) with
// the cached lookup (after).
func BenchmarkPackageDigest(b *testing.B) {
	dir := b.TempDir()
	require.NoError(b, os.WriteFile(filepath.Join(dir, "main.wasm"), make([]byte, 16<<20), 0o600))
	require.NoError(b, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0o600))
	b.Run("uncached", func(b *testing.B) {
		for range b.N {
			_, err := PackageSHA256(dir)
			require.NoError(b, err)
		}
	})
	b.Run("cached", func(b *testing.B) {
		c := newPackageDigests()
		for range b.N {
			_, err := c.get("ext", dir)
			require.NoError(b, err)
		}
	})
}

// Reload drops the cached digest; Disable does not touch files and keeps it.
func TestProvenanceDigestLifecycle(t *testing.T) {
	ctx := context.Background()
	e := newProvenanceEnv(t, "ext-prov")
	_, err := e.svc.InstallStorePackage(ctx, e.src, approveInstall)
	require.NoError(t, err)
	hashed := countDigests(e.svc)
	_, err = e.svc.Provenance(ctx, "ext-prov")
	require.NoError(t, err)
	require.Equal(t, 1, *hashed)

	require.NoError(t, e.svc.Disable(ctx, "ext-prov"))
	_, err = e.svc.Provenance(ctx, "ext-prov")
	require.NoError(t, err)
	assert.Equal(t, 1, *hashed, "Disable does not touch files")

	require.NoError(t, e.svc.Reload(ctx))
	_, err = e.svc.Provenance(ctx, "ext-prov")
	require.NoError(t, err)
	assert.Equal(t, 2, *hashed, "Reload invalidates")

	require.NoError(t, e.svc.Uninstall(ctx, "ext-prov", false, true))
	assert.Empty(t, e.svc.digests.entries, "Uninstall invalidates")
}
