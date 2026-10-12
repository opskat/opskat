package extstore

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/pkg/extension"
)

func ver(version, hostABI, minApp string) Version {
	return Version{Version: version, HostABI: hostABI, MinAppVersion: minApp, Source: Source{Type: SourceOCI}}
}

func withSource(v Version, typ string) Version {
	v.Source.Type = typ
	return v
}

func TestVersionInstallable(t *testing.T) {
	release := appversion.Info{Version: "1.14.0", Kind: appversion.KindRelease}

	t.Run("compatible oci version is installable", func(t *testing.T) {
		require.NoError(t, ver("1.0.0", "2.2", "1.14.0").Installable(release))
	})

	t.Run("unknown source type is not installable", func(t *testing.T) {
		err := withSource(ver("1.0.0", "2.2", ""), "ipfs").Installable(release)
		var se *UnsupportedSourceError
		require.True(t, errors.As(err, &se), "want UnsupportedSourceError, got %v", err)
		assert.Equal(t, "ipfs", se.Type)
	})

	t.Run("incompatible version carries the compatibility reason", func(t *testing.T) {
		err := ver("1.0.0", "9.0", "").Installable(release)
		var inc *extension.IncompatibleError
		require.True(t, errors.As(err, &inc), "want IncompatibleError, got %v", err)
		assert.Equal(t, extension.ReasonHostABI, inc.Reason)
	})
}

func TestSelectInstallable(t *testing.T) {
	release := appversion.Info{Version: "1.14.0", Kind: appversion.KindRelease}
	nightly := appversion.Info{Version: "1.14.0-nightly.20261003", Kind: appversion.KindNightly}

	// Deliberately out of order: selection must go by version, not position.
	ext := Extension{Name: "es", Versions: []Version{
		ver("1.2.0", "2.2", "1.14.0"),
		ver("1.0.0", "2.0", ""),
		ver("1.3.0", "9.0", ""), // needs a host ABI this app lacks
		ver("1.1.0", "2.2", ""),
	}}

	tests := []struct {
		name      string
		app       appversion.Info
		installed string
		action    Action
		version   string // "" = no version offered
	}{
		{"not installed → latest compatible", release, "", ActionInstall, "1.2.0"},
		{"installed older → update to latest compatible", release, "1.1.0", ActionUpdate, "1.2.0"},
		{"installed equals latest compatible", release, "1.2.0", ActionInstalled, ""},
		{"installed newer than latest compatible is never downgraded", release, "1.3.0", ActionInstalled, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choice, reason := SelectInstallable(ext, tt.app, tt.installed)
			require.NoError(t, reason)
			assert.Equal(t, tt.action, choice.Action)
			if tt.version == "" {
				assert.Nil(t, choice.Version)
				return
			}
			require.NotNil(t, choice.Version)
			assert.Equal(t, tt.version, choice.Version.Version)
		})
	}

	t.Run("release app skips versions whose minAppVersion is above it", func(t *testing.T) {
		e := Extension{Name: "es", Versions: []Version{ver("1.0.0", "2.2", ""), ver("2.0.0", "2.2", "1.15.0")}}
		choice, reason := SelectInstallable(e, release, "")
		require.NoError(t, reason)
		require.NotNil(t, choice.Version)
		assert.Equal(t, "1.0.0", choice.Version.Version)
	})

	t.Run("nightly app ignores minAppVersion", func(t *testing.T) {
		e := Extension{Name: "es", Versions: []Version{ver("1.0.0", "2.2", ""), ver("2.0.0", "2.2", "1.15.0")}}
		choice, reason := SelectInstallable(e, nightly, "")
		require.NoError(t, reason)
		require.NotNil(t, choice.Version)
		assert.Equal(t, "2.0.0", choice.Version.Version)
	})

	t.Run("newer version with unknown source is skipped", func(t *testing.T) {
		e := Extension{Name: "es", Versions: []Version{ver("1.0.0", "2.2", ""), withSource(ver("2.0.0", "2.2", ""), "ipfs")}}
		choice, reason := SelectInstallable(e, release, "")
		require.NoError(t, reason)
		require.NotNil(t, choice.Version)
		assert.Equal(t, "1.0.0", choice.Version.Version)
	})

	t.Run("no compatible version → unavailable with the newest version's reason", func(t *testing.T) {
		e := Extension{Name: "es", Versions: []Version{ver("2.0.0", "2.2", "1.16.0"), ver("1.0.0", "9.0", "")}}
		choice, reason := SelectInstallable(e, release, "")
		assert.Equal(t, ActionUnavailable, choice.Action)
		assert.Nil(t, choice.Version)
		var inc *extension.IncompatibleError
		require.True(t, errors.As(reason, &inc), "want IncompatibleError, got %v", reason)
		assert.Equal(t, extension.ReasonMinAppVersion, inc.Reason)
		assert.Equal(t, "1.16.0", inc.MinAppVersion)
	})

	t.Run("only an unknown source → unavailable, needs an OpsKat update", func(t *testing.T) {
		e := Extension{Name: "es", Versions: []Version{withSource(ver("1.0.0", "2.2", ""), "ipfs")}}
		choice, reason := SelectInstallable(e, release, "")
		assert.Equal(t, ActionUnavailable, choice.Action)
		var se *UnsupportedSourceError
		require.True(t, errors.As(reason, &se), "want UnsupportedSourceError, got %v", reason)
	})

	t.Run("installed with no compatible version offers nothing", func(t *testing.T) {
		e := Extension{Name: "es", Versions: []Version{ver("2.0.0", "9.0", "")}}
		choice, reason := SelectInstallable(e, release, "1.0.0")
		require.NoError(t, reason)
		assert.Equal(t, ActionInstalled, choice.Action)
		assert.Nil(t, choice.Version)
	})
}
