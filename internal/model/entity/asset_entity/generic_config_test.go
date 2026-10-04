package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenericConfigRoundTrip(t *testing.T) {
	a := &Asset{Type: AssetTypeGeneric}
	cfg := &GenericConfig{
		CustomType: "grafana",
		Values: map[string]GenericValue{
			"host":  {Value: "grafana.local"},
			"token": {Value: "cipher"},
			"key":   {CredentialID: 7},
			"empty": {},
		},
	}
	require.NoError(t, a.SetGenericConfig(cfg))
	// 引用托管凭据的值必须以 credential_id 叶子落库，FindByCredentialID 才能找到引用方。
	assert.Contains(t, a.Config, `"credential_id":7`)

	got, err := a.GetGenericConfig()
	require.NoError(t, err)
	assert.Equal(t, "grafana", got.CustomType)
	assert.Equal(t, cfg.Values, got.Values)
	_, present := got.Values["empty"]
	assert.True(t, present, "显式空值与缺省（走默认值）必须可区分")
}

// 通用资产必须基于一个自定义类型（Design decision 2）。
func TestValidateGenericRequiresCustomType(t *testing.T) {
	a := &Asset{Name: "g", Type: AssetTypeGeneric}
	require.NoError(t, a.SetGenericConfig(&GenericConfig{}))
	require.Error(t, a.Validate())

	require.NoError(t, a.SetGenericConfig(&GenericConfig{CustomType: "grafana"}))
	require.NoError(t, a.Validate())

	a.Config = `{"custom_type":`
	require.Error(t, a.Validate())
}

func TestGetGenericConfigWrongType(t *testing.T) {
	a := &Asset{Type: AssetTypeSSH, Config: `{}`}
	_, err := a.GetGenericConfig()
	require.Error(t, err)
	assert.False(t, a.IsGeneric())
}
