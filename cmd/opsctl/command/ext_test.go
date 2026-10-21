package command

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/assettype"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/audit_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extension/extensiontest"
)

// --- fixtures ----------------------------------------------------------------

type stubStateRepo struct {
	states []*extension_state_entity.ExtensionState
}

func (r *stubStateRepo) Find(context.Context, string) (*extension_state_entity.ExtensionState, error) {
	return nil, errors.New("not found")
}
func (r *stubStateRepo) FindAll(context.Context) ([]*extension_state_entity.ExtensionState, error) {
	return r.states, nil
}
func (r *stubStateRepo) Create(context.Context, *extension_state_entity.ExtensionState) error {
	return nil
}
func (r *stubStateRepo) Update(context.Context, *extension_state_entity.ExtensionState) error {
	return nil
}
func (r *stubStateRepo) Delete(context.Context, string) error { return nil }

// minimalWASM is the smallest valid WASM module (magic + version header). opsctl
// never runs it — it only hashes it to look the extension up in the descriptor cache.
var minimalWASM = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// describeCacheStub replays what the desktop app cached when it last loaded the
// extension. opsctl has no WASM runtime, so this cache is the only way it learns an
// extension's asset types and tools.
type describeCacheStub struct{ payloads map[string]string }

func (s *describeCacheStub) LoadDescriptor(name string) (string, []byte, error) {
	payload, ok := s.payloads[name]
	if !ok {
		return "", nil, nil
	}
	return extension.WasmHash(minimalWASM), []byte(payload), nil
}

func (s *describeCacheStub) StoreDescriptor(string, string, []byte) error { return nil }

func (s *describeCacheStub) DeleteDescriptor(name string) error {
	delete(s.payloads, name)
	return nil
}

func installDescribeStub(t *testing.T) *describeCacheStub {
	t.Helper()
	stub := &describeCacheStub{payloads: map[string]string{}}
	extension.SetDescribeCache(stub)
	t.Cleanup(func() { extension.SetDescribeCache(nil) })
	return stub
}

// writeExtManifest writes the manifest (the security contract) plus the descriptor
// the app would have cached for it (asset types, tools, policy face).
func writeExtManifest(t *testing.T, stub *describeCacheStub, dir, name, assetType string) {
	t.Helper()
	extDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(extDir, 0o755))
	manifest := map[string]any{
		"name":    name,
		"version": "1.0.0",
		"hostABI": extension.HostABIVersion,
		"backend": map[string]any{"runtime": "wasm", "binary": "main.wasm"},
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "main.wasm"), minimalWASM, 0o600))

	descriptor, err := json.Marshal(map[string]any{
		"i18n": map[string]any{"displayName": name + " display", "description": name + " description"},
		"assetTypes": []map[string]any{{
			"type": assetType,
			"i18n": map[string]any{"name": assetType},
			"configSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"endpoint": map[string]any{"type": "string"}},
			},
		}},
		"tools": []map[string]any{{
			"name":         "list_objects",
			"policyAction": "list",
			"parameters":   map[string]any{"type": "object", "properties": map[string]any{}},
		}},
		"policies": map[string]any{"type": name},
	})
	require.NoError(t, err)
	stub.payloads[name] = string(descriptor)
}

// withExtensionDir points the scanner at a temp extensions dir and installs the given
// persisted enabled/disabled state.
func withExtensionDir(t *testing.T, states ...*extension_state_entity.ExtensionState) string {
	t.Helper()
	dir := t.TempDir()
	orig := extensionsDirFn
	extensionsDirFn = func() string { return dir }
	origRepo := extension_state_repo.ExtensionState()
	extension_state_repo.RegisterExtensionState(&stubStateRepo{states: states})
	t.Cleanup(func() {
		extensionsDirFn = orig
		extension_state_repo.RegisterExtensionState(origRepo)
	})
	return dir
}

// --- scanning ----------------------------------------------------------------

func TestScanInstalledExtensionsUsesTheRealManifestParser(t *testing.T) {
	dir := withExtensionDir(t)
	stub := installDescribeStub(t)
	writeExtManifest(t, stub, dir, "acme", "acme-store")
	// A directory whose manifest the app itself would refuse (here: an unsupported
	// host ABI) must not be listed either.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bogus"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bogus", "manifest.json"),
		[]byte(`{"name":"bogus","version":"1.0.0","hostABI":"9.0"}`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "no-manifest"), 0o755))

	got, err := scanInstalledExtensions()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "acme", got[0].Name)
	assert.Equal(t, []string{"acme-store"}, got[0].AssetTypes)
	assert.Equal(t, []string{"list_objects"}, got[0].Tools)
	assert.True(t, got[0].Enabled)
}

