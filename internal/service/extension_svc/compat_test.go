package extension_svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/repository/extension_state_repo/mock_extension_state_repo"
	"github.com/opskat/opskat/pkg/extmanifest"
)

func useApp(t *testing.T, info appversion.Info) {
	t.Helper()
	prev := currentApp
	currentApp = func() appversion.Info { return info }
	t.Cleanup(func() { currentApp = prev })
}

func sourceWithManifest(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600))
	return dir
}

// A local ZIP / directory install is refused up front, before anything is staged,
// with a reason in the user's language: a hostABI mismatch names the declared and
// the supported versions, a too-new minAppVersion says to update OpsKat.
func TestInstallRejectsIncompatibleExtension(t *testing.T) {
	useApp(t, appversion.Info{Version: "1.5.0", Kind: appversion.KindRelease})
	extDir := t.TempDir()
	svc := New(newTestManager(extDir), nil, nil, nil, nil, nil, nil)

	cases := []struct {
		name, manifest string
		zh, en         []string
		reason         extmanifest.IncompatibleReason
	}{
		{"newer hostABI", `{"name":"x","version":"1.0.0","hostABI":"9.0"}`,
			[]string{"hostABI 9.0", "仅支持 1.0"}, []string{"hostABI 9.0", "supports only 1.0"}, extmanifest.ReasonHostABI},
		{"nightly-era hostABI", `{"name":"x","version":"1.0.0","hostABI":"2.5","capabilities":{}}`,
			[]string{"hostABI 2.5", "仅支持 1.0"}, []string{"hostABI 2.5", "supports only 1.0"}, extmanifest.ReasonHostABI},
		{"minAppVersion", `{"name":"x","version":"1.0.0","hostABI":"1.0","minAppVersion":"2.0.0"}`,
			[]string{"2.0.0", "更新 OpsKat"}, []string{"2.0.0", "Update OpsKat"}, extmanifest.ReasonMinAppVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := sourceWithManifest(t, tc.manifest)

			_, err := svc.Install(aictx.WithPolicyLang(context.Background(), "zh-CN"), src, nil)
			var inc *extmanifest.IncompatibleError
			require.True(t, errors.As(err, &inc), "typed reason must survive: %v", err)
			assert.Equal(t, tc.reason, inc.Reason)
			for _, want := range tc.zh {
				assert.Contains(t, err.Error(), want)
			}

			_, err = svc.Install(aictx.WithPolicyLang(context.Background(), "en"), src, nil)
			require.Error(t, err)
			for _, want := range tc.en {
				assert.Contains(t, err.Error(), want)
			}
		})
	}

	entries, err := os.ReadDir(extDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, ".cache", e.Name(), "a refused install must not stage or install anything")
	}
}

// An installed extension the host refuses is listed with why, not dropped: a
// retired hostABI as "incompatible" naming the declared and supported versions, a
// manifest in another shape (or an extension that fails to load) as "invalid" with
// the parse / load error. None of them is loaded or registers a contribution.
func TestListInstalledShowsExtensionsTheHostRefuses(t *testing.T) {
	stub := installDescribeStub(t)
	dir := t.TempDir()
	writeTestExtension(stub, dir, "ext-good")
	for name, manifest := range map[string]string{
		"ext-legacy": `{"name":"ext-legacy","version":"1.4.0","hostABI":"2.3","backend":{"runtime":"wasm","binary":"main.wasm"}}`,
		"ext-old":    `{"name":"ext-old","version":"0.9.0","hostABI":"1.0","capabilities":["x"],"assetTypes":[{"type":"t"}],"backend":{"runtime":"wasm","binary":"main.wasm"}}`,
	} {
		sub := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(sub, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "manifest.json"), []byte(manifest), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "main.wasm"), minimalWASM, 0o600))
	}
	// Valid manifest, but no cached describe and a module that cannot describe itself.
	writeSlimManifest(dir, "ext-broken")

	ctrl := gomock.NewController(t)
	stateRepo := mock_extension_state_repo.NewMockExtensionStateRepo(ctrl)
	stateRepo.EXPECT().FindAll(gomock.Any()).Return(nil, nil)
	svc := New(newTestManager(dir), stateRepo, nil, nil, zap.NewNop(), nil, nil)
	t.Cleanup(func() { closeService(context.Background(), svc) })
	require.NoError(t, svc.Init(context.Background()))

	byName := map[string]ExtensionInfo{}
	for _, info := range svc.ListInstalled("zh-CN") {
		byName[info.Name] = info
	}
	require.Len(t, byName, 4)

	assert.Nil(t, byName["ext-good"].LoadError)

	legacy := byName["ext-legacy"]
	require.NotNil(t, legacy.LoadError)
	assert.Equal(t, LoadErrorIncompatible, legacy.LoadError.Kind)
	assert.Contains(t, legacy.LoadError.Message, "hostABI 2.3")
	assert.Contains(t, legacy.LoadError.Message, "1.0")
	assert.Equal(t, "1.4.0", legacy.Version)
	assert.False(t, legacy.Enabled)
	assert.Nil(t, legacy.Manifest)

	old := byName["ext-old"]
	require.NotNil(t, old.LoadError)
	assert.Equal(t, LoadErrorInvalid, old.LoadError.Kind)
	assert.Contains(t, old.LoadError.Message, "capabilities")
	assert.False(t, old.Enabled)
	assert.Nil(t, old.Manifest)

	broken := byName["ext-broken"]
	require.NotNil(t, broken.LoadError)
	assert.Equal(t, LoadErrorInvalid, broken.LoadError.Kind)
	assert.False(t, broken.Enabled)

	assert.ElementsMatch(t, []string{"ext-good"}, svc.Bridge().ListNames(), "a refused extension registers nothing")
	assert.Equal(t, []string{"ext-good"}, registeredAssetTypes(svc))

	en := map[string]ExtensionInfo{}
	for _, info := range svc.ListInstalled("en") {
		en[info.Name] = info
	}
	assert.Contains(t, en["ext-legacy"].LoadError.Message, "supports only 1.0")
}
