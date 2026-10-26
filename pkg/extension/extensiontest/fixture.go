// Package extensiontest loads pkg/extension's fixture extensions for tests outside
// pkg/extension that need a real WASM guest on the other side of the host — the
// only way to observe describe(), check_policy, execute_tool and a database
// driver's session as the app sees them. testdata/fixture-ext exercises the host
// capabilities; testdata/fixture-dbdriver provides a database driver. It is for
// tests only.
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

// FixtureName is the fixture extension's name: its manifest's, and the directory
// InstallFixture lays it out in.
const FixtureName = "fixture-ext"

// DBDriverFixtureName is the database driver fixture's name. It provides one
// driver, DBDriverFixtureDriver, over an in-memory database that logs in to
// whatever the session's reach path dials (see testdata/fixture-dbdriver).
const DBDriverFixtureName = "fixture-dbdriver"

// DBDriverFixtureDriver is the id of the driver the database driver fixture provides.
const DBDriverFixtureDriver = "fixturedb"

// fixtureSrc is a fixture extension's source directory, found from this file so
// a test in any package can build it.
func fixtureSrc(t testing.TB, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("extensiontest: cannot locate the fixture extension source")
	}
	return filepath.Join(filepath.Dir(file), "..", "testdata", name)
}

// InstallFixture builds the fixture extension and lays it out under root exactly as
// an installed extension looks on disk (<root>/fixture-ext/{manifest.json,main.wasm}),
// so root can stand in for a data dir's extensions directory. It returns the
// extension's directory.
func InstallFixture(t testing.TB, root string) string {
	t.Helper()
	return install(t, root, FixtureName)
}

// InstallDBDriverFixture is InstallFixture for the database driver fixture
// (<root>/fixture-dbdriver).
func InstallDBDriverFixture(t testing.TB, root string) string {
	t.Helper()
	return install(t, root, DBDriverFixtureName)
}

func install(t testing.TB, root, name string) string {
	t.Helper()
	src := fixtureSrc(t, name)
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-buildmode=c-shared", "-o", filepath.Join(dir, "main.wasm"), ".") //nolint:gosec // fixed argv, only the temp output path varies
	build.Dir = src
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture extension %s: %v\n%s", name, err, out)
	}
	manifest, err := os.ReadFile(filepath.Join(src, "manifest.json")) //nolint:gosec // the fixture's own manifest
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o600); err != nil { //nolint:gosec // dir is this test's own temp dir
		t.Fatal(err)
	}
	return dir
}

// LoadFixture builds the fixture extension and loads it through the real
// extension.Manager, so its describe() reaches the manifest exactly as it does in
// the app. The manager is closed when the test ends.
func LoadFixture(t testing.TB) *extension.Extension {
	t.Helper()
	return load(t, InstallFixture)
}

// LoadDBDriverFixture is LoadFixture for the database driver fixture.
func LoadDBDriverFixture(t testing.TB) *extension.Extension {
	t.Helper()
	return load(t, InstallDBDriverFixture)
}

func load(t testing.TB, install func(testing.TB, string) string) *extension.Extension {
	t.Helper()
	root := t.TempDir()
	dir := install(t, root)

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