func TestScanInstalledExtensionsReportsDisabledState(t *testing.T) {
	dir := withExtensionDir(t, &extension_state_entity.ExtensionState{Name: "acme", Enabled: false})
	writeExtManifest(t, installDescribeStub(t), dir, "acme", "acme-store")

	got, err := scanInstalledExtensions()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Enabled, "a disabled extension must be listed as disabled, not as enabled")

	// A disabled extension's asset types are not registered in the running app either,
	// so opsctl must not register them locally.
	registerExtensionAssetTypes()
	_, registered := assettype.Get("acme-store")
	assert.False(t, registered)
}

// --- local registration ------------------------------------------------------

// forgetExtension undoes what registerExtensionAssetTypes wrote, so a test does not
// leak an asset type into the next one (registration refuses duplicates).
func forgetExtension(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() { extreg.Unregister(name) })
}

func TestRegisterExtensionAssetTypesMakesTheTypeUsableWithoutWASM(t *testing.T) {
	dir := withExtensionDir(t)
	writeExtManifest(t, installDescribeStub(t), dir, "acme", "acme-store")
	forgetExtension(t, "acme")

	registerExtensionAssetTypes()

	// create/update/list resolve the type through this registry: without it
	// `opsctl create asset --type acme-store` reports an unsupported asset type while
	// the desktop app on the same machine accepts it.
	handler, ok := assettype.Get("acme-store")
	require.True(t, ok)
	assert.Equal(t, "acme-store", handler.Type())
	owner, ok := assettype.ExtensionOwnerOf("acme-store")
	require.True(t, ok)
	assert.Equal(t, "acme", owner, "exec activates the owning extension by this answer")

	// `opsctl help acme-store` renders the tool/parameter table describe() reported.
	help, ok := permission.HelpFor("acme-store")
	require.True(t, ok)
	assert.Contains(t, help, "list_objects")

	// `opsctl policy show/allow/deny` need a permanent-rule landing for the type.
	assert.True(t, permission.TypeRulesSupported("acme-store"))

	// …but nothing executes until exec activates the extension: this registration
	// compiles no WASM.
	assert.NotContains(t, permission.RegisteredExecTypes(), "acme-store")
}

func TestRegisterExtensionAssetTypesReportsAFailedDescribeInsteadOfRegisteringNothing(t *testing.T) {
	dir := withExtensionDir(t)
	stub := installDescribeStub(t)
	writeExtManifest(t, stub, dir, "acme", "acme-store")
	// This machine has never loaded the extension, and loading it here fails.
	require.NoError(t, stub.DeleteDescriptor("acme"))
	forgetExtension(t, "acme")

	var described []string
	orig := describeExtensionFn
	describeExtensionFn = func(_ context.Context, extDir string) (*extension.ManifestInfo, error) {
		described = append(described, filepath.Base(extDir))
		return nil, errors.New("wasm does not compile")
	}
	t.Cleanup(func() { describeExtensionFn = orig })

	stderr := captureStderr(t, registerExtensionAssetTypes)

	assert.Equal(t, []string{"acme"}, described)
	assert.Contains(t, stderr, "acme")
	assert.Contains(t, stderr, "wasm does not compile")
	_, registered := assettype.Get("acme-store")
	assert.False(t, registered)
}

// An extension that cannot be described here (no cache, main.wasm gone) leaves its
// asset type unclaimed; the final error names the extension and the reason instead of
// only saying the type has no exec support.
func TestCmdExec_ExtensionThatFailedToDescribeIsNamedInTheError(t *testing.T) {
	env := setupLocalExtExec(t)
	require.NoError(t, os.Remove(filepath.Join(env.extDir, "main.wasm")))
	_ = captureStderr(t, func() { bootWithoutDescribeCache(t, env) })

	code, _, stderr := env.exec(t, "fx-1", "--", "echo", "--msg", "hi")

	assert.Equal(t, 1, code)
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	last := lines[len(lines)-1]
	assert.Contains(t, last, "has no exec support yet")
	assert.Contains(t, last, `"`+extensiontest.FixtureName+`"`)
	assert.Contains(t, last, "main.wasm")
}

