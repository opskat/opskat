package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVNCConfigDefaults(t *testing.T) {
	vnc := &Asset{Type: AssetTypeVNC, Config: `{"host":"vnc.example.com"}`}
	vncCfg, err := vnc.GetVNCConfig()
	require.NoError(t, err)
	require.Equal(t, 5900, vncCfg.Port)
}

func TestVNCValidate(t *testing.T) {
	require.NoError(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"127.0.0.1","port":5901}`}).Validate())
	require.Error(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"","port":5901}`}).Validate())
	require.Error(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"127.0.0.1","port":70000}`}).Validate())
}
