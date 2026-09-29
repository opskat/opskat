package assettype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

type genericEnv struct {
	ctx      context.Context
	password int64 // 托管密码凭据
	sshKey   int64 // 托管 SSH 密钥凭据（类型不被接受）
}

// setupGeneric 用内存 SQLite 跑真实仓储：通用资产的字段契约来自数据库里的自定义类型。
func setupGeneric(t *testing.T) *genericEnv {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&custom_type_entity.CustomType{}, &asset_entity.Asset{}, &credential_entity.Credential{}))
	db.SetDefault(gdb)
	oldAsset, oldCredential, oldCustomType := asset_repo.Asset(), credential_repo.Credential(), custom_type_repo.CustomType()
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	credential_repo.RegisterCredential(credential_repo.NewCredential())
	custom_type_repo.RegisterCustomType(custom_type_repo.New())
	t.Cleanup(func() {
		asset_repo.RegisterAsset(oldAsset)
		credential_repo.RegisterCredential(oldCredential)
		custom_type_repo.RegisterCustomType(oldCustomType)
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	custom_type_svc.CustomType().SetReservedNames(RegisteredTypes)

	ctx := context.Background()
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "Grafana", Slug: "grafana", Icon: "gauge-circle", ExecMode: custom_type_entity.ExecModeHTTP,
		Fields: []custom_type_entity.Field{
			{Name: "host", Required: true},
			{Name: "org", Required: true, Default: "main"},
			{Name: "token", Secret: true, Required: true},
			{Name: "note"},
		},
		HTTP: &custom_type_entity.HTTPConfig{BaseURL: "https://{{host}}"},
	}))
	env := &genericEnv{ctx: ctx}
	cipher, err := credential_svc.Default().Encrypt("managed-token")
	require.NoError(t, err)
	password := &credential_entity.Credential{Name: "grafana token", Type: credential_entity.TypePassword, Password: cipher}
	require.NoError(t, credential_repo.Credential().Create(ctx, password))
	sshKey := &credential_entity.Credential{Name: "deploy key", Type: credential_entity.TypeSSHKey}
	require.NoError(t, credential_repo.Credential().Create(ctx, sshKey))
	env.password, env.sshKey = password.ID, sshKey.ID
	return env
}

func (e *genericEnv) newAsset(t *testing.T) *asset_entity.Asset {
	t.Helper()
	a, err := NewAsset(e.ctx, "grafana")
	require.NoError(t, err)
	a.Name = "grafana-prod"
	return a
}

// create 走与 asset_put_svc 相同的 prepare → apply 路径，返回落库前的资产。
func (e *genericEnv) create(t *testing.T, config map[string]any) (*asset_entity.Asset, PreparedCreate) {
	t.Helper()
	a := e.newAsset(t)
	prepared, err := PrepareCreate(e.ctx, a, config)
	require.NoError(t, err)
	require.NoError(t, prepared.Handler.ApplyCreateArgs(e.ctx, a, prepared.Config))
	return a, prepared
}

func TestNewAssetResolvesCallerFacingTypeNames(t *testing.T) {
	env := setupGeneric(t)

	a, err := NewAsset(env.ctx, "grafana")
	require.NoError(t, err)
	assert.Equal(t, asset_entity.AssetTypeGeneric, a.Type, "a custom type slug is stored as a generic asset")
	cfg, err := a.GetGenericConfig()
	require.NoError(t, err)
	assert.Equal(t, "grafana", cfg.CustomType)
	assert.Equal(t, "grafana", TypeName(a), "the caller-facing type name of a generic asset is its slug")

	ssh, err := NewAsset(env.ctx, asset_entity.AssetTypeSSH)
	require.NoError(t, err)
	assert.Equal(t, asset_entity.AssetTypeSSH, ssh.Type)
	assert.Equal(t, asset_entity.AssetTypeSSH, TypeName(ssh))

	_, err = NewAsset(env.ctx, "no-such-type")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnknownType), "got %v", err)
	assert.Contains(t, err.Error(), "no-such-type")
}