// With no describe failure the message stays exactly the AI exec tool's.
func TestUnsupportedExecTypeError_UnchangedWhenNoExtensionFailed(t *testing.T) {
	dir := withExtensionDir(t)
	writeExtManifest(t, installDescribeStub(t), dir, "acme", "acme-store")
	forgetExtension(t, "acme")
	registerExtensionAssetTypes()

	err := unsupportedExecTypeError(&asset_entity.Asset{Name: "a", Type: "rdp"})

	assert.NotContains(t, err.Error(), "failed to describe")
}

// --- ext dev -----------------------------------------------------------------

// stubDevInstall replaces the socket round trip so the tests observe what opsctl
// decided to send, not whether a desktop app happens to be running.
func stubDevInstall(t *testing.T, fn func(sourceDir string) (string, string, error)) *string {
	t.Helper()
	var got string
	orig := devInstallFn
	devInstallFn = func(sourceDir string) (string, string, error) {
		got = sourceDir
		return fn(sourceDir)
	}
	t.Cleanup(func() { devInstallFn = orig })
	return &got
}

func TestExtDevSendsTheResolvedAbsoluteDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0o600))
	sent := stubDevInstall(t, func(string) (string, string, error) { return "acme", "1.0.0", nil })

	require.Equal(t, 0, cmdExtDev([]string{dir}))
	// The desktop process resolves paths against its own working directory, so a
	// relative path typed here would land somewhere else entirely.
	assert.True(t, filepath.IsAbs(*sent))
	assert.Equal(t, dir, *sent)
}

func TestExtDevRejectsADirectoryWithoutAManifest(t *testing.T) {
	sent := stubDevInstall(t, func(string) (string, string, error) { return "acme", "1.0.0", nil })

	assert.Equal(t, 1, cmdExtDev([]string{t.TempDir()}))
	assert.Empty(t, *sent)
}

func TestExtDevReportsAFailedInstall(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0o600))
	stubDevInstall(t, func(string) (string, string, error) {
		return "", "", errors.New("cannot connect to approval socket")
	})

	assert.Equal(t, 1, cmdExtDev([]string{dir}))
}

func TestExtDevNeedsADirectory(t *testing.T) {
	sent := stubDevInstall(t, func(string) (string, string, error) { return "acme", "1.0.0", nil })

	assert.Equal(t, 1, cmdExtDev(nil))
	assert.Empty(t, *sent)
}

func TestExtRoutesDevToTheDevSubcommand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0o600))
	sent := stubDevInstall(t, func(string) (string, string, error) { return "acme", "1.0.0", nil })

	require.Equal(t, 0, cmdExt(context.Background(), []string{"dev", dir}))
	assert.Equal(t, dir, *sent)
}

// --- file-readable parameters -------------------------------------------------

// registerBulkExtension makes opsctl learn, from the cached describe(), an
// extension whose `bulk` tool has a file-readable `body` parameter.
func registerBulkExtension(t *testing.T) {
	t.Helper()
	dir := withExtensionDir(t)
	stub := installDescribeStub(t)
	writeExtManifest(t, stub, dir, "acme", "acme-store")
	var d map[string]any
	require.NoError(t, json.Unmarshal([]byte(stub.payloads["acme"]), &d))
	d["tools"] = []map[string]any{{
		"name":         "bulk",
		"policyAction": "write",
		"fileParams":   []string{"body"},
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"body":  map[string]any{"type": "string"},
			"index": map[string]any{"type": "string"},
		}},
	}}
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	stub.payloads["acme"] = string(raw)
	forgetExtension(t, "acme")
	registerExtensionAssetTypes()
}

