package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestOSSApplyUpdateArgsInlineSecretClearsCredentialID 更新时新填的内联 secret 应比
// 陈旧的托管凭证优先 —— 否则 ResolvePasswordGeneric 会因 credential_id>0 而忽略新密钥。
func TestOSSApplyUpdateArgsInlineSecretClearsCredentialID(t *testing.T) {
	h := &ossHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeOSS}
	require.NoError(t, a.SetOSSConfig(&asset_entity.OSSConfig{
		Endpoint: "s3.us-east-1.amazonaws.com", AccessKeyID: "AKIA", CredentialID: 5,
	}))

	err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{
		"secret_access_key": "newsecret",
	})
	require.NoError(t, err)

	cfg, err := a.GetOSSConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.CredentialID)
	assert.NotEmpty(t, cfg.SecretAccessKey)
	assert.NotEqual(t, "newsecret", cfg.SecretAccessKey)

	decrypted, err := credential_svc.Default().Decrypt(cfg.SecretAccessKey)
	require.NoError(t, err)
	assert.Equal(t, "newsecret", decrypted)
}

// TestOSSApplyCreateArgsInlineSecretClearsCredentialID Create 路径同理：若同时传入
// 陈旧/误传的 credential_id 与内联 secret，内联 secret 应最终生效。
func TestOSSApplyCreateArgsInlineSecretClearsCredentialID(t *testing.T) {
	h := &ossHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeOSS}

	err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
		"endpoint": "s3.us-east-1.amazonaws.com", "access_key_id": "AKIA",
		"credential_id": float64(5), "secret_access_key": "newsecret",
	})
	require.NoError(t, err)

	cfg, err := a.GetOSSConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.CredentialID)
	assert.NotEmpty(t, cfg.SecretAccessKey)
	assert.NotEqual(t, "newsecret", cfg.SecretAccessKey)

	decrypted, err := credential_svc.Default().Decrypt(cfg.SecretAccessKey)
	require.NoError(t, err)
	assert.Equal(t, "newsecret", decrypted)
}

// TestOSSHandlerPolicy 锁住 OSS 成为一等策略种类的接线：Register 依据 PolicyKind()
// 写 asset-kind 注册表，ResolvePolicyKind("oss") 与策略编辑器都靠这张表找到 oss。
func TestOSSHandlerPolicy(t *testing.T) {
	h := &ossHandler{}
	p := h.DefaultPolicy()
	require.NotNil(t, p)
	pol, ok := p.(*asset_entity.OSSPolicy)
	require.True(t, ok)
	assert.NotEmpty(t, pol.Groups, "default oss policy should reference builtin groups")

	kind, ok := policyent.AssetKindOf(asset_entity.AssetTypeOSS)
	require.True(t, ok, "Register should have written the asset-kind mapping from PolicyKind()")
	assert.Equal(t, policyent.PolicyKindOSS, kind)
}
