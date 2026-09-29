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
	"github.com/opskat/opskat/internal/repository/grant_repo"
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

// --- 参数级策略：guest 按参数给出 (action, resource)，规则 <action>[:<glob>] ---

func TestExtensionRuleWithAResourceGlobMatchesOnlyThatResource(t *testing.T) {
	cp := &asset_entity.CommandPolicy{AllowList: []string{"object.write:prod/*"}}

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
		AllowList: []string{"object.write"},
	})
	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision)
}

func TestExtensionResourceDenyBeatsAWiderAllow(t *testing.T) {
	cp := &asset_entity.CommandPolicy{
		AllowList: []string{"object.write"},
		DenyList:  []string{"object.write:prod/secret*"},
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
		AllowList: []string{"object.write:pub/*"},
		DenyList:  []string{"object.write:prod/*"},
	}

	registerFake(t, &fakePlugin{action: "object.write", resource: "prod/a"})
	ctx := withGrantFixturePolicy(t, 1, "acme-store", cp)
	permission.SaveGrantPattern(ctx, "sess-ext", 1, "acme-1", "acme-store", "ext:acme:object.write:prod/a")
	got := permission.CheckPermission(ctx, "acme-store", 1, command)
	assert.Equal(t, aictx.Deny, got.Decision, "a grant must not lift a deny")

	Unregister("acme")
	registerFake(t, &fakePlugin{action: "object.write", resource: "pub/a"})
	ctx = withGrantFixturePolicy(t, 2, "acme-store", cp)
	permission.SaveGrantPattern(ctx, "sess-ext", 2, "acme-1", "acme-store", "ext:acme:object.write:pub/a")
	got = permission.CheckPermission(ctx, "acme-store", 2, command)
	assert.Equal(t, aictx.Allow, got.Decision)
	assert.Equal(t, aictx.SourcePolicyAllow, got.DecisionSource, "allow is judged before the grant")

	Unregister("acme")
	registerFake(t, &fakePlugin{action: "object.write", resource: "tmp/a"})
	ctx = withGrantFixturePolicy(t, 3, "acme-store", cp)
	permission.SaveGrantPattern(ctx, "sess-ext", 3, "acme-1", "acme-store", "ext:acme:object.write:tmp/a")
	got = permission.CheckPermission(ctx, "acme-store", 3, command)
	assert.Equal(t, aictx.Allow, got.Decision)
	assert.Equal(t, aictx.SourceGrantAllow, got.DecisionSource, "no rule decides → the grant does")
}

// --- 多资源：guest 用 PolicyResources 给出 (action, [resources])，逐个资源判 ---

// 宽 allow 下，任一资源命中 deny 即拒——资源还是单个串时 "x,prod-1" 整体撞不上 prod-*。
func TestExtensionDenyOnAnyResourceBeatsABroadAllow(t *testing.T) {
	cp := &asset_entity.CommandPolicy{
		AllowList: []string{"object.write"},
		DenyList:  []string{"object.write:prod-*"},
	}
	registerFake(t, &fakePlugin{action: "object.write", resources: []string{"x", "prod-1"}})
	got := permission.CheckPermission(withGrantFixturePolicy(t, 1, "acme-store", cp), "acme-store", 1, "list_objects --bucket=x")
	assert.Equal(t, aictx.Deny, got.Decision)
	assert.Equal(t, aictx.SourcePolicyDeny, got.DecisionSource)
	assert.Equal(t, "object.write:prod-* (prod-1)", got.MatchedPattern,
		"the audit's matched pattern names the deny rule and the resource it denied")
}

// allow 须覆盖每个资源：logs-* 放行不了 [logs-a, secret]；grant 补上没覆盖的那个即放行，
// MatchedPattern 列出参与判定的规则与 grant。
func TestExtensionAllowMustCoverEveryResourceAndGrantsFillTheRest(t *testing.T) {
	const command = "list_objects --bucket=prod"
	cp := &asset_entity.CommandPolicy{AllowList: []string{"object.write:logs-*"}}
	registerFake(t, &fakePlugin{action: "object.write", resources: []string{"logs-a", "secret"}})
	ctx := withGrantFixturePolicy(t, 1, "acme-store", cp)

	got := permission.CheckPermission(ctx, "acme-store", 1, command)
	require.Equal(t, aictx.NeedConfirm, got.Decision, "secret is covered by no allow rule")

	permission.SaveGrantPattern(ctx, "sess-ext", 1, "acme-1", "acme-store", "ext:acme:object.write:secret")
	got = permission.CheckPermission(ctx, "acme-store", 1, command)
	assert.Equal(t, aictx.Allow, got.Decision)
	assert.Equal(t, aictx.SourceGrantAllow, got.DecisionSource)
	assert.Equal(t, "object.write:logs-*; ext:acme:object.write:secret", got.MatchedPattern)
}

