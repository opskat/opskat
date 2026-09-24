package extreg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/extension"
)

// fixtureExtSrc is pkg/extension's test extension: its spin_short tool declares
// a 300ms timeout in describe() and busy-loops for as long as it is told.
const fixtureExtSrc = "../../pkg/extension/testdata/fixture-ext"

// loadFixtureExtension builds the fixture extension and loads it through the
// real extension.Manager, so describe() — the tools' declared timeouts included —
// reaches the manifest exactly as it does in the app.
func loadFixtureExtension(t *testing.T) *extension.Extension {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "fixture-ext")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	build := exec.Command("go", "build", "-buildmode=c-shared", "-o", filepath.Join(dir, "main.wasm"), fixtureExtSrc) //nolint:gosec // fixed argv, only the temp output path varies
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "build fixture extension: %s", out)
	manifest, err := os.ReadFile(filepath.Join(fixtureExtSrc, "manifest.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600)) //nolint:gosec // dir is this test's own temp dir

	mgr := extension.NewManager(root, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, zap.NewNop())
	t.Cleanup(func() { mgr.Close(context.Background()) })
	m, err := mgr.LoadExtension(context.Background(), dir)
	require.NoError(t, err)
	return mgr.GetExtension(m.Name)
}

// TestExecHonoursToolDeclaredTimeout drives an extension tool through the
// executor the unified exec registers for the asset type — the path AI exec and
// opsctl's delegated exec both run — and shows the tool's own declared timeout,
// not the host's 30s default, is what stops it.
func TestExecHonoursToolDeclaredTimeout(t *testing.T) {
	ext := loadFixtureExtension(t)
	require.NoError(t, Register(ext))
	t.Cleanup(func() { Unregister(ext.Name) })

	execFn, ok := permission.ExecutorFor("fixture")
	require.True(t, ok)
	asset := &asset_entity.Asset{ID: 1, Name: "fixture-1", Type: "fixture"}

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := execFn(context.Background(), asset, "spin_short --ms=20000", "")
		done <- err
	}()
	select {
	case err := <-done:
		elapsed := time.Since(start)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deadline exceeded")
		assert.Less(t, elapsed, 5*time.Second, "cut off by the tool's 300ms declaration, not the 30s default")
	case <-time.After(10 * time.Second):
		t.Fatal("exec of a tool declaring a 300ms timeout did not return within 10s")
	}
}
