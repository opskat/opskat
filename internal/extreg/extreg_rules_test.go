package extreg

import (
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

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

// --- 参数级策略：guest 按参数给出 (action, resource)，规则 ext:<type>:<action>[:<glob>] ---

func TestExtensionRuleWithAResourceGlobMatchesOnlyThatResource(t *testing.T) {
	cp := &asset_entity.CommandPolicy{AllowList: []string{"ext:acme:object.write:prod/*"}}

	registerFake(t, &fakePlugin{action: "object.write", resource: "prod/a.txt"})
	got := permission.CheckPermission(withGrantFixturePolicy(t, 1, "acme-store", cp), "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision, "the resource the guest classified must be matched against the rule's glob")
	assert.Equal(t, "object.write:prod/*", got.MatchedPattern)

	Unregister("acme")
	registerFake(t, &fakePlugin{action: "object.write", resource: "dev/a.txt"})
	got = permission.CheckPermission(withGrantFixturePolicy(t, 2, "acme-store", cp), "acme-store", 2, "list_objects --bucket=prod")
	assert.Equal(t, aictx.NeedConfirm, got.Decision, "a resource outside the glob must still ask")
}

func TestExtensionRuleWithoutAResourceMatchesAnyResource(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.write", resource: "anything/at/all"})
	ctx := withGrantFixturePolicy(t, 1, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"ext:acme:object.write"},
	})
	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision)
}

func TestExtensionResourceDenyBeatsAWiderAllow(t *testing.T) {
	cp := &asset_entity.CommandPolicy{
		AllowList: []string{"ext:acme:object.write"},
		DenyList:  []string{"ext:acme:object.write:prod/secret*"},
	}
	registerFake(t, &fakePlugin{action: "object.write", resource: "prod/secret.env"})
	got := permission.CheckPermission(withGrantFixturePolicy(t, 1, "acme-store", cp), "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Deny, got.Decision)

	Unregister("acme")
	registerFake(t, &fakePlugin{action: "object.write", resource: "prod/readme"})
	got = permission.CheckPermission(withGrantFixturePolicy(t, 2, "acme-store", cp), "acme-store", 2, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision)
}

// deny → allow → grant → confirm：一条已批准的 grant 盖不住 deny，而 allow 在 grant 之前判定。
func TestExtensionDecisionOrderIsDenyAllowGrantConfirm(t *testing.T) {
	const command = "list_objects --bucket=prod"
	cp := &asset_entity.CommandPolicy{
		AllowList: []string{"ext:acme:object.write:pub/*"},
		DenyList:  []string{"ext:acme:object.write:prod/*"},
	}

	registerFake(t, &fakePlugin{action: "object.write", resource: "prod/a"})
	ctx := withGrantFixturePolicy(t, 1, "acme-store", cp)
	permission.SaveGrantPattern(ctx, "sess-ext", 1, "acme-1", "acme-store", command)
	got := permission.CheckPermission(ctx, "acme-store", 1, command)
	assert.Equal(t, aictx.Deny, got.Decision, "a grant must not lift a deny")

	Unregister("acme")
	registerFake(t, &fakePlugin{action: "object.write", resource: "pub/a"})
	ctx = withGrantFixturePolicy(t, 2, "acme-store", cp)
	permission.SaveGrantPattern(ctx, "sess-ext", 2, "acme-1", "acme-store", command)
	got = permission.CheckPermission(ctx, "acme-store", 2, command)
	assert.Equal(t, aictx.Allow, got.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, got.DecisionSource, "allow is judged before the grant")

	Unregister("acme")
	registerFake(t, &fakePlugin{action: "object.write", resource: "tmp/a"})
	ctx = withGrantFixturePolicy(t, 3, "acme-store", cp)
	permission.SaveGrantPattern(ctx, "sess-ext", 3, "acme-1", "acme-store", command)
	got = permission.CheckPermission(ctx, "acme-store", 3, command)
	assert.Equal(t, aictx.Allow, got.Decision)
	assert.Equal(t, aictx.SourceGrantAllow, got.DecisionSource, "no rule decides → the grant does")
}

// 资源 glob 与 manifest 权限组里的规则同一套语言（组里的规则不带 ext:<type>: 前缀）。
func TestExtensionPolicyGroupRulesMatchResourceGlobs(t *testing.T) {
	m := testManifest()
	m.Policies.Groups[0].Policy = map[string]any{"allow_list": []any{"object.write:pub/*"}}
	l := loaded{name: m.Name, manifest: m, plugin: &fakePlugin{action: "object.write", resource: "pub/x"}}
	require.NoError(t, register(l, "help", "desc"))
	t.Cleanup(func() { Unregister(m.Name) })

	got := permission.CheckPermission(withGrantFixture(t, 1, "acme-store"), "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision)
}

// guest 返回的 action 必须在类型声明的动作集合里；否则视为 NeedConfirm（不查 grant）并记错误。
func TestExtensionUndeclaredActionNeedsConfirmAndIsLogged(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	origLogger := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(origLogger) })

	const command = "list_objects --bucket=prod"
	registerFake(t, &fakePlugin{action: "object.nuke"})
	ctx := withGrantFixturePolicy(t, 1, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"ext:acme:object.nuke"},
	})
	permission.SaveGrantPattern(ctx, "sess-ext", 1, "acme-1", "acme-store", command)

	got := permission.CheckPermission(ctx, "acme-store", 1, command)
	assert.Equal(t, aictx.NeedConfirm, got.Decision,
		"an action the type never declared must be asked about, whatever rules or grants say")

	entries := logs.FilterMessage("extension policy returned an undeclared action").All()
	require.Len(t, entries, 1)
	assert.Equal(t, zap.ErrorLevel, entries[0].Level)
	fields := entries[0].ContextMap()
	assert.Equal(t, "acme", fields["extension"])
	assert.Equal(t, "list_objects", fields["tool"])
	assert.Equal(t, "object.nuke", fields["action"])
}