// 通配资源：deny 可能重叠即命中，allow 须完整覆盖。
func TestExtensionWildcardResourceIsJudgedByTheNamesItStandsFor(t *testing.T) {
	const command = "list_objects --bucket=prod"
	cp := &asset_entity.CommandPolicy{
		AllowList: []string{"object.write:logs-*"},
		DenyList:  []string{"object.write:prod-*"},
	}
	judge := func(assetID int64, resources ...string) aictx.CheckResult {
		Unregister("acme")
		registerFake(t, &fakePlugin{action: "object.write", resources: resources})
		return permission.CheckPermission(withGrantFixturePolicy(t, assetID, "acme-store", cp), "acme-store", assetID, command)
	}

	assert.Equal(t, aictx.Allow, judge(1, "logs-2026-*").Decision, "logs-2026-* stands only for names logs-* covers")
	assert.Equal(t, aictx.NeedConfirm, judge(2, "logs*").Decision, "logs* also stands for names outside logs-*")
	assert.Equal(t, aictx.Deny, judge(3, "p*").Decision, "p* may name a prod- index")
	assert.Equal(t, aictx.Deny, judge(4, "logs-a", "*").Decision, "* may name any index")
}

// 多资源调用的"始终允许"：逐资源落 grant，下一次同样的调用逐资源命中。
func TestExtensionMultiResourceAllowAllGrantsEachResource(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.write", resources: []string{"a", "logs-*"}})
	ctx := withGrantFixture(t, 1, "acme-store")

	require.Equal(t, aictx.Allow, allowAllChecker().CheckForAsset(ctx, 1, "acme-store", "list_objects --bucket=prod").Decision)
	items, err := grant_repo.Grant().ListApprovedItems(ctx, "sess-ext")
	require.NoError(t, err)
	persisted := make([]string, 0, len(items))
	for _, item := range items {
		persisted = append(persisted, item.Command)
	}
	assert.Equal(t, []string{"ext:acme:object.write:a", "ext:acme:object.write:logs-*"}, persisted)

	got := permission.CheckPermission(ctx, "acme-store", 1, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision)
	assert.Equal(t, "ext:acme:object.write:a; ext:acme:object.write:logs-*", got.MatchedPattern)
}

// 资源 glob 与 manifest 权限组里的规则同一套语言。
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
		AllowList: []string{"object.nuke"},
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

// 动作按工具核对，不是按整个扩展的并集：一个只声明了 object.delete 的工具，guest 的
// check_policy 却答成另一个工具才有的 object.list，就是 guest 缺陷——不能借一条
// object.list 的 allow 规则让删除免审批。
func TestExtensionActionMustBeDeclaredByTheCalledTool(t *testing.T) {
	registerFake(t, &fakePlugin{action: "object.list"})
	ctx := withGrantFixturePolicy(t, 1, "acme-store", &asset_entity.CommandPolicy{
		AllowList: []string{"object.list"},
	})

	got := permission.CheckPermission(ctx, "acme-store", 1, "delete_bucket --bucket=prod")
	assert.Equal(t, aictx.NeedConfirm, got.Decision,
		"an action another tool declares must not classify a call of this tool")
}

// resource 是 guest 的任意文本，可以含 ':'。规则串在 action 之后的第一个 ':' 处切开，
// 其后整段都是 glob；guest 既不能借 resource 伪造 action 段，也不能借带 ':' 的 action
// 撞上一条带 glob 的规则。
func TestExtensionResourceWithColonsCannotEscapeTheRule(t *testing.T) {
	cp := &asset_entity.CommandPolicy{AllowList: []string{"object.write:prod"}}

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
		AllowList: []string{"object.write:a:*"},
	}), "acme-store", 3, "list_objects --bucket=prod")
	assert.Equal(t, aictx.Allow, got.Decision, "a glob may itself spell ':' to match such a resource")
}

func TestExtensionRuleLandsWithAResourceGlob(t *testing.T) {
	registerFake(t, &fakePlugin{})

	asset := &asset_entity.Asset{ID: 1, Name: "acme-1", Type: "acme-store"}
	landed, err := permission.AppendTypeRules(asset, "acme-store", permission.RuleAllow, []string{"object.list:prod-*"})
	require.NoError(t, err)
	assert.Equal(t, []permission.LandedRule{{Rule: "object.list:prod-*"}}, landed)

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

	assert.NotNil(t, permission.ShadowingDeny(view("object.write"), "acme-store", "object.write:prod/*"),
		"a deny without a resource covers every resource of the action")
	assert.NotNil(t, permission.ShadowingDeny(view("object.write:prod/*"), "acme-store", "object.write:prod/a"),
		"a deny whose glob covers the landed resource shadows it")
	assert.Nil(t, permission.ShadowingDeny(view("object.write:prod/*"), "acme-store", "object.write"),
		"a deny on some resources does not shadow an allow on all of them")
	assert.Nil(t, permission.ShadowingDeny(view("object.write:prod/*"), "acme-store", "object.list:prod/a"))
}
