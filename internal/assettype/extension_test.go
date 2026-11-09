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

	prepared, err := PrepareCreate(context.Background(), &asset_entity.Asset{Type: "acme-store"}, map[string]any{"endpoint": "https://acme.test", "region": "cn"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"endpoint": "https://acme.test", "region": "cn"}, prepared.Approval)
	assert.Equal(t, CredentialKindNone, prepared.Credential.Kind)

	_, err = PrepareCreate(context.Background(), &asset_entity.Asset{Type: "acme-store"}, map[string]any{"endpoint": "https://acme.test", "nope": 1})
	require.Error(t, err, "fields outside configSchema must be rejected")
	assert.Contains(t, err.Error(), "nope")

	_, err = PrepareCreate(context.Background(), &asset_entity.Asset{Type: "acme-store"}, map[string]any{"region": "cn"})
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

// 决定 22：扩展类型与已有自定义类型标识重名时注册失败，错误指出冲突的自定义类型；
// 自定义类型及其资产照常解析。检查由组合根注入，未注入时不查（不带数据库的注册测试不受影响）。
func TestRegisterExtensionTypeRefusesACustomTypeSlug(t *testing.T) {
	env := setupGeneric(t)
	SetCustomTypeSlugLookup(CustomTypeSlugExists)
	t.Cleanup(func() { SetCustomTypeSlugLookup(nil) })

	spec := extSpec()
	spec.Type = "grafana"
	err := RegisterExtensionType(spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"grafana"`, "the error must name the custom type to rename or remove")
	_, registered := Get("grafana")
	assert.False(t, registered, "a refused type must not enter the registry")

	a, err := NewAsset(env.ctx, "grafana")
	require.NoError(t, err, "the custom type keeps serving its slug after the refused registration")
	assert.Equal(t, "grafana", TypeName(a))

	other := extSpec()
	require.NoError(t, RegisterExtensionType(other), "a type with no custom-type namesake still registers")
	Unregister(other.Type)
}

func TestRegisterExtensionTypeSurfacesALookupFailure(t *testing.T) {
	SetCustomTypeSlugLookup(func(context.Context, string) (bool, error) { return false, assert.AnError })
	t.Cleanup(func() { SetCustomTypeSlugLookup(nil) })

	require.ErrorIs(t, RegisterExtensionType(extSpec()), assert.AnError)
	_, registered := Get("acme-store")
	assert.False(t, registered)
}

func TestRegisterExtensionTypeSkipsTheCustomTypeCheckWhenNothingIsInjected(t *testing.T) {
	SetCustomTypeSlugLookup(nil)
	require.NoError(t, RegisterExtensionType(extSpec()))
	Unregister("acme-store")
}
