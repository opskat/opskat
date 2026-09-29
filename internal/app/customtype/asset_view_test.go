package customtype

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// viewType 覆盖详情页要区分的几种字段：有默认值的普通字段、直接输入的密钥、托管凭据引用的
// 密钥，以及一个还没填的必填字段（模拟"类型新增了必填字段"）。
func viewType() *custom_type_entity.CustomType {
	return &custom_type_entity.CustomType{
		Name:     "Grafana",
		Slug:     "grafana",
		ExecMode: custom_type_entity.ExecModeHTTP,
		Usage:    "GET /api/search",
		Fields: []custom_type_entity.Field{
			{Name: "host", Label: "主机", Required: true},
			{Name: "org", Label: "组织", Default: "1"},
			{Name: "token", Label: "Token", Secret: true, Required: true},
			{Name: "webhook", Secret: true},
			{Name: "datasource", Label: "数据源", Required: true},
		},
		HTTP: &custom_type_entity.HTTPConfig{BaseURL: "https://{{host}}/o/{{org}}"},
	}
}

func createViewAsset(t *testing.T, b *CustomType) (*asset_entity.Asset, int64) {
	t.Helper()
	res, err := b.SaveCustomType(viewType())
	require.NoError(t, err)
	require.Empty(t, res.Issues)

	cred, err := credential_mgr_svc.CreatePassword(t.Context(), credential_mgr_svc.CreatePasswordRequest{
		Name: "grafana-prod-sa", Password: "managed-plaintext",
	})
	require.NoError(t, err)

	cipher, err := credential_svc.Default().Encrypt("inline-plaintext")
	require.NoError(t, err)

	a := &asset_entity.Asset{Name: "grafana-prod", Type: asset_entity.AssetTypeGeneric, Status: asset_entity.StatusActive}
	require.NoError(t, a.SetGenericConfig(&asset_entity.GenericConfig{CustomType: "grafana", Values: map[string]asset_entity.GenericValue{
		"host":    {Value: "grafana.example.com:3000"},
		"token":   {CredentialID: cred.ID},
		"webhook": {Value: cipher},
	}}))
	require.NoError(t, asset_repo.Asset().Create(t.Context(), a))
	return a, cred.ID
}

func TestGetGenericAssetView_ValuesMissingAndAddressWithoutSecrets(t *testing.T) {
	b := setup(t)
	a, credID := createViewAsset(t, b)

	view, err := b.GetGenericAssetView(a.ID)
	require.NoError(t, err)
	require.NotNil(t, view)

	assert.Equal(t, "grafana", view.Slug)
	assert.Equal(t, "Grafana", view.TypeName)
	assert.Equal(t, custom_type_entity.ExecModeHTTP, view.ExecMode)
	assert.Equal(t, "GET /api/search", view.Usage)
	assert.Equal(t, []string{"datasource"}, view.Missing)
	assert.Equal(t, "https://grafana.example.com:3000/o/1", view.ActualAddress)

	byName := map[string]GenericFieldView{}
	for _, f := range view.Fields {
		byName[f.Name] = f
	}
	require.Len(t, byName, 5, "every field of the type is listed, in structure order")
	assert.Equal(t, "host", view.Fields[0].Name)

	assert.Equal(t, "grafana.example.com:3000", byName["host"].Value)
	assert.Equal(t, "1", byName["org"].Value, "unfilled optional field shows its default")
	assert.True(t, byName["datasource"].Missing)
	assert.False(t, byName["datasource"].Set)

	token := byName["token"]
	assert.True(t, token.Secret)
	assert.True(t, token.Set)
	assert.Equal(t, credID, token.CredentialID)
	assert.Equal(t, "grafana-prod-sa", token.CredentialName)
	assert.Empty(t, token.Value)

	webhook := byName["webhook"]
	assert.True(t, webhook.Set)
	assert.Zero(t, webhook.CredentialID)
	assert.Empty(t, webhook.Value)

	raw, err := json.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "inline-plaintext", "the view must never carry a secret's plaintext")
	assert.NotContains(t, string(raw), "managed-plaintext", "the view must never carry a secret's plaintext")
}

func TestGetGenericAssetView_RejectsNonGenericAsset(t *testing.T) {
	b := setup(t)
	a := &asset_entity.Asset{Name: "web-01", Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive, Config: "{}"}
	require.NoError(t, asset_repo.Asset().Create(t.Context(), a))

	_, err := b.GetGenericAssetView(a.ID)
	assert.Error(t, err)
}

func TestRevealGenericSecret(t *testing.T) {
	b := setup(t)
	a, _ := createViewAsset(t, b)

	got, err := b.RevealGenericSecret(a.ID, "webhook")
	require.NoError(t, err)
	assert.Equal(t, "inline-plaintext", got)

	got, err = b.RevealGenericSecret(a.ID, "token")
	require.NoError(t, err)
	assert.Equal(t, "managed-plaintext", got)

	_, err = b.RevealGenericSecret(a.ID, "host")
	assert.Error(t, err, "only secret fields go through reveal; plain values are already in the view")

	_, err = b.RevealGenericSecret(a.ID, "nope")
	assert.Error(t, err)
}