func TestGenericPrepareCreateUsesTheCustomTypeFieldContract(t *testing.T) {
	env := setupGeneric(t)

	tests := []struct {
		name   string
		config map[string]any
		want   []string
	}{
		{name: "unknown field", config: map[string]any{"host": "g.internal", "token": "t", "password": "x"}, want: []string{"unknown config field", "password"}},
		{name: "missing required", config: map[string]any{"note": "n"}, want: []string{"host", "token"}},
		{name: "required given empty", config: map[string]any{"host": "", "token": "t"}, want: []string{"host"}},
		{name: "required default overridden empty", config: map[string]any{"host": "g", "org": "", "token": "t"}, want: []string{"org"}},
		{name: "plain field not a string", config: map[string]any{"host": float64(3), "token": "t"}, want: []string{"host", "string"}},
		{name: "plain field given a credential", config: map[string]any{"host": map[string]any{"credential_id": float64(1)}, "token": "t"}, want: []string{"host", "string"}},
		{name: "secret not string or reference", config: map[string]any{"host": "g", "token": true}, want: []string{"token", "credential_id"}},
		{name: "secret reference with extra key", config: map[string]any{"host": "g", "token": map[string]any{"credential_id": float64(1), "value": "x"}}, want: []string{"token", "credential_id"}},
		{name: "secret reference not positive", config: map[string]any{"host": "g", "token": map[string]any{"credential_id": float64(0)}}, want: []string{"credential_id"}},
		{name: "secret reference missing", config: map[string]any{"host": "g", "token": map[string]any{"credential_id": float64(999)}}, want: []string{"999"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PrepareCreate(env.ctx, env.newAsset(t), tt.config)
			require.Error(t, err)
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}

	t.Run("secret reference of a non-password credential", func(t *testing.T) {
		_, err := PrepareCreate(env.ctx, env.newAsset(t), map[string]any{
			"host": "g", "token": map[string]any{"credential_id": env.sshKey},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), credential_entity.TypeSSHKey)
	})

	t.Run("generic without a custom type", func(t *testing.T) {
		a, err := NewAsset(env.ctx, asset_entity.AssetTypeGeneric)
		require.NoError(t, err)
		_, err = PrepareCreate(env.ctx, a, map[string]any{"host": "g"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "custom type")
	})
}

func TestGenericCreateStoresValuesAndKeepsSecretsOutOfApproval(t *testing.T) {
	env := setupGeneric(t)

	a, prepared := env.create(t, map[string]any{"host": "g.internal", "token": "s3cret-token", "note": ""})

	assert.Equal(t, map[string]any{"host": "g.internal", "note": ""}, prepared.Approval,
		"approval carries non-secret fields only")
	assert.NotContains(t, a.Config, "s3cret-token", "a plaintext secret is encrypted before it is stored")
	cfg, err := a.GetGenericConfig()
	require.NoError(t, err)
	assert.Equal(t, "grafana", cfg.CustomType)
	assert.NotContains(t, cfg.Values, "org", "an absent field keeps resolving to the type default")

	require.NoError(t, asset_repo.Asset().Create(env.ctx, a))
	resolved, err := custom_type_svc.CustomType().ResolveAsset(env.ctx, a)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"host": "g.internal", "org": "main", "token": "s3cret-token", "note": ""}, resolved.Values)
	assert.Empty(t, resolved.Missing)

	view := prepared.Handler.SafeView(a)
	assert.Equal(t, map[string]any{"custom_type": "grafana"}, view)
}

func TestGenericCreateStoresManagedCredentialReference(t *testing.T) {
	env := setupGeneric(t)

	a, prepared := env.create(t, map[string]any{"host": "g", "token": map[string]any{"credential_id": float64(env.password)}})

	assert.NotContains(t, prepared.Approval, "token")
	cfg, err := a.GetGenericConfig()
	require.NoError(t, err)
	assert.Equal(t, asset_entity.GenericValue{CredentialID: env.password}, cfg.Values["token"])
	require.NoError(t, asset_repo.Asset().Create(env.ctx, a))
	resolved, err := custom_type_svc.CustomType().ResolveAsset(env.ctx, a)
	require.NoError(t, err)
	assert.Equal(t, "managed-token", resolved.Values["token"])
}

func TestGenericUpdateMergesOnlyTheGivenFields(t *testing.T) {
	env := setupGeneric(t)
	a, _ := env.create(t, map[string]any{"host": "g", "token": "old-token", "note": "keep"})
	require.NoError(t, asset_repo.Asset().Create(env.ctx, a))

	prepared, err := PrepareUpdate(env.ctx, a, map[string]any{"host": "g2", "token": map[string]any{"credential_id": int64(env.password)}})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"host": "g2"}, prepared.Approval)
	require.NoError(t, prepared.Handler.ApplyUpdateArgs(env.ctx, a, prepared.Config))

	resolved, err := custom_type_svc.CustomType().ResolveAsset(env.ctx, a)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"host": "g2", "org": "main", "token": "managed-token", "note": "keep"}, resolved.Values)

	for name, config := range map[string]map[string]any{
		"unknown field":        {"nope": "x"},
		"required made empty":  {"token": ""},
		"secret of wrong type": {"token": float64(1)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := PrepareUpdate(env.ctx, a, config)
			require.Error(t, err)
		})
	}
}

func TestGenericDefaultPolicyIsCopiedFromTheCustomType(t *testing.T) {
	env := setupGeneric(t)
	a := env.newAsset(t)

	p, ok, err := policy.DefaultPolicyForAsset(env.ctx, a.Type, a.Config)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, &policy.CommandPolicy{AllowList: []string{"GET *", "HEAD *", "OPTIONS *"}}, p)

	missing := &asset_entity.Asset{Type: asset_entity.AssetTypeGeneric}
	require.NoError(t, missing.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "deleted-type"}))
	_, _, err = policy.DefaultPolicyForAsset(env.ctx, missing.Type, missing.Config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleted-type")
}

func TestGenericApplyCreateArgsUsesCustomTypeIconWhenEmpty(t *testing.T) {
	env := setupGeneric(t)

	a := env.newAsset(t)
	// Icon is empty when creating via opsctl without --icon
	a.Icon = ""

	prepared, err := PrepareCreate(env.ctx, a, map[string]any{"host": "g.internal", "token": "s3cret"})
	require.NoError(t, err)
	require.NoError(t, prepared.Handler.ApplyCreateArgs(env.ctx, a, prepared.Config))

	assert.Equal(t, "gauge-circle", a.Icon, "empty icon should be set to custom type's icon")
}

func TestGenericApplyCreateArgsPreservesExplicitIcon(t *testing.T) {
	env := setupGeneric(t)

	a := env.newAsset(t)
	// Icon is explicitly set via --icon
	a.Icon = "globe"

	prepared, err := PrepareCreate(env.ctx, a, map[string]any{"host": "g.internal", "token": "s3cret"})
	require.NoError(t, err)
	require.NoError(t, prepared.Handler.ApplyCreateArgs(env.ctx, a, prepared.Config))

	assert.Equal(t, "globe", a.Icon, "explicit icon should be preserved")
}
