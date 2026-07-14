package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVNCHandlerRegistered(t *testing.T) {
	h, ok := Get(asset_entity.AssetTypeVNC)
	require.True(t, ok)
	assert.Equal(t, "vnc", h.Type())
	assert.Equal(t, 5900, h.DefaultPort())
}

func TestVNCValidateCreateArgs(t *testing.T) {
	h := &vncHandler{}
	require.Error(t, h.ValidateCreateArgs(map[string]any{}))
	require.NoError(t, h.ValidateCreateArgs(map[string]any{"host": "vnc.example.com"}))
}

func TestVNCApplyCreateArgsDefaultsAndSafeView(t *testing.T) {
	h := &vncHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeVNC}
	require.NoError(t, h.ApplyCreateArgs(context.Background(), a, map[string]any{
		"host":              "vnc.example.com",
		"username":          "operator",
		"file_ssh_asset_id": float64(7),
	}))

	cfg, err := a.GetVNCConfig()
	require.NoError(t, err)
	assert.Equal(t, "vnc.example.com", cfg.Host)
	assert.Equal(t, 5900, cfg.Port, "port should default to 5900 when omitted")
	assert.Equal(t, "operator", cfg.Username)
	assert.Equal(t, int64(7), cfg.FileSSHAssetID)

	sv := h.SafeView(a)
	assert.Equal(t, "vnc.example.com", sv["host"])
	_, hasPassword := sv["password"]
	assert.False(t, hasPassword, "SafeView 不得泄露密码")
	_, hasCredential := sv["credential_id"]
	assert.False(t, hasCredential, "SafeView 不得泄露凭证 ID")
}

func TestVNCApplyCreateArgsEncryptsPassword(t *testing.T) {
	h := &vncHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeVNC}
	require.NoError(t, h.ApplyCreateArgs(context.Background(), a, map[string]any{
		"host":     "vnc.example.com",
		"password": "s3cret",
	}))

	cfg, err := a.GetVNCConfig()
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.Password)
	assert.NotEqual(t, "s3cret", cfg.Password, "password must be stored encrypted")

	decrypted, err := credential_svc.Default().Decrypt(cfg.Password)
	require.NoError(t, err)
	assert.Equal(t, "s3cret", decrypted)
}

// TestVNCApplyUpdateArgsInlinePasswordClearsCredentialID 更新时新填的内联密码应清除
// 陈旧的托管凭证 —— 否则 ResolvePasswordGeneric 会因 credential_id>0 而忽略新密码。
func TestVNCApplyUpdateArgsInlinePasswordClearsCredentialID(t *testing.T) {
	h := &vncHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeVNC}
	require.NoError(t, a.SetVNCConfig(&asset_entity.VNCConfig{
		Host: "vnc.example.com", CredentialID: 5,
	}))

	require.NoError(t, h.ApplyUpdateArgs(context.Background(), a, map[string]any{
		"password": "newsecret",
	}))

	cfg, err := a.GetVNCConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.CredentialID)
	assert.NotEmpty(t, cfg.Password)

	decrypted, err := credential_svc.Default().Decrypt(cfg.Password)
	require.NoError(t, err)
	assert.Equal(t, "newsecret", decrypted)
}
