package command

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
	"github.com/opskat/opskat/pkg/extension"
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
		"policies": map[string]any{"type": "ext:" + name},
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
	// A directory whose manifest the app itself would refuse (here: built against the
	// retired 1.x host ABI) must not be listed either: opsctl used to hand-roll a laxer
	// parser and list it anyway.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bogus"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bogus", "manifest.json"),
		[]byte(`{"name":"bogus","version":"1.0.0","hostABI":"1.0"}`), 0o600))
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
	// so exec must not try to route to it.
	assert.Empty(t, extensionAssetTypeOwners())
}

func TestExtensionAssetTypeOwnersMapsEnabledTypes(t *testing.T) {
	dir := withExtensionDir(t)
	writeExtManifest(t, installDescribeStub(t), dir, "acme", "acme-store")

	assert.Equal(t, map[string]string{"acme-store": "acme"}, extensionAssetTypeOwners())
}

// --- delegation --------------------------------------------------------------

func TestExecViaDesktopFailsClosedWhenDesktopIsOffline(t *testing.T) {
	orig := delegateExtExecFn
	delegateExtExecFn = func(int64, string, string, string) (string, error) {
		return "", errors.New("cannot connect to approval socket")
	}
	t.Cleanup(func() { delegateExtExecFn = orig })

	asset := &asset_entity.Asset{ID: 1, Name: "my-bucket", Type: "acme-store"}
	assert.Equal(t, 1, execViaDesktop(asset, "acme", "list_objects --bucket=logs", "sess"))
}

func TestExecViaDesktopPassesTheCommandThroughVerbatim(t *testing.T) {
	var gotID int64
	var gotCommand string
	orig := delegateExtExecFn
	delegateExtExecFn = func(assetID int64, _, command, _ string) (string, error) {
		gotID, gotCommand = assetID, command
		return `{"objects":1}`, nil
	}
	t.Cleanup(func() { delegateExtExecFn = orig })

	asset := &asset_entity.Asset{ID: 7, Name: "my-bucket", Type: "acme-store"}
	require.Equal(t, 0, execViaDesktop(asset, "acme", "list_objects --bucket=logs", "sess"))
	assert.Equal(t, int64(7), gotID)
	// The desktop side canonicalizes and policy-checks; opsctl must not rewrite the
	// command on the way there, or approval would show something else than was typed.
	assert.Equal(t, "list_objects --bucket=logs", gotCommand)
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

func TestExtDevRefusesInProduction(t *testing.T) {
	t.Setenv("OPSKAT_ENV", "production")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0o600))
	sent := stubDevInstall(t, func(string) (string, string, error) { return "acme", "1.0.0", nil })

	assert.Equal(t, 1, cmdExtDev([]string{dir}))
	assert.Empty(t, *sent, "nothing may be sent once the command refuses")
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

	require.Equal(t, 0, cmdExt([]string{"dev", dir}))
	assert.Equal(t, dir, *sent)
}
