package assettype

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
)

func extSpec() ExtensionTypeSpec {
	return ExtensionTypeSpec{
		Type:                "acme-store",
		ExtensionName:       "acme",
		ConfigFields:        []string{"endpoint", "token", "region"},
		RequiredFields:      []string{"endpoint"},
		SecretFields:        []string{"token"},
		PolicyKind:          "ext:acme",
		DefaultPolicyGroups: []string{"ext:acme:readonly"},
	}
}

func TestRegisterExtensionTypeIsRemovable(t *testing.T) {
	require.NoError(t, RegisterExtensionType(extSpec()))
	t.Cleanup(func() { Unregister("acme-store") })

	h, ok := Get("acme-store")
	require.True(t, ok, "extension asset type must be visible through the shared registry")
	assert.Equal(t, "acme-store", h.Type())

	kind, ok := policyent.AssetKindOf("acme-store")
	require.True(t, ok)
	assert.Equal(t, "ext:acme", kind)

	Unregister("acme-store")
	_, ok = Get("acme-store")
	assert.False(t, ok, "Unregister must remove the type from the registry")
	_, ok = policyent.AssetKindOf("acme-store")
	assert.False(t, ok, "Unregister must remove the asset-kind mapping too")
}

func TestRegisterExtensionTypeRejectsConflict(t *testing.T) {
	require.NoError(t, RegisterExtensionType(extSpec()))
	t.Cleanup(func() { Unregister("acme-store") })

	err := RegisterExtensionType(extSpec())
	require.Error(t, err, "a second extension declaring the same asset type must be refused loudly")
	assert.Contains(t, err.Error(), "acme-store")

	// A built-in type is equally off-limits.
	clash := extSpec()
	clash.Type = asset_entity.AssetTypeSSH
	assert.Error(t, RegisterExtensionType(clash))
}

func TestExtensionTypeAutomationContract(t *testing.T) {
	require.NoError(t, RegisterExtensionType(extSpec()))
	t.Cleanup(func() { Unregister("acme-store") })

	prepared, err := PrepareCreate("acme-store", map[string]any{"endpoint": "https://acme.test", "region": "cn"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"endpoint": "https://acme.test", "region": "cn"}, prepared.Approval)
	assert.Equal(t, CredentialKindNone, prepared.Credential.Kind)

	_, err = PrepareCreate("acme-store", map[string]any{"endpoint": "https://acme.test", "nope": 1})
	require.Error(t, err, "fields outside configSchema must be rejected")
	assert.Contains(t, err.Error(), "nope")

	_, err = PrepareCreate("acme-store", map[string]any{"region": "cn"})
	require.Error(t, err, "configSchema.required must be enforced")
	assert.Contains(t, err.Error(), "endpoint")
}

func TestExtensionTypeSafeViewHidesSecretFields(t *testing.T) {
	h := newExtensionHandler(extSpec())
	asset := &asset_entity.Asset{Config: `{"endpoint":"https://acme.test","token":"cipher","region":"cn"}`}

	view := h.SafeView(asset)
	assert.Equal(t, map[string]any{"endpoint": "https://acme.test", "region": "cn"}, view)

	contract := h.AutomationContract()
	assert.NotContains(t, contract.ApprovalFields, "token")
	assert.Contains(t, contract.ConfigFields, "token")
}

func TestExtensionTypeApplyUpdateMergesExistingConfig(t *testing.T) {
	h := newExtensionHandler(ExtensionTypeSpec{
		Type: "acme-store", ExtensionName: "acme",
		ConfigFields: []string{"endpoint", "region"},
	})
	asset := &asset_entity.Asset{Config: `{"endpoint":"https://acme.test","region":"cn"}`}

	require.NoError(t, h.ApplyUpdateArgs(context.Background(), asset, map[string]any{"region": "us"}))
	assert.JSONEq(t, `{"endpoint":"https://acme.test","region":"us"}`, asset.Config)
	assert.Equal(t, "acme", asset.ExtensionName)
}