// prepareExtensionExec reads `--<flag>-file` into the inline `--<flag> <content>`
// form after activating the extension, so canonicalization, policy, approval and the
// tool only ever see the inline form.
func TestPrepareExtensionExecExpandsFileFlags(t *testing.T) {
	registerBulkExtension(t)
	orig := activateExtensionFn
	activateExtensionFn = func(context.Context, string) error { return nil }
	t.Cleanup(func() { activateExtensionFn = orig })
	asset := &asset_entity.Asset{ID: 7, Name: "s", Type: "acme-store"}
	path := filepath.Join(t.TempDir(), "body.ndjson")
	require.NoError(t, os.WriteFile(path, []byte("line1\nline2\n"), 0o600))
	ctx := context.Background()

	t.Run("file path", func(t *testing.T) {
		got, err := prepareExtensionExec(ctx, asset, "acme", "bulk --index=logs --body-file "+path)
		require.NoError(t, err)
		assert.NotContains(t, got, "-file", "everything after opsctl only ever sees the inline form")
		assert.Contains(t, got, "--body='line1\nline2\n'")
	})

	t.Run("stdin", func(t *testing.T) {
		orig := execStdin
		execStdin = strings.NewReader("line1\nline2\n")
		t.Cleanup(func() { execStdin = orig })
		got, err := prepareExtensionExec(ctx, asset, "acme", "bulk --index=logs --body-file -")
		require.NoError(t, err)
		assert.Contains(t, got, "--body='line1\nline2\n'")
	})

	t.Run("a refused file is an error", func(t *testing.T) {
		_, err := prepareExtensionExec(ctx, asset, "acme", "bulk --body=x --body-file "+path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--body")

		_, err = prepareExtensionExec(ctx, asset, "acme", "bulk --body-file "+path+".missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing")
	})
}

// --- no describe() cache on this machine ---------------------------------------

// bootWithoutDescribeCache puts the fixture environment in the state of a machine whose
// desktop app never loaded the extension: installed, enabled, but no extension_describe
// row, then runs opsctl's boot registration.
func bootWithoutDescribeCache(t *testing.T, env *localExtEnv) {
	t.Helper()
	extreg.Unregister(extensiontest.FixtureName)
	require.NoError(t, repoDescribeCache{}.DeleteDescriptor(extensiontest.FixtureName))
	_, payload, err := repoDescribeCache{}.LoadDescriptor(extensiontest.FixtureName)
	require.NoError(t, err)
	require.Empty(t, payload, "precondition: the cache row is gone")
	registerExtensionAssetTypes()
}

// opsctl describes the extension itself when the cache has no answer, instead of
// leaving its asset types unregistered and telling the user to open the desktop app.
func TestRegisterExtensionAssetTypes_FillsAMissingDescribeCacheItself(t *testing.T) {
	env := setupLocalExtExec(t)
	bootWithoutDescribeCache(t, env)

	owner, ok := assettype.ExtensionOwnerOf("fixture")
	require.True(t, ok, "the type must be registered from opsctl's own describe()")
	assert.Equal(t, extensiontest.FixtureName, owner)
	_, payload, err := repoDescribeCache{}.LoadDescriptor(extensiontest.FixtureName)
	require.NoError(t, err)
	assert.NotEmpty(t, payload, "the answer is written to the cache for the next command")
}

// unwritableDescribeCache is the data dir's describe() cache with writes failing
// (a locked or read-only database).
type unwritableDescribeCache struct{ repoDescribeCache }

func (unwritableDescribeCache) StoreDescriptor(string, string, []byte) error {
	return errors.New("database is locked")
}

// The answer opsctl just got from describe() registers the extension even when it
// cannot be written to the cache: the command that described it still sees its types.
func TestRegisterExtensionAssetTypes_UsesItsOwnDescribeWhenTheCacheCannotBeWritten(t *testing.T) {
	env := setupLocalExtExec(t)
	extension.SetDescribeCache(unwritableDescribeCache{})
	t.Cleanup(func() { extension.SetDescribeCache(repoDescribeCache{}) })

	stderr := captureStderr(t, func() { bootWithoutDescribeCache(t, env) })

	owner, ok := assettype.ExtensionOwnerOf("fixture")
	require.True(t, ok, "the type must be registered from opsctl's own describe(): %s", stderr)
	assert.Equal(t, extensiontest.FixtureName, owner)
}

func TestCmdExec_ExtensionAssetRunsWithNoDescribeCache(t *testing.T) {
	env := setupLocalExtExec(t)
	bootWithoutDescribeCache(t, env)

	code, stdout, stderr := env.exec(t, "fx-1", "--", "echo", "--msg", "hi")

	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, `{"args":{"msg":"hi"},"tool":"echo"}`, stdout)
	assert.NotContains(t, stderr, "desktop")
}

func TestCmdCreateAsset_ExtensionTypeWorksWithNoDescribeCache(t *testing.T) {
	env := setupLocalExtExec(t)
	bootWithoutDescribeCache(t, env)

	var code int
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			code = cmdCreate(context.Background(), buildHandlerMap(),
				[]string{"asset", "--type", "fixture", "--name", "fx-2", "--config", `{"endpoint":"http://127.0.0.1:1"}`}, "sess-ext")
		})
	})

	// Nothing can approve the write here (no terminal, no desktop), so the command stops
	// at the approval step: it got past the type lookup, which is what the cache fixes.
	assert.NotContains(t, stderr, "unsupported asset type")
	assert.Equal(t, 3, code, "stderr: %s", stderr)
	assert.Contains(t, stderr, "NEEDS TTY")
}

