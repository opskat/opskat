package extension_svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/pkg/appversion"
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
