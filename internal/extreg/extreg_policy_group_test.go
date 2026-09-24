package extreg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/pkg/extension"
)

// 扩展权限组与策略面归属：一个策略面（policies.type）只属于一个扩展，一个权限组 ID
// 也只属于一个扩展。后来者撞上任何一个都整体拒绝，而不是静默覆盖先来者。

func otherManifest(t *testing.T, name, policyType, assetType, groupID string) *extension.Manifest {
	t.Cleanup(func() { Unregister(name) })
	m := testManifest()
	m.Name = name
	m.AssetTypes[0].Type = assetType
	m.Policies.Type = policyType
	m.Policies.Groups[0].ID = groupID
	m.Policies.Groups[0].Policy = map[string]any{"allow_list": []any{"object.list", "object.delete"}}
	m.Policies.Default = []string{groupID}
	return m
}

func assertAcmeGroupIntact(t *testing.T) {
	t.Helper()
	pg := policy_group_entity.FindExtensionGroup("ext:acme:readonly")
	require.NotNil(t, pg, "the first extension's group must survive")
	assert.Equal(t, "acme", pg.ExtensionName)
	assert.Contains(t, pg.Policy, `"deny_list":["object.delete"]`)
}

func TestRegisterRefusesAPolicyGroupIDOwnedByAnotherExtension(t *testing.T) {
	registerFake(t, &fakePlugin{})

	// beta 用自己的策略面，却声明了 acme 的组 ID（绕过描述符校验的手写 guest / 旧缓存）。
	other := otherManifest(t, "beta", "beta", "beta-store", "ext:acme:readonly")
	err := register(loaded{name: other.Name, manifest: other, plugin: &fakePlugin{}}, "help", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ext:acme:readonly")

	assertAcmeGroupIntact(t)
	_, stillThere := registered["beta"]
	assert.False(t, stillThere, "a refused extension must not be half-registered")

	// 卸载被拒的扩展（no-op）也不能带走 acme 的组。
	Unregister("beta")
	assertAcmeGroupIntact(t)
}

func TestRegisterRefusesAPolicyTypeOwnedByAnotherExtension(t *testing.T) {
	registerFake(t, &fakePlugin{})

	other := otherManifest(t, "beta", "acme", "beta-store", "ext:acme:wide-open")
	err := register(loaded{name: other.Name, manifest: other, plugin: &fakePlugin{}}, "help", "desc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"acme"`)

	assert.Nil(t, policy_group_entity.FindExtensionGroup("ext:acme:wide-open"),
		"the refused extension must not leave a group in the shared policy face")
	assertAcmeGroupIntact(t)
	_, ok := registered["beta"]
	assert.False(t, ok)
}

func TestDescribeOnlyRefusesAPolicyTypeOwnedByAnotherExtension(t *testing.T) {
	registerFake(t, &fakePlugin{})

	other := otherManifest(t, "beta", "acme", "beta-store", "ext:acme:wide-open")
	err := RegisterDescribeOnly(&extension.ManifestInfo{Name: "beta", Manifest: other})
	require.Error(t, err)
	assert.Nil(t, policy_group_entity.FindExtensionGroup("ext:acme:wide-open"))
	assertAcmeGroupIntact(t)
}

func TestPolicyTypeIsReleasedOnUnregister(t *testing.T) {
	registerFake(t, &fakePlugin{})
	Unregister("acme")

	other := otherManifest(t, "beta", "acme", "beta-store", "ext:acme:readonly")
	require.NoError(t, register(loaded{name: other.Name, manifest: other, plugin: &fakePlugin{}}, "help", "desc"))
	Unregister("beta")
}

// 策略面与内置策略类型同名时，用户自建的该内置类型权限组会被 CheckExtensionPolicy 当成
// 扩展规则来判（它只按 PolicyType 筛组）。内置类型名因此不能被任何扩展占用。
func TestRegisterRefusesABuiltinPolicyType(t *testing.T) {
	for _, builtin := range []string{policy_group_entity.PolicyTypeCommand, policy_group_entity.PolicyTypeOSS} {
		other := otherManifest(t, "beta", builtin, "beta-store", "ext:"+builtin+":readonly")
		err := register(loaded{name: other.Name, manifest: other, plugin: &fakePlugin{}}, "help", "desc")
		require.Error(t, err, builtin)
		assert.Contains(t, err.Error(), "built-in")
		assert.Nil(t, policy_group_entity.FindExtensionGroup("ext:"+builtin+":readonly"))

		err = RegisterDescribeOnly(&extension.ManifestInfo{Name: "beta", Manifest: other})
		require.Error(t, err, builtin)
		assert.Contains(t, err.Error(), "built-in")
	}
}
