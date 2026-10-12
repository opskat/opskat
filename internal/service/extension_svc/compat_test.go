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
	"github.com/opskat/opskat/pkg/extension"
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
// with a reason in the user's language that says to update OpsKat.
func TestInstallRejectsIncompatibleExtension(t *testing.T) {
	useApp(t, appversion.Info{Version: "1.5.0", Kind: appversion.KindRelease})
	extDir := t.TempDir()
	svc := New(newTestManager(extDir), nil, nil, nil, nil, nil, nil)

	cases := []struct {
		name, manifest, zh, en string
		reason                 extension.IncompatibleReason
	}{
		{"hostABI", `{"name":"x","version":"1.0.0","hostABI":"9.0"}`, "hostABI 9.0", "hostABI 9.0", extension.ReasonHostABI},
		{"minAppVersion", `{"name":"x","version":"1.0.0","hostABI":"2.2","minAppVersion":"2.0.0"}`, "2.0.0", "2.0.0", extension.ReasonMinAppVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := sourceWithManifest(t, tc.manifest)

			_, err := svc.Install(aictx.WithPolicyLang(context.Background(), "zh-CN"), src, nil)
			var inc *extension.IncompatibleError
			require.True(t, errors.As(err, &inc), "typed reason must survive: %v", err)
			assert.Equal(t, tc.reason, inc.Reason)
			assert.Contains(t, err.Error(), "更新 OpsKat")
			assert.Contains(t, err.Error(), tc.zh)

			_, err = svc.Install(aictx.WithPolicyLang(context.Background(), "en"), src, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Update OpsKat")
			assert.Contains(t, err.Error(), tc.en)
		})
	}

	entries, err := os.ReadDir(extDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, ".cache", e.Name(), "a refused install must not stage or install anything")
	}
}
