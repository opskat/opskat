package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteDesktopConfigDefaults(t *testing.T) {
	vnc := &Asset{Type: AssetTypeVNC, Config: `{"host":"vnc.example.com"}`}
	vncCfg, err := vnc.GetVNCConfig()
	require.NoError(t, err)
	require.Equal(t, 5900, vncCfg.Port)

	rdp := &Asset{Type: AssetTypeRDP, Config: `{"host":"rdp.example.com","username":"admin"}`}
	rdpCfg, err := rdp.GetRDPConfig()
	require.NoError(t, err)
	require.Equal(t, 3389, rdpCfg.Port)
	require.Equal(t, 1280, rdpCfg.ScreenWidth)
	require.Equal(t, 720, rdpCfg.ScreenHeight)
	require.Equal(t, 24, rdpCfg.ColorDepth)
}

func TestRemoteDesktopValidate(t *testing.T) {
	require.NoError(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"127.0.0.1","port":5901}`}).Validate())
	require.Error(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"","port":5901}`}).Validate())
	require.Error(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"127.0.0.1","port":70000}`}).Validate())

	require.NoError(t, (&Asset{Name: "rdp", Type: AssetTypeRDP, Config: `{"host":"127.0.0.1","port":3389,"username":"admin"}`}).Validate())
	require.Error(t, (&Asset{Name: "rdp", Type: AssetTypeRDP, Config: `{"host":"127.0.0.1","port":3389}`}).Validate())
	require.Error(t, (&Asset{Name: "rdp", Type: AssetTypeRDP, Config: `{"host":"127.0.0.1","port":3389,"username":"admin","color_depth":12}`}).Validate())
}
