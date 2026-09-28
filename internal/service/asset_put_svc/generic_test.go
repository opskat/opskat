package asset_put_svc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/credential_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// setupGenericPut 在 setupPutTest 的库上加自定义类型表，并存一个带默认规则的类型。
func setupGenericPut(t *testing.T) *putTestEnv {
	t.Helper()
	env := setupPutTest(t)
	require.NoError(t, env.db.AutoMigrate(&custom_type_entity.CustomType{}))
	oldCustomType := custom_type_repo.CustomType()
	custom_type_repo.RegisterCustomType(custom_type_repo.New())
	t.Cleanup(func() { custom_type_repo.RegisterCustomType(oldCustomType) })
	custom_type_svc.CustomType().SetReservedNames(assettype.RegisteredTypes)
	require.NoError(t, custom_type_svc.CustomType().Save(env.ctx, &custom_type_entity.CustomType{
		Name: "Grafana", Slug: "grafana", ExecMode: custom_type_entity.ExecModeHTTP,
		Fields: []custom_type_entity.Field{
			{Name: "host", Required: true},
			{Name: "token", Secret: true, Required: true},
		},
		HTTP: &custom_type_entity.HTTPConfig{BaseURL: "https://{{host}}"},
	}))
	return env
}

func encryptForTest(t *testing.T, plain string) string {
	t.Helper()
	cipher, err := credential_svc.Default().Encrypt(plain)
	require.NoError(t, err)
	return cipher
}

func newGenericAsset(t *testing.T, env *putTestEnv, name string) *asset_entity.Asset {
	t.Helper()
	a, err := assettype.NewAsset(env.ctx, "grafana")
	require.NoError(t, err)
	a.Name = name
	return a
}

func TestPutGenericAssetCopiesTheTypeDefaultPolicyAndKeepsSecretsOutOfSafeViews(t *testing.T) {
	env := setupGenericPut(t)

	prepared, err := Prepare(env.ctx, Request{Asset: newGenericAsset(t, env, "grafana-prod"), Config: map[string]any{
		"host": "grafana.internal", "token": "glsa_topsecret",
	}})
	require.NoError(t, err)
	result, err := Commit(env.ctx, prepared)
	require.NoError(t, err)

	for _, view := range []map[string]any{prepared.SafeApprovalDetail(), prepared.SafeAuditArgsForResult(result)} {
		encoded, err := json.Marshal(view)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "glsa_topsecret")
		assert.NotContains(t, string(encoded), "token")
		assert.Contains(t, string(encoded), "grafana.internal")
	}

	stored, err := asset_repo.Asset().Find(env.ctx, result.ID)
	require.NoError(t, err)
	assert.Equal(t, asset_entity.AssetTypeGeneric, stored.Type)
	assert.JSONEq(t, `{"allow_list":["GET *","HEAD *","OPTIONS *"],"deny_list":null}`, stored.CmdPolicy,
		"a new generic asset starts with a copy of its custom type's default rules")
	assert.NotContains(t, stored.Config, "glsa_topsecret")
	resolved, err := custom_type_svc.CustomType().ResolveAsset(env.ctx, stored)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"host": "grafana.internal", "token": "glsa_topsecret"}, resolved.Values)
}

func TestPutGenericAssetUpdateReferencesManagedCredential(t *testing.T) {
	env := setupGenericPut(t)
	created, err := Put(env.ctx, Request{Asset: newGenericAsset(t, env, "grafana-prod"), Config: map[string]any{
		"host": "grafana.internal", "token": "old",
	}})
	require.NoError(t, err)
	cred := &credential_entity.Credential{Name: "grafana token", Type: credential_entity.TypePassword, Password: encryptForTest(t, "managed")}
	require.NoError(t, credential_repo.Credential().Create(env.ctx, cred))

	existing, err := asset_repo.Asset().Find(env.ctx, created.ID)
	require.NoError(t, err)
	_, err = Put(env.ctx, Request{Asset: existing, Config: map[string]any{"token": map[string]any{"credential_id": float64(cred.ID)}}})
	require.NoError(t, err)

	stored, err := asset_repo.Asset().Find(env.ctx, created.ID)
	require.NoError(t, err)
	resolved, err := custom_type_svc.CustomType().ResolveAsset(env.ctx, stored)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"host": "grafana.internal", "token": "managed"}, resolved.Values)
}

func TestPrepareGenericAssetRejectsWithoutWriting(t *testing.T) {
	env := setupGenericPut(t)
	for name, config := range map[string]map[string]any{
		"unknown field":    {"host": "g", "token": "t", "org": "x"},
		"missing required": {"host": "g"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Put(env.ctx, Request{Asset: newGenericAsset(t, env, "g"), Config: config})
			require.Error(t, err)
			assets, _ := env.counts(t)
			assert.Zero(t, assets)
		})
	}
}
