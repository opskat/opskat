package extension

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/appversion"
)

func TestCheckCompatible(t *testing.T) {
	release := appversion.Info{Version: "1.5.0", Kind: appversion.KindRelease}
	nightly := appversion.Info{Version: "1.5.0-nightly.20261001", Kind: appversion.KindNightly}
	dev := appversion.Info{Version: "1.0.0", Kind: appversion.KindDev}

	tests := []struct {
		name          string
		hostABI, minV string
		app           appversion.Info
		reason        IncompatibleReason // zero = compatible
	}{
		{"supported abi, no min", "2.2", "", release, ""},
		{"min equals app", "2.2", "1.5.0", release, ""},
		{"min below app", "2.0", "1.4.9", release, ""},
		{"unsupported abi", "9.0", "", release, ReasonHostABI},
		{"unsupported abi on nightly", "9.0", "", nightly, ReasonHostABI},
		{"unsupported abi on dev", "9.0", "", dev, ReasonHostABI},
		{"release below min", "2.2", "1.6.0", release, ReasonMinAppVersion},
		{"beta release below its own base", "2.2", "1.5.0", appversion.Info{Version: "1.5.0-beta.1", Kind: appversion.KindRelease}, ReasonMinAppVersion},
		{"nightly ignores min", "2.2", "9.0.0", nightly, ""},
		{"dev ignores min", "2.2", "9.0.0", dev, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckCompatible(tt.hostABI, tt.minV, tt.app)
			if tt.reason == "" {
				require.NoError(t, err)
				return
			}
			var inc *IncompatibleError
			require.True(t, errors.As(err, &inc), "want typed IncompatibleError, got %v", err)
			assert.Equal(t, tt.reason, inc.Reason)
			assert.Equal(t, tt.hostABI, inc.HostABI)
			assert.Equal(t, tt.minV, inc.MinAppVersion)
			assert.Equal(t, tt.app.Version, inc.AppVersion)
			assert.Equal(t, SupportedHostABIs, inc.SupportedHostABIs)
		})
	}
}

func TestCheckSourceCompatible(t *testing.T) {
	release := appversion.Info{Version: "1.5.0", Kind: appversion.KindRelease}
	manifest := func(abi, minV string) []byte {
		return []byte(`{"name":"x","version":"1.0.0","hostABI":"` + abi + `","minAppVersion":"` + minV + `"}`)
	}
	dir := func(m []byte) string {
		d := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(d, "manifest.json"), m, 0o600))
		return d
	}
	zipOf := func(m []byte) string {
		p := filepath.Join(t.TempDir(), "x.zip")
		f, err := os.Create(p) //nolint:gosec // test TempDir
		require.NoError(t, err)
		w := zip.NewWriter(f)
		e, err := w.Create("manifest.json")
		require.NoError(t, err)
		_, err = e.Write(m)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		require.NoError(t, f.Close())
		return p
	}
	var inc *IncompatibleError
	for name, src := range map[string]func([]byte) string{"dir": dir, "zip": zipOf} {
		require.NoError(t, CheckSourceCompatible(src(manifest("2.2", "")), release), name)
		err := CheckSourceCompatible(src(manifest("3.0", "")), release)
		require.True(t, errors.As(err, &inc), "%s: %v", name, err)
		assert.Equal(t, ReasonHostABI, inc.Reason)
		err = CheckSourceCompatible(src(manifest("2.2", "2.0.0")), release)
		require.True(t, errors.As(err, &inc), "%s: %v", name, err)
		assert.Equal(t, ReasonMinAppVersion, inc.Reason)
	}
	assert.Error(t, CheckSourceCompatible(t.TempDir(), release), "a source without manifest.json is an error, not compatible")
}
