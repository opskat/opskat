// Package extensiontest loads pkg/extension's fixture extensions for tests outside
// pkg/extension that need a real WASM guest on the other side of the host — the
// only way to observe describe(), check_policy, execute_tool and a database
// driver's session as the app sees them. testdata/fixture-ext exercises the host
// capabilities; testdata/fixture-dbdriver provides a database driver;
// testdata/fixture-settings declares extension settings; testdata/fixture-reviewer
// contributes a command reviewer. It is for tests only.
package extensiontest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/extension"
)

// FixtureName is the fixture extension's name: its manifest's, and the directory
// InstallFixture lays it out in.
const FixtureName = "fixture-ext"

// DBDriverFixtureName is the database driver fixture's name. It provides one
// driver, DBDriverFixtureDriver, over an in-memory database that logs in to
// whatever the session's reach path dials (see testdata/fixture-dbdriver). It is
// built for hostABI 1.1 and runs and dumps SQL files; DBDriverFixtureBuild builds
// it otherwise.
const DBDriverFixtureName = "fixture-dbdriver"

// DBDriverFixtureDriver is the id of the driver the database driver fixture provides.
const DBDriverFixtureDriver = "fixturedb"

// SettingsFixtureName is the settings fixture's name: an extension declaring
// settings (baseURL format:"endpoint", apiKey format:"password" injected as
// "Authorization: Bearer <apiKey>", model) and permissions.network.settingsEndpoint,
// with host-only tools call_service (GET args.url, answering status and body)
// and settings (what ctx.Settings reads). See testdata/fixture-settings.
const SettingsFixtureName = "fixture-settings"

// ReviewerFixtureName is the command reviewer fixture's name: an extension
// contributing a command reviewer (opskat.CommandReviewer, tool
// opskat.ReviewToolName; questions destructive, disruptive, remote_code) and
// settings (endpoint format:"endpoint", apiKey format:"password" injected as a
// bearer token) under permissions.network.settingsEndpoint. With no endpoint
// saved it judges by keyword — "fixture-error" errors "unavailable",
// "fixture-slow" runs past the timeout, "rm -rf" rejects destructive (0.9),
// "curl" with "| sh" rejects remote_code (0.8), anything else passes; with one
// saved it POSTs {assetType, command} to <endpoint>/review and answers the JSON
// reply. See testdata/fixture-reviewer.
const ReviewerFixtureName = "fixture-reviewer"

// BadKindsFixtureName is the registration-mistake fixture's name: an extension
// whose one tool is both HostOnly and PageOnly, so it never loads. See
// testdata/fixture-badkinds.
const BadKindsFixtureName = "fixture-badkinds"

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

// InstallSettingsFixture is InstallFixture for the settings fixture
// (<root>/fixture-settings).
func InstallSettingsFixture(t testing.TB, root string) string {
	t.Helper()
	return install(t, root, SettingsFixtureName)
}

// InstallReviewerFixture is InstallFixture for the command reviewer fixture
// (<root>/fixture-reviewer).
func InstallReviewerFixture(t testing.TB, root string) string {
	t.Helper()
	return install(t, root, ReviewerFixtureName)
}

// InstallBadKindsFixture is InstallFixture for the registration-mistake fixture
// (<root>/fixture-badkinds).
func InstallBadKindsFixture(t testing.TB, root string) string {
	t.Helper()
	return install(t, root, BadKindsFixtureName)
}

// DBDriverFixtureBuild is how the database driver fixture is built. The zero value
// is the fixture as it is: built for hostABI 1.1, declaring and serving running SQL
// files and dumping them.
type DBDriverFixtureBuild struct {
	// HostABI10 builds the driver the way an extension built for hostABI 1.0 is: it
	// neither declares nor serves running SQL files and dumping them, and its
	// manifest declares hostABI 1.0.
	HostABI10 bool
	// ManifestHostABI, when set, is the hostABI the manifest declares in place of
	// the build's own — for a manifest that does not match its code.
	ManifestHostABI string
}

