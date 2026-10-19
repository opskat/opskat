package command

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
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