// resource 是 guest 的任意文本，可以含 ':'。规则串在 action 之后的第一个 ':' 处切开，
// 其后整段都是 glob；guest 既不能借 resource 伪造 action 段，也不能借带 ':' 的 action
// 撞上一条带 glob 的规则。
func TestExtensionResourceWithColonsCannotEscapeTheRule(t *testing.T) {
	cp := &asset_entity.CommandPolicy{AllowList: []string{"ext:acme:object.write:prod"}}

	registerFake(t, &fakePlugin{action: "object.write", resource: "prod:extra"})
	got := permission.CheckPermission(withGrantFixturePolicy(t, 1, "acme-store", cp), "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.NeedConfirm, got.Decision, "a resource is matched whole against the glob, not split on ':'")

	Unregister("acme")
	registerFake(t, &fakePlugin{action: "object.write:prod"})
	got = permission.CheckPermission(withGrantFixturePolicy(t, 2, "acme-store", cp), "acme-store", 2, "list_objects --bucket=prod")
	assert.Equal(t, aictx.NeedConfirm, got.Decision, "an action carrying ':' is undeclared, never an action plus a resource")

	Unregister("acme")
	registerFake(t, &fakePlugin{action: "object.write", resource: "a:b"})
	got = permission.CheckPermission(withGrantFixturePolicy(t, 3, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"ext:acme:object.write:a:*"},
	}), "acme-store", 3, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision, "a glob may itself spell ':' to match such a resource")
}

func TestExtensionRuleLandsWithAResourceGlob(t *testing.T) {
	registerFake(t, &fakePlugin{})

	asset := &asset_entity.Asset{ID: 1, Name: "acme-1", Type: "acme-store"}
	landed, err := permission.AppendTypeRules(asset, "acme-store", permission.RuleAllow, []string{"object.list:prod-*"})
	require.NoError(t, err)
	assert.Equal(t, []permission.LandedRule{{Rule: "ext:acme:object.list:prod-*"}}, landed)

	for _, bad := range []string{"object.nuke:prod-*", "object.list:", "object.list:[", ":prod"} {
		_, err := permission.AppendTypeRules(asset, "acme-store", permission.RuleAllow, []string{bad})
		assert.Error(t, err, "%q must be refused before it lands", bad)
	}
}

func TestExtensionDenyShadowingUnderstandsResourceGlobs(t *testing.T) {
	registerFake(t, &fakePlugin{})
	view := func(deny ...string) *permission.TypeRuleView {
		v := &permission.TypeRuleView{}
		for _, d := range deny {
			v.Deny = append(v.Deny, permission.SourcedRule{Rule: d})
		}
		return v
	}

	assert.NotNil(t, permission.ShadowingDeny(view("ext:acme:object.write"), "acme-store", "ext:acme:object.write:prod/*"),
		"a deny without a resource covers every resource of the action")
	assert.NotNil(t, permission.ShadowingDeny(view("object.write:prod/*"), "acme-store", "ext:acme:object.write:prod/a"),
		"a group deny (no namespace prefix) whose glob covers the landed resource shadows it")
	assert.Nil(t, permission.ShadowingDeny(view("ext:acme:object.write:prod/*"), "acme-store", "ext:acme:object.write"),
		"a deny on some resources does not shadow an allow on all of them")
	assert.Nil(t, permission.ShadowingDeny(view("ext:acme:object.write:prod/*"), "acme-store", "ext:acme:object.list:prod/a"))
}
