// Package extensiontest loads pkg/extension's fixture extension (testdata/fixture-ext)
// for tests outside pkg/extension that need a real WASM guest on the other side of
// the host — the only way to observe describe(), check_policy and execute_tool as
// the app sees them. It is for tests only.
package extensiontest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/extension"
)

// fixtureSrc is the fixture extension's source directory, found from this file so
// a test in any package can build it.
func fixtureSrc(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("extensiontest: cannot locate the fixture extension source")
	}
	return filepath.Join(filepath.Dir(file), "..", "testdata", "fixture-ext")
}

// LoadFixture builds the fixture extension and loads it through the real
// extension.Manager, so its describe() reaches the manifest exactly as it does in
// the app. The manager is closed when the test ends.
func LoadFixture(t testing.TB) *extension.Extension {
	t.Helper()
	src := fixtureSrc(t)
	root := t.TempDir()
	dir := filepath.Join(root, "fixture-ext")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-buildmode=c-shared", "-o", filepath.Join(dir, "main.wasm"), ".") //nolint:gosec // fixed argv, only the temp output path varies
	build.Dir = src
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture extension: %v\n%s", err, out)
	}
	manifest, err := os.ReadFile(filepath.Join(src, "manifest.json")) //nolint:gosec // the fixture's own manifest
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600); err != nil { //nolint:gosec // dir is this test's own temp dir
		t.Fatal(err)
	}

	mgr := extension.NewManager(root, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, zap.NewNop())
	t.Cleanup(func() { mgr.Close(context.Background()) })
	m, err := mgr.LoadExtension(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return mgr.GetExtension(m.Name)
}
