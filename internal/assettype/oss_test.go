package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSSHandlerRegistered(t *testing.T) {
	h, ok := Get(asset_entity.AssetTypeOSS)
	require.True(t, ok)
	assert.Equal(t, "oss", h.Type())
}

func TestOSSValidateCreateArgs(t *testing.T) {
	h := &ossHandler{}
	require.Error(t, h.ValidateCreateArgs(map[string]any{}))
	require.Error(t, h.ValidateCreateArgs(map[string]any{"endpoint": "s3.amazonaws.com"}))
	require.NoError(t, h.ValidateCreateArgs(map[string]any{"endpoint": "s3.amazonaws.com", "access_key_id": "AKIA"}))
}

func TestOSSApplyCreateArgsAndSafeView(t *testing.T) {
	h := &ossHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeOSS}
	require.NoError(t, h.ApplyCreateArgs(context.Background(), a, map[string]any{
		"provider": "s3", "endpoint": "s3.us-east-1.amazonaws.com",
		"region": "us-east-1", "access_key_id": "AKIA", "use_ssl": true,
	}))
	cfg, err := a.GetOSSConfig()
	require.NoError(t, err)
	assert.Equal(t, "s3.us-east-1.amazonaws.com", cfg.Endpoint)
	assert.True(t, cfg.UseSSL)

	sv := h.SafeView(a)
	assert.Equal(t, "s3.us-east-1.amazonaws.com", sv["endpoint"])
	assert.Equal(t, "AKIA", sv["access_key_id"])

	_, hasSecretSnake := sv["secret_access_key"]
	assert.False(t, hasSecretSnake, "SafeView 不得泄露密钥 (snake_case key)")
	_, hasSecretCamel := sv["secretAccessKey"]
	assert.False(t, hasSecretCamel, "SafeView 不得泄露密钥 (camelCase key)")

	_, hasCredSnake := sv["credential_id"]
	assert.False(t, hasCredSnake, "SafeView 不得泄露凭证 ID (snake_case key)")
	_, hasCredCamel := sv["credentialId"]
	assert.False(t, hasCredCamel, "SafeView 不得泄露凭证 ID (camelCase key)")
}
