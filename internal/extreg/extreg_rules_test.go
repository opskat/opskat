package extreg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
)

// 扩展资产的永久规则（opsctl policy allow/deny/rm/show）。落点是共用的 CommandPolicy
// 列，规则形状 `ext:<policyType>:<action>`。

func TestExtensionAssetTypeHasAPermanentRuleLanding(t *testing.T) {
	registerFake(t, &fakePlugin{})

	require.True(t, permission.TypeRulesSupported("acme-store"),
		"opsctl policy allow/deny/rm must reach an extension asset like any other type")

	asset := &asset_entity.Asset{ID: 1, Name: "acme-1", Type: "acme-store"}
	landed, err := permission.AppendTypeRules(asset, "acme-store", permission.RuleAllow, []string{"object.list"})
	require.NoError(t, err)
	assert.Equal(t, []permission.LandedRule{{Rule: "ext:acme:object.list"}}, landed,
		"an extension rule must carry its policy-type segment: the CommandPolicy column is shared")

	cp, err := asset.GetCommandPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"ext:acme:object.list"}, cp.AllowList)
}

func TestExtensionRuleRefusesAnActionTheExtensionDoesNotDeclare(t *testing.T) {
	registerFake(t, &fakePlugin{})

	asset := &asset_entity.Asset{ID: 1, Name: "acme-1", Type: "acme-store"}
	_, err := permission.AppendTypeRules(asset, "acme-store", permission.RuleAllow, []string{"list_objects --bucket=prod"})
	require.Error(t, err, "extension rules are per action, not per command — a command must not land as a rule that never matches")
	assert.Contains(t, err.Error(), "object.list")
}

// 一个资产组可以同时挂着两个扩展的资产，而它们的规则落在同一列 CommandPolicy 上。
func TestExtensionRulesFromDifferentPolicyTypesDoNotCross(t *testing.T) {
	registerFake(t, &fakePlugin{})
	other := testManifest()
	other.Name = "beta"
	other.AssetTypes[0].Type = "beta-store"
	other.Policies.Type = "beta"
	other.Policies.Groups[0].ID = "ext:beta:readonly"
	other.Policies.Default = []string{"ext:beta:readonly"}
	require.NoError(t, register(loaded{name: other.Name, manifest: other, plugin: &fakePlugin{}}, "help", "desc"))
	t.Cleanup(func() { Unregister("beta") })

	group := &group_entity.Group{ID: 7, Name: "shared"}
	_, err := permission.AppendTypeRules(group, "acme-store", permission.RuleAllow, []string{"object.list"})
	require.NoError(t, err)
	_, err = permission.AppendTypeRules(group, "beta-store", permission.RuleDeny, []string{"object.delete"})
	require.NoError(t, err)

	allow, deny, err := permission.HolderOwnTypeRules(group, "acme-store")
	require.NoError(t, err)
	assert.Equal(t, []string{"ext:acme:object.list"}, allow)
	assert.Empty(t, deny, "the other extension's deny must not show up as this type's rule")

	allow, deny, err = permission.HolderOwnTypeRules(group, "beta-store")
	require.NoError(t, err)
	assert.Empty(t, allow)
	assert.Equal(t, []string{"ext:beta:object.delete"}, deny)
}

// object.write 是默认权限组既不 allow 也不 deny 的动作：这两个用例断言的是 holder
// 自己那一列，而不是 manifest 默认组。
func TestExtensionAllowRuleOnTheAssetSkipsApproval(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.write"})

	ctx := withGrantFixturePolicy(t, 1, "acme-store", &asset_entity.CommandPolicy{})
	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	require.Equal(t, aictx.NeedConfirm, got.Decision, "without a rule the user must be asked")

	ctx = withGrantFixturePolicy(t, 2, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"ext:acme:object.write"},
	})
	got = permission.CheckPermission(ctx, "acme-store", 2, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision, "a permanent allow rule must stop the approval prompt")
	assert.Equal(t, aictx.SourcePolicyAllow, got.DecisionSource)
}

func TestExtensionDenyRuleShadowsAnAllowRule(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.write"})
	ctx := withGrantFixturePolicy(t, 1, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"ext:acme:object.write"},
		DenyList:  []string{"ext:acme:object.write"},
	})

	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Deny, got.Decision, "deny is judged unconditionally first")
	assert.Equal(t, aictx.SourcePolicyDeny, got.DecisionSource)
}

func TestUnregisterRemovesTheRuleLandingWithoutLeaking(t *testing.T) {
	registerFake(t, &fakePlugin{})
	require.True(t, permission.TypeRulesSupported("acme-store"))

	Unregister("acme")

	assert.False(t, permission.TypeRulesSupported("acme-store"),
		"a disabled extension must take its rule landing with it")
	canonical, ok := permission.CanonicalForPolicyKind("command")
	require.True(t, ok)
	assert.Equal(t, asset_entity.AssetTypeSSH, canonical,
		"a runtime landing must never claim the shared command column's canonical type")
}