// dbDriverFixtureABI10Tag is the build tag of DBDriverFixtureBuild.HostABI10
// (testdata/fixture-dbdriver/abi10.go).
const dbDriverFixtureABI10Tag = "fixture_hostabi10"

// InstallDBDriverFixtureBuild is InstallDBDriverFixture for a build of the database
// driver fixture other than its own.
func InstallDBDriverFixtureBuild(t testing.TB, root string, b DBDriverFixtureBuild) string {
	t.Helper()
	var tags []string
	hostABI := b.ManifestHostABI
	if b.HostABI10 {
		tags = append(tags, dbDriverFixtureABI10Tag)
		if hostABI == "" {
			hostABI = "1.0"
		}
	}
	return installBuild(t, root, DBDriverFixtureName, tags, hostABI)
}

// LoadDBDriverFixtureBuild builds the database driver fixture as b says and loads
// it through the real extension.Manager with contributions judging its describe()
// answer — the host's contribution points, for a test of what the host accepts.
// It returns the error loading failed with.
func LoadDBDriverFixtureBuild(t testing.TB, b DBDriverFixtureBuild, contributions *extension.ContributionRegistry) (*extension.Extension, error) {
	t.Helper()
	return loadExtensionWith(t, func(t testing.TB, root string) string { return InstallDBDriverFixtureBuild(t, root, b) }, contributions)
}

func install(t testing.TB, root, name string) string {
	t.Helper()
	return installBuild(t, root, name, nil, "")
}

// installBuild builds fixture name with the build tags and lays it out under root;
// a non-empty hostABI replaces the one its manifest declares.
func installBuild(t testing.TB, root, name string, tags []string, hostABI string) string {
	t.Helper()
	src := fixtureSrc(t, name)
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	args := []string{"build", "-buildmode=c-shared", "-o", filepath.Join(dir, "main.wasm")}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	build := exec.Command("go", append(args, ".")...) //nolint:gosec // fixed argv, only the temp output path and the fixture's own tags vary
	build.Dir = src
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture extension %s: %v\n%s", name, err, out)
	}
	manifest, err := os.ReadFile(filepath.Join(src, "manifest.json")) //nolint:gosec // the fixture's own manifest
	if err != nil {
		t.Fatal(err)
	}
	if hostABI != "" {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(manifest, &fields); err != nil {
			t.Fatalf("parse fixture manifest %s: %v", name, err)
		}
		if fields["hostABI"], err = json.Marshal(hostABI); err != nil {
			t.Fatal(err)
		}
		if manifest, err = json.Marshal(fields); err != nil {
			t.Fatal(err)
		}
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

// LoadBadKindsFixtureError builds the registration-mistake fixture and tries to
// load it through the real extension.Manager, returning the load error the app
// would show.
func LoadBadKindsFixtureError(t testing.TB) error {
	t.Helper()
	_, err := loadExtension(t, InstallBadKindsFixture)
	return err
}

func load(t testing.TB, install func(testing.TB, string) string) *extension.Extension {
	t.Helper()
	ext, err := loadExtension(t, install)
	if err != nil {
		t.Fatal(err)
	}
	return ext
}

func loadExtension(t testing.TB, install func(testing.TB, string) string) (*extension.Extension, error) {
	t.Helper()
	return loadExtensionWith(t, install, AcceptingContributions())
}

func loadExtensionWith(t testing.TB, install func(testing.TB, string) string, contributions *extension.ContributionRegistry) (*extension.Extension, error) {
	t.Helper()
	root := t.TempDir()
	dir := install(t, root)

	mgr := extension.NewManager(root, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, contributions, zap.NewNop())
	t.Cleanup(func() { mgr.Close(context.Background()) })
	m, err := mgr.LoadExtension(context.Background(), dir)
	if err != nil {
		return nil, err
	}
	return mgr.GetExtension(m.Name), nil
}