func TestCmdPolicyShow_ExtensionAssetWorksWithNoDescribeCache(t *testing.T) {
	env := setupLocalExtExec(t)
	bootWithoutDescribeCache(t, env)

	var code int
	stderr := captureStderr(t, func() {
		captureStdout(t, func() {
			code = cmdPolicyShow(context.Background(), []string{"fx-1"}, "sess-ext")
		})
	})

	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.NotContains(t, stderr, "no permanent rule support")
}

// A disabled extension is neither described nor loaded: it stays out of the registry
// and its describe() stays unanswered.
func TestRegisterExtensionAssetTypes_DisabledExtensionIsNotDescribed(t *testing.T) {
	env := setupLocalExtExec(t, &extension_state_entity.ExtensionState{Name: extensiontest.FixtureName, Enabled: false})
	bootWithoutDescribeCache(t, env)

	_, ok := assettype.Get("fixture")
	assert.False(t, ok)
	_, payload, err := repoDescribeCache{}.LoadDescriptor(extensiontest.FixtureName)
	require.NoError(t, err)
	assert.Empty(t, payload, "a disabled extension is not loaded, so nothing is cached")
}

// --- extension-provided database drivers ---------------------------------------

// driverExtEnv is the fixture environment plus the database-driver fixture installed
// and described on this machine, a TCP "database" it can reach, and a database asset
// whose driver the extension provides. No desktop app, no terminal.
type driverExtEnv struct {
	*localExtEnv
	asset       *asset_entity.Asset
	db          *driverFixtureDB
	activations []string
	approvals   []approval.ApprovalRequest
}

// driverFixtureDB answers the fixture driver's wire protocol on a real TCP port.
type driverFixtureDB struct {
	addr   string
	mu     sync.Mutex
	logins int
	lines  []string
}

func newDriverFixtureDB(t *testing.T) *driverFixtureDB {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	d := &driverFixtureDB{addr: ln.Addr().String()}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					d.mu.Lock()
					if strings.HasPrefix(line, "LOGIN ") {
						d.logins++
					} else {
						d.lines = append(d.lines, line)
					}
					d.mu.Unlock()
					if _, err := c.Write([]byte("OK\n")); err != nil {
						return
					}
				}
			}()
		}
	}()
	return d
}

func (d *driverFixtureDB) loginCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.logins
}

func (d *driverFixtureDB) statements() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.lines...)
}

// setupDriverExt installs the driver fixture next to the asset-type fixture, as a
// machine whose desktop app described it once, then runs opsctl's boot registration.
func setupDriverExt(t *testing.T, states ...*extension_state_entity.ExtensionState) *driverExtEnv {
	t.Helper()
	base := setupLocalExtExec(t)
	ctx := context.Background()
	root := extensionsDirFn()
	dir := extensiontest.InstallDBDriverFixture(t, root)
	mgr := extension.NewManager(root, func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, zap.NewNop())
	_, err := mgr.LoadExtension(ctx, dir)
	require.NoError(t, err)
	mgr.Close(ctx)
	for _, s := range states {
		enabled := s.Enabled
		require.NoError(t, extension_state_repo.ExtensionState().Create(ctx, s))
		// The column defaults to enabled; a disable is an update of the existing row.
		s.Enabled = enabled
		require.NoError(t, extension_state_repo.ExtensionState().Update(ctx, s))
	}
	extreg.Unregister(extensiontest.FixtureName)
	t.Cleanup(func() {
		releaseExtensions(ctx)
		extreg.Unregister(extensiontest.DBDriverFixtureName)
	})
	registerExtensionAssetTypes()

	env := &driverExtEnv{localExtEnv: base, db: newDriverFixtureDB(t)}
	host, port, err := net.SplitHostPort(env.db.addr)
	require.NoError(t, err)
	portNum, err := strconv.Atoi(port)
	require.NoError(t, err)
	env.asset = &asset_entity.Asset{Name: "fx-db", Type: asset_entity.AssetTypeDatabase, Status: asset_entity.StatusActive, Createtime: 1}
	encrypted, err := credential_svc.Default().Encrypt("s3cret")
	require.NoError(t, err)
	require.NoError(t, env.asset.SetDatabaseConfig(&asset_entity.DatabaseConfig{
		Driver: "fixturedb", Host: host, Port: portNum, Username: "alice", Password: encrypted, Database: "app",
	}))
	require.NoError(t, asset_repo.Asset().Create(ctx, env.asset))

	swapGlobal(t, func() func(context.Context, string) error { return activateExtensionFn },
		func(f func(context.Context, string) error) { activateExtensionFn = f },
		func(ctx context.Context, name string) error {
			env.activations = append(env.activations, name)
			return activateExtension(ctx, name)
		})
	swapGlobal(t, func() func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) { return execApprovalFn },
		func(f func(context.Context, approval.ApprovalRequest) (ApprovalResult, error)) { execApprovalFn = f },
		func(_ context.Context, req approval.ApprovalRequest) (ApprovalResult, error) {
			env.approvals = append(env.approvals, req)
			return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-ext"}, nil
		})
	return env
}

