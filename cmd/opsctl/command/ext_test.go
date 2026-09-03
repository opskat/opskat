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

func writeExtManifest(t *testing.T, dir, name, assetType string) {
	t.Helper()
	extDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(extDir, 0o755))
	manifest := map[string]any{
		"name":    name,
		"version": "1.0.0",
		"hostABI": "1.0",
		"i18n":    map[string]any{"displayName": name + " display", "description": name + " description"},
		"assetTypes": []map[string]any{{
			"type": assetType,
			"i18n": map[string]any{"name": assetType},
			"configSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"endpoint": map[string]any{"type": "string"}},
			},
		}},
		"tools": []map[string]any{{
			"name":       "list_objects",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
		}},
		"policies": map[string]any{"type": "ext:" + name},
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(extDir, "manifest.json"), data, 0o600))
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
	writeExtManifest(t, dir, "acme", "acme-store")
	// A directory whose manifest the app itself would refuse (no assetTypes) must not be
	// listed either: opsctl used to hand-roll a laxer parser and list it anyway.
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
	writeExtManifest(t, dir, "acme", "acme-store")

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
	writeExtManifest(t, dir, "acme", "acme-store")

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
