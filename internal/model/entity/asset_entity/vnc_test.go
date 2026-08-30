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
	require.Equal(t, VNCEncryptionServer, vncCfg.Encryption)
}

func TestVNCEncryptionPolicyRoundTrip(t *testing.T) {
	for _, policy := range []VNCEncryptionPolicy{
		VNCEncryptionServer,
		VNCEncryptionAlwaysMaximum,
		VNCEncryptionAlwaysOn,
		VNCEncryptionPreferOn,
		VNCEncryptionPreferOff,
	} {
		t.Run(string(policy), func(t *testing.T) {
			asset := &Asset{Name: "vnc", Type: AssetTypeVNC}
			require.NoError(t, asset.SetVNCConfig(&VNCConfig{Host: "vnc.example.com", Port: 5900, Encryption: policy}))

			cfg, err := asset.GetVNCConfig()
			require.NoError(t, err)
			require.Equal(t, policy, cfg.Encryption)
			require.NoError(t, asset.Validate())
		})
	}
}

func TestVNCEncryptionPolicyCompatibilityAndValidation(t *testing.T) {
	for _, config := range []string{
		`{"host":"vnc.example.com","port":5900}`,
		`{"host":"vnc.example.com","port":5900,"encryption":""}`,
	} {
		asset := &Asset{Name: "vnc", Type: AssetTypeVNC, Config: config}
		cfg, err := asset.GetVNCConfig()
		require.NoError(t, err)
		require.Equal(t, VNCEncryptionServer, cfg.Encryption)
		require.NoError(t, asset.Validate())
	}

	invalid := &Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"vnc.example.com","port":5900,"encryption":"downgrade"}`}
	require.ErrorContains(t, invalid.Validate(), "downgrade")
}

func TestVNCValidate(t *testing.T) {
	require.NoError(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"127.0.0.1","port":5901}`}).Validate())
	require.Error(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"","port":5901}`}).Validate())
	require.Error(t, (&Asset{Name: "vnc", Type: AssetTypeVNC, Config: `{"host":"127.0.0.1","port":70000}`}).Validate())
}