// A driver-only extension is not "a missing describe cache": the boot registration
// knows its driver from the cached answer, so opsctl sees it with no WASM compiled.
func TestRegisterExtensionAssetTypes_DriverOnlyExtensionRegistersItsDriverFromTheCache(t *testing.T) {
	setupDriverExt(t)
	extreg.Unregister(extensiontest.DBDriverFixtureName)
	var described []string
	swapGlobal(t, func() func(context.Context, string) (*extension.ManifestInfo, error) { return describeExtensionFn },
		func(f func(context.Context, string) (*extension.ManifestInfo, error)) { describeExtensionFn = f },
		func(ctx context.Context, dir string) (*extension.ManifestInfo, error) {
			described = append(described, filepath.Base(dir))
			return describeExtension(ctx, dir)
		})

	registerExtensionAssetTypes()

	assert.NotContains(t, described, extensiontest.DBDriverFixtureName, "the cached answer is enough: no WASM is compiled")

	owner, ok := extreg.DriverOwnerOf("fixturedb")
	require.True(t, ok, "the driver must be registered from the cached describe() answer")
	assert.Equal(t, extensiontest.DBDriverFixtureName, owner)
	d, ok := dbregistry.ByName("fixturedb")
	require.True(t, ok)
	assert.Equal(t, "FixtureDB", d.Descriptor().Name)
}

func TestCmdCreateAsset_ExtensionDriverIsValidatedByTheDriversOwnRules(t *testing.T) {
	setupDriverExt(t)
	create := func(args ...string) (int, string) {
		var code int
		stderr := captureStderr(t, func() {
			captureStdout(t, func() {
				code = cmdCreate(context.Background(), buildHandlerMap(),
					append([]string{"asset", "--type", "database", "--driver", "fixturedb", "--name", "fx-db-2"}, args...), "sess-ext")
			})
		})
		return code, stderr
	}

	// The driver's own config rules run: a network driver needs a host.
	code, stderr := create("--port", "7357", "--username", "alice")
	assert.Equal(t, 1, code, "stderr: %s", stderr)
	assert.Contains(t, stderr, "host")

	// A valid config gets past validation to the approval step, which nothing here can answer.
	code, stderr = create("--host", "db.internal", "--port", "7357", "--username", "alice")
	assert.Equal(t, 3, code, "stderr: %s", stderr)
	assert.Contains(t, stderr, "NEEDS TTY")
}

// With no desktop app on the data dir, `opsctl exec` on a database asset whose driver
// an extension provides activates that extension in this process and runs the SQL
// through its session; approval and audit are the database flow's own.
func TestCmdExec_ExtensionDriverAssetRunsSQLInOpsctlWithoutDesktop(t *testing.T) {
	env := setupDriverExt(t)

	code, stdout, stderr := env.exec(t, "fx-db", "--", "SELECT * FROM [app].[users]")

	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "alice")
	assert.Equal(t, []string{extensiontest.DBDriverFixtureName}, env.activations)
	assert.Equal(t, 1, env.db.loginCount(), "the extension's session logged in to the database once")

	require.Len(t, env.approvals, 1, "the existing database approval flow asked once")
	assert.Equal(t, "sql", env.approvals[0].Type)
	assert.Equal(t, env.asset.ID, env.approvals[0].AssetID)
	assert.Equal(t, "SELECT * FROM [app].[users]", env.approvals[0].Command)

	var rows []audit_entity.AuditLog
	require.NoError(t, env.gdb.Order("id").Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, "opsctl", rows[0].Source)
	assert.Equal(t, env.asset.ID, rows[0].AssetID)
	assert.Equal(t, "SELECT * FROM [app].[users]", rows[0].Command)
	assert.Equal(t, 1, rows[0].Success)
}

