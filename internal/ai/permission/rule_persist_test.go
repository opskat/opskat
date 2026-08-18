package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/policy_group_repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPolicyGroupRepo 返回预设的用户权限组，覆盖 CollectTypeRules 对引用组的解析。
type stubPolicyGroupRepo struct {
	policy_group_repo.PolicyGroupRepo
	groups map[int64]*policy_group_entity.PolicyGroup
}

func (s *stubPolicyGroupRepo) Find(_ context.Context, id int64) (*policy_group_entity.PolicyGroup, error) {
	if g, ok := s.groups[id]; ok {
		return g, nil
	}
	return nil, fmt.Errorf("policy group not found: %d", id)
}

func (s *stubPolicyGroupRepo) ListByIDs(_ context.Context, ids []int64) ([]*policy_group_entity.PolicyGroup, error) {
	var out []*policy_group_entity.PolicyGroup
	for _, id := range ids {
		if g, ok := s.groups[id]; ok {
			out = append(out, g)
		}
	}
	return out, nil
}

func mustPolicyJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func ruleTexts(rules []SourcedRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Rule)
	}
	return out
}

// --- 按类型落点（spec Testing decisions：≥三种形状 × allow/deny × 资产/组两种 holder）---

func TestAppendTypeRulesLandsEachShape(t *testing.T) {
	tests := []struct {
		name       string
		canonical  string
		side       RuleSide
		patterns   []string
		wantLanded []LandedRule
		readBack   func(policyentHolder) (allow, deny []string)
	}{
		{
			name:       "ssh asset allow lands CommandPolicy.AllowList",
			canonical:  asset_entity.AssetTypeSSH,
			side:       RuleAllow,
			patterns:   []string{"systemctl *"},
			wantLanded: []LandedRule{{Rule: "systemctl *"}},
			readBack: func(h policyentHolder) ([]string, []string) {
				p, err := h.GetCommandPolicy()
				require.NoError(t, err)
				return p.AllowList, p.DenyList
			},
		},
		{
			name:       "ssh group deny lands CommandPolicy.DenyList",
			canonical:  asset_entity.AssetTypeSSH,
			side:       RuleDeny,
			patterns:   []string{"rm -rf *"},
			wantLanded: []LandedRule{{Rule: "rm -rf *"}},
			readBack: func(h policyentHolder) ([]string, []string) {
				p, err := h.GetCommandPolicy()
				require.NoError(t, err)
				return p.AllowList, p.DenyList
			},
		},
		{
			name:       "database asset allow lands QueryPolicy.AllowTypes broader",
			canonical:  asset_entity.AssetTypeDatabase,
			side:       RuleAllow,
			patterns:   []string{"SELECT * FROM users"},
			wantLanded: []LandedRule{{Rule: "SELECT", Broader: true}},
			readBack: func(h policyentHolder) ([]string, []string) {
				p, err := h.GetQueryPolicy()
				require.NoError(t, err)
				return p.AllowTypes, p.DenyTypes
			},
		},
		{
			name:       "database group deny lands QueryPolicy.DenyTypes broader",
			canonical:  asset_entity.AssetTypeDatabase,
			side:       RuleDeny,
			patterns:   []string{"DROP TABLE users"},
			wantLanded: []LandedRule{{Rule: "DROP TABLE", Broader: true}},
			readBack: func(h policyentHolder) ([]string, []string) {
				p, err := h.GetQueryPolicy()
				require.NoError(t, err)
				return p.AllowTypes, p.DenyTypes
			},
		},
		{
			name:       "redis asset allow lands RedisPolicy.AllowList as-is",
			canonical:  asset_entity.AssetTypeRedis,
			side:       RuleAllow,
			patterns:   []string{"GET session:*"},
			wantLanded: []LandedRule{{Rule: "GET session:*"}},
			readBack: func(h policyentHolder) ([]string, []string) {
				p, err := h.GetRedisPolicy()
				require.NoError(t, err)
				return p.AllowList, p.DenyList
			},
		},
		{
			name:       "redis group deny lands RedisPolicy.DenyList as-is",
			canonical:  asset_entity.AssetTypeRedis,
			side:       RuleDeny,
			patterns:   []string{"FLUSHALL"},
			wantLanded: []LandedRule{{Rule: "FLUSHALL"}},
			readBack: func(h policyentHolder) ([]string, []string) {
				p, err := h.GetRedisPolicy()
				require.NoError(t, err)
				return p.AllowList, p.DenyList
			},
		},
		{
			name:       "mongo asset allow drops flags and lands op+collection broader",
			canonical:  asset_entity.AssetTypeMongoDB,
			side:       RuleAllow,
			patterns:   []string{"deleteMany users --db=prod --query={a:1}"},
			wantLanded: []LandedRule{{Rule: "deleteMany users", Broader: true}},
			readBack: func(h policyentHolder) ([]string, []string) {
				p, err := h.GetMongoPolicy()
				require.NoError(t, err)
				return p.AllowTypes, p.DenyTypes
			},
		},
		{
			name:       "cp:read asset allow lands prefixed path rule in CommandPolicy",
			canonical:  GrantToolCpRead,
			side:       RuleAllow,
			patterns:   []string{`/etc/\*`},
			wantLanded: []LandedRule{{Rule: `cp:read:/etc/\*`}},
			readBack: func(h policyentHolder) ([]string, []string) {
				p, err := h.GetCommandPolicy()
				require.NoError(t, err)
				return p.AllowList, p.DenyList
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.canonical, func(t *testing.T) {
			for _, holder := range []policyentHolder{
				&asset_entity.Asset{ID: 5, Name: "web-01", Type: tt.canonical},
				&group_entity.Group{ID: 2, Name: "prod"},
			} {
				landed, err := AppendTypeRules(holder, tt.canonical, tt.side, tt.patterns)
				require.NoError(t, err)
				assert.Equal(t, tt.wantLanded, landed)

				allow, deny := tt.readBack(holder)
				sideRules := allow
				if tt.side == RuleDeny {
					sideRules = deny
				}
				for _, want := range tt.wantLanded {
					assert.Contains(t, sideRules, want.Rule)
				}
				other := deny
				if tt.side == RuleDeny {
					other = allow
				}
				for _, want := range tt.wantLanded {
					assert.NotContains(t, other, want.Rule)
				}
			}
		})
	}
}

// policyentHolder 让 readBack 闭包能同时读四种形状；两种 holder 都实现它。
type policyentHolder interface {
	policyent.Holder
	GetCommandPolicy() (*policyent.CommandPolicy, error)
	GetQueryPolicy() (*policyent.QueryPolicy, error)
	GetRedisPolicy() (*policyent.RedisPolicy, error)
	GetMongoPolicy() (*policyent.MongoPolicy, error)
}

func TestAppendTypeRulesQueryMultiStatementLandsEachType(t *testing.T) {
	holder := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
	landed, err := AppendTypeRules(holder, asset_entity.AssetTypeDatabase, RuleAllow,
		[]string{"SELECT 1; SHOW TABLES"})
	require.NoError(t, err)
	assert.Equal(t, []LandedRule{
		{Rule: "SELECT", Broader: true},
		{Rule: "SHOW", Broader: true},
	}, landed)
}

func TestAppendTypeRulesDedupsExistingRule(t *testing.T) {
	holder := &asset_entity.Asset{Type: asset_entity.AssetTypeSSH}
	require.NoError(t, holder.SetCommandPolicy(&policyent.CommandPolicy{AllowList: []string{"uptime"}}))

	landed, err := AppendTypeRules(holder, asset_entity.AssetTypeSSH, RuleAllow, []string{"uptime", "df -h"})
	require.NoError(t, err)
	assert.Equal(t, []LandedRule{{Rule: "uptime"}, {Rule: "df -h"}}, landed)

	p, err := holder.GetCommandPolicy()
	require.NoError(t, err)
	assert.Equal(t, []string{"uptime", "df -h"}, p.AllowList)
}

func TestAppendTypeRulesRejectsUnparsableSQLAndLeavesPolicyUnchanged(t *testing.T) {
	holder := &asset_entity.Asset{Type: asset_entity.AssetTypeDatabase}
	_, err := AppendTypeRules(holder, asset_entity.AssetTypeDatabase, RuleAllow, []string{"SELEC FROM WHERE"})
	require.Error(t, err)
	p, err := holder.GetQueryPolicy()
	require.NoError(t, err)
	assert.Empty(t, p.AllowTypes)
}

func TestAppendTypeRulesRejectsEmptyPattern(t *testing.T) {
	holder := &asset_entity.Asset{Type: asset_entity.AssetTypeSSH}
	_, err := AppendTypeRules(holder, asset_entity.AssetTypeSSH, RuleAllow, []string{"  "})
	require.Error(t, err)
}

func TestAppendTypeRulesUnknownType(t *testing.T) {
	holder := &asset_entity.Asset{Type: "rdp"}
	_, err := AppendTypeRules(holder, "rdp", RuleAllow, []string{"x"})
	require.Error(t, err)
	assert.False(t, TypeRulesSupported("rdp"))
	assert.False(t, TypeRulesSupported(GrantToolCp))
	assert.True(t, TypeRulesSupported(GrantToolCpRead))
	for _, typ := range []string{
		asset_entity.AssetTypeSSH, asset_entity.AssetTypeSerial, asset_entity.AssetTypeDatabase,
		asset_entity.AssetTypeRedis, asset_entity.AssetTypeEtcd, asset_entity.AssetTypeMongoDB,
		asset_entity.AssetTypeKafka, asset_entity.AssetTypeK8s, asset_entity.AssetTypeOSS,
	} {
		assert.True(t, TypeRulesSupported(typ), typ)
	}
}

// --- 生效规则收集（与 policyHoldersForAsset 判定路径同源）与遮蔽检测 ---

func newCollectFixture(t *testing.T) (context.Context, *asset_entity.Asset) {
	t.Helper()
	groups := &stubGroupRepo{groups: map[int64]*group_entity.Group{
		2: {ID: 2, Name: "prod"},
	}}
	origGroup := group_repo.Group()
	group_repo.RegisterGroup(groups)
	t.Cleanup(func() { group_repo.RegisterGroup(origGroup) })

	// 用户权限组 7（command 形状）持有一条 deny。
	pg := &policy_group_entity.PolicyGroup{
		ID: 7, Name: "guard", PolicyType: policy_group_entity.PolicyTypeCommand,
		Policy: mustPolicyJSON(t, &policyent.CommandPolicy{DenyList: []string{"reboot"}}),
	}
	origPG := policy_group_repo.PolicyGroup()
	policy_group_repo.RegisterPolicyGroup(&stubPolicyGroupRepo{groups: map[int64]*policy_group_entity.PolicyGroup{7: pg}})
	t.Cleanup(func() { policy_group_repo.RegisterPolicyGroup(origPG) })

	asset := &asset_entity.Asset{ID: 5, Name: "web-01", Type: asset_entity.AssetTypeSSH, GroupID: 2}
	require.NoError(t, asset.SetCommandPolicy(&policyent.CommandPolicy{
		AllowList: []string{"uptime"},
		DenyList:  []string{"shutdown -h *"},
		Groups:    []string{"7"},
	}))
	return context.Background(), asset
}

func TestCollectTypeRulesAttributesEachLayer(t *testing.T) {
	ctx, asset := newCollectFixture(t)

	view, err := CollectTypeRules(ctx, asset, asset_entity.AssetTypeSSH)
	require.NoError(t, err)

	require.Len(t, view.Allow, 1)
	assert.Equal(t, "uptime", view.Allow[0].Rule)
	assert.Equal(t, RuleSourceAsset, view.Allow[0].Kind)
	assert.Equal(t, int64(5), view.Allow[0].HolderID)
	assert.Equal(t, "web-01", view.Allow[0].HolderName)

	denyByRule := map[string]SourcedRule{}
	for _, d := range view.Deny {
		denyByRule[d.Rule] = d
	}
	assetDeny, ok := denyByRule["shutdown -h *"]
	require.True(t, ok, "asset deny collected: %v", ruleTexts(view.Deny))
	assert.Equal(t, RuleSourceAsset, assetDeny.Kind)

	pgDeny, ok := denyByRule["reboot"]
	require.True(t, ok, "policy group deny collected: %v", ruleTexts(view.Deny))
	assert.Equal(t, RuleSourcePolicyGroup, pgDeny.Kind)
	assert.Equal(t, "7", pgDeny.PolicyGroupID)
	assert.Equal(t, "guard", pgDeny.PolicyGroupName)

	require.Len(t, view.Groups, 1)
	assert.Equal(t, PolicyGroupRef{ID: "7", Name: "guard"}, view.Groups[0])
}

func TestCollectTypeRulesGroupChainLayer(t *testing.T) {
	origGroup := group_repo.Group()
	groups := &stubGroupRepo{groups: map[int64]*group_entity.Group{
		2: {ID: 2, Name: "prod"},
	}}
	require.NoError(t, groups.groups[2].SetCommandPolicy(&policyent.CommandPolicy{DenyList: []string{"rm -rf *"}}))
	group_repo.RegisterGroup(groups)
	t.Cleanup(func() { group_repo.RegisterGroup(origGroup) })

	asset := &asset_entity.Asset{ID: 5, Name: "web-01", Type: asset_entity.AssetTypeSSH, GroupID: 2}
	view, err := CollectTypeRules(context.Background(), asset, asset_entity.AssetTypeSSH)
	require.NoError(t, err)
	require.Len(t, view.Deny, 1)
	assert.Equal(t, RuleSourceGroup, view.Deny[0].Kind)
	assert.Equal(t, int64(2), view.Deny[0].HolderID)
	assert.Equal(t, "prod", view.Deny[0].HolderName)
}

// redis 走类型形状（RdsPolicy）之外的组通用层：组链 CmdPolicy 的 deny 也参与遮蔽。
func TestCollectTypeRulesIncludesGenericLayerForNonCommandShapes(t *testing.T) {
	origGroup := group_repo.Group()
	groups := &stubGroupRepo{groups: map[int64]*group_entity.Group{
		2: {ID: 2, Name: "prod"},
	}}
	require.NoError(t, groups.groups[2].SetCommandPolicy(&policyent.CommandPolicy{DenyList: []string{"FLUSHALL"}}))
	group_repo.RegisterGroup(groups)
	t.Cleanup(func() { group_repo.RegisterGroup(origGroup) })

	asset := &asset_entity.Asset{ID: 5, Name: "cache", Type: asset_entity.AssetTypeRedis, GroupID: 2}
	// 资产自身那一列按 RedisPolicy 读（资产的策略只有一列，形状由类型决定），
	// 但组通用层只读组链的 CmdPolicy——资产的列不能经两层各出现一次。
	require.NoError(t, asset.SetRedisPolicy(&policyent.RedisPolicy{DenyList: []string{"CONFIG *"}}))
	view, err := CollectTypeRules(context.Background(), asset, asset_entity.AssetTypeRedis)
	require.NoError(t, err)
	require.Len(t, view.Deny, 2)
	assert.Equal(t, "CONFIG *", view.Deny[0].Rule)
	assert.Equal(t, RuleSourceAsset, view.Deny[0].Kind)
	assert.False(t, view.Deny[0].Generic)
	assert.Equal(t, "FLUSHALL", view.Deny[1].Rule)
	assert.True(t, view.Deny[1].Generic)
	assert.Equal(t, RuleSourceGroup, view.Deny[1].Kind)
}

func TestShadowingDenyThreeSources(t *testing.T) {
	ctx, asset := newCollectFixture(t)
	view, err := CollectTypeRules(ctx, asset, asset_entity.AssetTypeSSH)
	require.NoError(t, err)

	// 资产自身 deny 遮蔽。
	sh := ShadowingDeny(view, asset_entity.AssetTypeSSH, "shutdown -h now")
	require.NotNil(t, sh)
	assert.Equal(t, "shutdown -h *", sh.Rule)
	assert.Equal(t, RuleSourceAsset, sh.Kind)

	// 权限组 deny 遮蔽。
	sh = ShadowingDeny(view, asset_entity.AssetTypeSSH, "reboot")
	require.NotNil(t, sh)
	assert.Equal(t, RuleSourcePolicyGroup, sh.Kind)

	// 未被遮蔽 → nil。
	assert.Nil(t, ShadowingDeny(view, asset_entity.AssetTypeSSH, "uptime"))

	// 类型形状的遮蔽（QueryPolicy 的 DenyTypes 按语句类型遮蔽 allow 落点）。
	dbAsset := &asset_entity.Asset{ID: 6, Name: "db", Type: asset_entity.AssetTypeDatabase}
	require.NoError(t, dbAsset.SetQueryPolicy(&policyent.QueryPolicy{DenyTypes: []string{"SELECT"}}))
	dbView, err := CollectTypeRules(ctx, dbAsset, asset_entity.AssetTypeDatabase)
	require.NoError(t, err)
	sh = ShadowingDeny(dbView, asset_entity.AssetTypeDatabase, "SELECT")
	require.NotNil(t, sh)
	assert.Equal(t, "SELECT", sh.Rule)
	assert.Nil(t, ShadowingDeny(dbView, asset_entity.AssetTypeDatabase, "SHOW"))
}

// --- 撤销（rm）：目标自身的规则可枚举可移除 ---

func TestHolderOwnTypeRulesAndRemove(t *testing.T) {
	asset := &asset_entity.Asset{ID: 5, Type: asset_entity.AssetTypeSSH}
	require.NoError(t, asset.SetCommandPolicy(&policyent.CommandPolicy{
		AllowList: []string{"uptime", "df -h"},
		DenyList:  []string{"reboot"},
	}))

	allow, deny, err := HolderOwnTypeRules(asset, asset_entity.AssetTypeSSH)
	require.NoError(t, err)
	assert.Equal(t, []string{"uptime", "df -h"}, allow)
	assert.Equal(t, []string{"reboot"}, deny)

	require.NoError(t, RemoveTypeRule(asset, asset_entity.AssetTypeSSH, RuleAllow, "df -h"))
	allow, deny, err = HolderOwnTypeRules(asset, asset_entity.AssetTypeSSH)
	require.NoError(t, err)
	assert.Equal(t, []string{"uptime"}, allow)
	assert.Equal(t, []string{"reboot"}, deny)

	err = RemoveTypeRule(asset, asset_entity.AssetTypeSSH, RuleAllow, "df -h")
	require.Error(t, err, "removing a rule that is not there must fail")

	// 移空后 SetXxx 的 MarshalOrClear 会把列清回空串。
	require.NoError(t, RemoveTypeRule(asset, asset_entity.AssetTypeSSH, RuleAllow, "uptime"))
	require.NoError(t, RemoveTypeRule(asset, asset_entity.AssetTypeSSH, RuleDeny, "reboot"))
	p, err := asset.GetCommandPolicy()
	require.NoError(t, err)
	assert.True(t, p.IsEmpty())
	assert.Empty(t, asset.CmdPolicy)
}

// 组目标的 show：列出组自身全部非空形状列。
func TestListHolderRuleShapes(t *testing.T) {
	g := &group_entity.Group{ID: 2, Name: "prod"}
	require.NoError(t, g.SetCommandPolicy(&policyent.CommandPolicy{AllowList: []string{"uptime"}}))
	require.NoError(t, g.SetRedisPolicy(&policyent.RedisPolicy{DenyList: []string{"FLUSHALL"}}))

	shapes, err := ListHolderRuleShapes(g)
	require.NoError(t, err)
	kinds := make([]string, 0, len(shapes))
	for _, s := range shapes {
		kinds = append(kinds, s.PolicyType)
	}
	assert.Equal(t, []string{policy_group_entity.PolicyTypeCommand, policy_group_entity.PolicyTypeRedis}, kinds)
	assert.Equal(t, []string{"uptime"}, shapes[0].Allow)
	assert.Equal(t, []string{"FLUSHALL"}, shapes[1].Deny)
}

func TestListHolderRuleShapesPropagatesRegisteredShapeReadError(t *testing.T) {
	g := &group_entity.Group{ID: 2, Name: "prod", CmdPolicy: `{not-json`}

	shapes, err := ListHolderRuleShapes(g)

	require.Error(t, err)
	assert.Nil(t, shapes)
	assert.Contains(t, err.Error(), policyent.PolicyKindCommand)
}

func TestAddAndRemovePolicyShapeRef(t *testing.T) {
	g := &group_entity.Group{ID: 2, Name: "prod"}

	require.NoError(t, AddPolicyShapeRef(g, policyent.PolicyKindQuery, "builtin:readonly"))
	refs, err := PolicyShapeRefs(g, policyent.PolicyKindQuery)
	require.NoError(t, err)
	assert.Equal(t, []string{"builtin:readonly"}, refs)

	require.NoError(t, RemovePolicyShapeRef(g, policyent.PolicyKindQuery, "builtin:readonly"))
	refs, err = PolicyShapeRefs(g, policyent.PolicyKindQuery)
	require.NoError(t, err)
	assert.Empty(t, refs)
}

// 策略 kind → canonical 资产类型由注册表派生，调用方不维护镜像表。
func TestCanonicalForPolicyKind(t *testing.T) {
	for kind, want := range map[string]string{
		policyent.PolicyKindCommand: asset_entity.AssetTypeSSH,
		policyent.PolicyKindQuery:   asset_entity.AssetTypeDatabase,
		policyent.PolicyKindRedis:   asset_entity.AssetTypeRedis,
		policyent.PolicyKindMongo:   asset_entity.AssetTypeMongoDB,
		policyent.PolicyKindKafka:   asset_entity.AssetTypeKafka,
		policyent.PolicyKindK8s:     asset_entity.AssetTypeK8s,
		policyent.PolicyKindEtcd:    asset_entity.AssetTypeEtcd,
		policyent.PolicyKindOSS:     asset_entity.AssetTypeOSS,
	} {
		canon, ok := CanonicalForPolicyKind(kind)
		require.True(t, ok, kind)
		assert.Equal(t, want, canon, kind)
	}

	_, ok := CanonicalForPolicyKind("no-such-kind")
	assert.False(t, ok)
}
