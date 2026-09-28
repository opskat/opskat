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

// 扩展资产的永久规则（opsctl policy allow/deny/rm/show 与桌面端详情页）。
//
// 资产只有一种类型，它那一列里的规则就是这个类型的裸动作名；资产组同时挂着多种类型，
// 扩展规则落在组的扩展策略列里、按策略面分开，从不进 shell 的 CommandPolicy。

func TestExtensionAssetTypeHasAPermanentRuleLanding(t *testing.T) {
	registerFake(t, &fakePlugin{})

	require.True(t, permission.TypeRulesSupported("acme-store"),
		"opsctl policy allow/deny/rm must reach an extension asset like any other type")

	asset := &asset_entity.Asset{ID: 1, Name: "acme-1", Type: "acme-store"}
	landed, err := permission.AppendTypeRules(asset, "acme-store", permission.RuleAllow, []string{"object.list"})
	require.NoError(t, err)
	assert.Equal(t, []permission.LandedRule{{Rule: "object.list"}}, landed,
		"an asset's column belongs to its one type, so the rule is the bare action — the same shape the desktop writes")

	cp, err := asset.GetCommandPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"object.list"}, cp.AllowList)
}

func TestExtensionRuleRefusesAnActionTheExtensionDoesNotDeclare(t *testing.T) {
	registerFake(t, &fakePlugin{})

	asset := &asset_entity.Asset{ID: 1, Name: "acme-1", Type: "acme-store"}
	_, err := permission.AppendTypeRules(asset, "acme-store", permission.RuleAllow, []string{"list_objects --bucket=prod"})
	require.Error(t, err, "extension rules are per action, not per command — a command must not land as a rule that never matches")
	assert.Contains(t, err.Error(), "object.list")
}

func registerBeta(t *testing.T) {
	t.Helper()
	other := testManifest()
	other.Name = "beta"
	other.AssetTypes[0].Type = "beta-store"
	other.Policies.Type = "beta"
	other.Policies.Groups[0].ID = "ext:beta:readonly"
	other.Policies.Default = []string{"ext:beta:readonly"}
	require.NoError(t, register(loaded{name: other.Name, manifest: other, plugin: &fakePlugin{}}, "help", "desc"))
	t.Cleanup(func() { Unregister("beta") })
}

// 一个资产组可以同时挂着 ssh 资产和两个扩展的资产：扩展规则不能进 shell 那一列
// （否则 `object.list` 会被当成一条 shell 命令），两个扩展的规则也不能串。
func TestExtensionRulesOnAGroupStayOutOfTheShellColumnAndApart(t *testing.T) {
	registerFake(t, &fakePlugin{})
	registerBeta(t)

	group := &group_entity.Group{ID: 7, Name: "shared"}
	_, err := permission.AppendTypeRules(group, "acme-store", permission.RuleAllow, []string{"object.list"})
	require.NoError(t, err)
	_, err = permission.AppendTypeRules(group, "beta-store", permission.RuleDeny, []string{"object.delete"})
	require.NoError(t, err)

	shell, err := group.GetCommandPolicy()
	require.NoError(t, err)
	assert.Empty(t, shell.AllowList, "an extension rule must not become a shell rule of the group")
	assert.Empty(t, shell.DenyList)

	allow, deny, err := permission.HolderOwnTypeRules(group, "acme-store")
	require.NoError(t, err)
	assert.Equal(t, []string{"object.list"}, allow)
	assert.Empty(t, deny, "the other extension's deny must not show up as this type's rule")

	allow, deny, err = permission.HolderOwnTypeRules(group, "beta-store")
	require.NoError(t, err)
	assert.Empty(t, allow)
	assert.Equal(t, []string{"object.delete"}, deny)
}

// policy show --group 列出组自身每个非空策略面，扩展的面按它的策略面名列出。
func TestGroupShowListsTheExtensionFaceByItsPolicyType(t *testing.T) {
	registerFake(t, &fakePlugin{})

	group := &group_entity.Group{ID: 7, Name: "shared"}
	_, err := permission.AppendTypeRules(group, "acme-store", permission.RuleDeny, []string{"object.delete"})
	require.NoError(t, err)

	shapes, err := permission.ListHolderRuleShapes(group)
	require.NoError(t, err)
	require.Len(t, shapes, 1)
	assert.Equal(t, "acme", shapes[0].PolicyType)
	assert.Equal(t, []string{"object.delete"}, shapes[0].Deny)
}

// object.write 是默认权限组既不 allow 也不 deny 的动作：这两个用例断言的是 holder
// 自己那一列，而不是 manifest 默认组。规则按桌面端详情页写下的样子（裸动作名）给出。
func TestExtensionAllowRuleOnTheAssetSkipsApproval(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.write"})

	ctx := withGrantFixturePolicy(t, 1, "acme-store", &asset_entity.CommandPolicy{})
	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	require.Equal(t, aictx.NeedConfirm, got.Decision, "without a rule the user must be asked")

	ctx = withGrantFixturePolicy(t, 2, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"object.write"},
	})
	got = permission.CheckPermission(ctx, "acme-store", 2, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision, "a permanent allow rule must stop the approval prompt")
	assert.Equal(t, aictx.SourcePolicyAllow, got.DecisionSource)
}

func TestExtensionDenyRuleShadowsAnAllowRule(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.write"})
	ctx := withGrantFixturePolicy(t, 1, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"object.write"},
		DenyList:  []string{"object.write"},
	})

	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Deny, got.Decision, "deny is judged unconditionally first")
	assert.Equal(t, aictx.SourcePolicyDeny, got.DecisionSource)
}

// 组上写下的扩展 deny 沿资产 → 组链生效，盖过资产自身的 allow。
func TestExtensionDenyRuleOnTheGroupReachesItsAssets(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.write"})

	group := &group_entity.Group{ID: 7, Name: "shared"}
	_, err := permission.AppendTypeRules(group, "acme-store", permission.RuleDeny, []string{"object.write"})
	require.NoError(t, err)
	ctx := withGrantFixturePolicyInGroup(t, 1, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"object.write"},
	}, group)

	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Deny, got.Decision)
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