// An extension disabled in the desktop app is unavailable to opsctl the same way: the
// error names the driver, no extension is loaded and nothing is asked or run.
func TestCmdExec_ExtensionDriverDisabledInDesktopIsDriverUnavailable(t *testing.T) {
	env := setupDriverExt(t, &extension_state_entity.ExtensionState{Name: extensiontest.DBDriverFixtureName, Enabled: false})

	code, stdout, stderr := env.exec(t, "fx-db", "--", "SELECT * FROM [app].[users]")

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "driver unavailable")
	assert.Contains(t, stderr, "fixturedb")
	assert.Contains(t, stderr, "disabled")
	assert.Zero(t, env.db.loginCount())
	assert.Empty(t, env.approvals, "an unavailable driver is refused before any approval is asked")
}

// Built-in drivers never trigger an extension activation.
func TestCmdExec_BuiltinDriverAssetActivatesNoExtension(t *testing.T) {
	env := setupDriverExt(t)
	builtin := &asset_entity.Asset{Name: "sqlite-db", Type: asset_entity.AssetTypeDatabase, Status: asset_entity.StatusActive, Createtime: 1}
	require.NoError(t, builtin.SetDatabaseConfig(&asset_entity.DatabaseConfig{Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "x.db")}))
	require.NoError(t, asset_repo.Asset().Create(context.Background(), builtin))

	env.exec(t, "sqlite-db", "--", "SELECT 1")

	assert.Empty(t, env.activations)
}

// Batch items on extension-driver assets activate the providing extension once for
// the whole batch and each run through its session.
func TestCmdBatch_ExtensionDriverItemsActivateTheExtensionOnce(t *testing.T) {
	env := setupDriverExt(t)
	swapGlobal(t, func() func([]approval.BatchItem, string) (ApprovalResult, error) { return requireBatchApprovalFn },
		func(f func([]approval.BatchItem, string) (ApprovalResult, error)) { requireBatchApprovalFn = f },
		func(_ []approval.BatchItem, session string) (ApprovalResult, error) {
			return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: session}, nil
		})

	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = cmdBatch(context.Background(), buildHandlerMap(),
				[]string{"sql:fx-db:SELECT 1", "sql:fx-db:SELECT 1"}, "sess-batch")
		})
	})

	require.Equal(t, 0, code, "stdout: %s\nstderr: %s", stdout, stderr)
	assert.Equal(t, []string{extensiontest.DBDriverFixtureName}, env.activations, "one activation for the whole batch")
	assert.Equal(t, 2, strings.Count(stdout, `\"columns\"`), "both items returned a result: %s", stdout)
	assert.GreaterOrEqual(t, len(env.db.statements()), 2, "both items reached the database through the extension")
}

// --- driver fields through an extension driver, managed from the command line ------

// withDriverFields rewrites the describe() answer the desktop app cached for the driver
// fixture so its driver declares fields of its own (the fixture source declares none),
// then redoes opsctl's boot registration from that cache.
func withDriverFields(t *testing.T) {
	t.Helper()
	hash, payload, err := repoDescribeCache{}.LoadDescriptor(extensiontest.DBDriverFixtureName)
	require.NoError(t, err)
	var desc map[string]any
	require.NoError(t, json.Unmarshal(payload, &desc))
	drivers := desc["databaseDrivers"].([]any)
	drivers[0].(map[string]any)["driverFields"] = []map[string]any{
		{"key": "service_name", "type": "text", "tab": "connection", "required": true},
		{"key": "wallet_secret", "type": "password", "tab": "connection"},
	}
	patched, err := json.Marshal(desc)
	require.NoError(t, err)
	require.NoError(t, repoDescribeCache{}.StoreDescriptor(extensiontest.DBDriverFixtureName, hash, patched))
	extreg.Unregister(extensiontest.DBDriverFixtureName)
	registerExtensionAssetTypes()
	d, ok := dbregistry.ByName("fixturedb")
	require.True(t, ok)
	require.Len(t, d.Descriptor().DriverFields, 2, "the cached driver fields reach the describe-only registration")
}

