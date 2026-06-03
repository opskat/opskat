package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalConfigRoundTrip(t *testing.T) {
	a := &Asset{Type: AssetTypeLocal}
	require.NoError(t, a.SetLocalConfig(&LocalConfig{
		Shell: "/bin/zsh", Args: []string{"-l"}, Cwd: "/tmp",
	}))
	cfg, err := a.GetLocalConfig()
	require.NoError(t, err)
	assert.Equal(t, "/bin/zsh", cfg.Shell)
	assert.Equal(t, []string{"-l"}, cfg.Args)
	assert.Equal(t, "/tmp", cfg.Cwd)
	assert.True(t, a.IsLocal())
}

func TestLocalAssetCanConnectWhenActive(t *testing.T) {
	a := &Asset{Type: AssetTypeLocal, Status: StatusActive}
	require.NoError(t, a.SetLocalConfig(&LocalConfig{}))
	assert.True(t, a.CanConnect(), "本地资产无需 host/port,激活即可连")
}

func TestLocalValidateAllowsEmptyShell(t *testing.T) {
	a := &Asset{Name: "my-shell", Type: AssetTypeLocal}
	require.NoError(t, a.SetLocalConfig(&LocalConfig{}))
	assert.NoError(t, a.Validate(), "shell 可空(运行时按 OS 兜底)")
}