func allowAssetWrites(t *testing.T) {
	t.Helper()
	allow := func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return ApprovalResult{Decision: aictx.Allow, DecisionSource: aictx.SourceUserAllow, SessionID: "sess-ext"}, nil
	}
	swapGlobal(t, func() func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return requireCreateApproval
	},
		func(f func(context.Context, approval.ApprovalRequest) (ApprovalResult, error)) {
			requireCreateApproval = f
		}, allow)
	swapGlobal(t, func() func(context.Context, approval.ApprovalRequest) (ApprovalResult, error) {
		return requireUpdateApproval
	},
		func(f func(context.Context, approval.ApprovalRequest) (ApprovalResult, error)) {
			requireUpdateApproval = f
		}, allow)
}

func runCmd(t *testing.T, fn func() int) (code int, stdout, stderr string) {
	t.Helper()
	stderr = captureStderr(t, func() { stdout = captureStdout(t, func() { code = fn() }) })
	return code, stdout, stderr
}

func TestExtensionDriverFields_ManagedFromTheCommandLine(t *testing.T) {
	setupDriverExt(t)
	withDriverFields(t)
	allowAssetWrites(t)
	ctx := context.Background()
	handlers := buildHandlerMap()
	create := func(options string) (int, string, string) {
		cfg := `{"driver":"fixturedb","host":"db.internal","port":7357,"username":"alice","driver_options":` + options + `}`
		return runCmd(t, func() int {
			return cmdCreate(ctx, handlers, []string{"asset", "--type", "database", "--name", "fx-opts", "--config", cfg}, "sess-ext")
		})
	}

	// Validation by the driver's declarations.
	code, _, stderr := create(`{"service_name":"orcl","bogus":1}`)
	assert.Equal(t, 1, code, "stderr: %s", stderr)
	assert.Contains(t, stderr, "bogus")
	code, _, stderr = create(`{}`)
	assert.Equal(t, 1, code, "stderr: %s", stderr)
	assert.Contains(t, stderr, "service_name")

	// Valid, with a password-type option: plaintext warning, created.
	code, stdout, stderr := create(`{"service_name":"orcl","wallet_secret":"top-secret-value"}`)
	require.Equal(t, 0, code, "stderr: %s\nstdout: %s", stderr, stdout)
	assert.Contains(t, stderr, "shell history")
	assert.NotContains(t, stderr+stdout, "top-secret-value")

	// Update changes a driver option.
	code, _, stderr = runCmd(t, func() int {
		return cmdUpdate(ctx, handlers, []string{"asset", "fx-opts", "--config", `{"driver_options":{"service_name":"orcl2"}}`}, "sess-ext")
	})
	require.Equal(t, 0, code, "stderr: %s", stderr)

	// Update without a driver in --config: the stored asset's driver identifies the password option.
	code, stdout, stderr = runCmd(t, func() int {
		return cmdUpdate(ctx, handlers, []string{"asset", "fx-opts", "--config", `{"driver_options":{"wallet_secret":"update-top-secret"}}`}, "sess-ext")
	})
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stderr, "shell history")
	assert.NotContains(t, stderr+stdout, "update-top-secret")

	// The stored config carries the updated option and the password option encrypted.
	found, err := asset_repo.Asset().FindByName(ctx, "fx-opts")
	require.NoError(t, err)
	require.Len(t, found, 1)
	cfg, err := found[0].GetDatabaseConfig()
	require.NoError(t, err)
	assert.Equal(t, "orcl2", cfg.DriverOptions["service_name"])
	assert.NotEmpty(t, cfg.DriverOptions["wallet_secret"])
	assert.NotEqual(t, "top-secret-value", cfg.DriverOptions["wallet_secret"])

	// Get: the plain option is shown, the password one only as set.
	code, stdout, stderr = runCmd(t, func() int { return cmdGet(ctx, handlers, []string{"asset", "fx-opts"}) })
	require.Equal(t, 0, code, "stderr: %s", stderr)
	var view struct {
		DriverOptions map[string]any `json:"driver_options"`
		SecretsSet    []string       `json:"driver_options_secrets_set"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &view), stdout)
	assert.Equal(t, map[string]any{"service_name": "orcl2"}, view.DriverOptions)
	assert.Equal(t, []string{"wallet_secret"}, view.SecretsSet)
	assert.NotContains(t, stdout+stderr, "top-secret-value")
	assert.NotContains(t, stdout, cfg.DriverOptions["wallet_secret"])

	// List shows the asset with its extension driver.
	code, stdout, stderr = runCmd(t, func() int { return cmdList(ctx, handlers, []string{"assets", "--type", "database"}) })
	require.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "fx-opts")
	assert.Contains(t, stdout, "fixturedb")
}
