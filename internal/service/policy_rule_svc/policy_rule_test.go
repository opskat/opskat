package policy_rule_svc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/repository/policy_group_repo"
	"github.com/opskat/opskat/internal/repository/policy_group_repo/mock_policy_group_repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func transactionContext() context.Context {
	return dbutil.WithTransactionRunner(context.Background(), func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
}

func TestAppendRulesRechecksShadowingDenyInsideTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(repo)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
	fresh := &asset_entity.Asset{ID: 1, Name: "host", Type: asset_entity.AssetTypeSSH}
	require.NoError(t, fresh.SetCommandPolicy(&policyent.CommandPolicy{DenyList: []string{"rm *"}}))
	repo.EXPECT().Find(gomock.Any(), int64(1)).Return(fresh, nil)

	err := PolicyRule().AppendRules(transactionContext(), permission.RuleAllow, []Target{{
		Asset: &asset_entity.Asset{ID: 1}, Canonical: asset_entity.AssetTypeSSH, Patterns: []string{"rm -rf /tmp/x"},
	}})
	var shadowed *ShadowedError
	require.ErrorAs(t, err, &shadowed)
	require.Equal(t, "rm *", shadowed.Deny.Rule)
}

func TestUpdateGroupRefsUsesRegisteredPolicyShape(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(repo)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
	fresh := &asset_entity.Asset{ID: 2, Name: "cluster", Type: asset_entity.AssetTypeK8s}
	repo.EXPECT().Find(gomock.Any(), int64(2)).Return(fresh, nil)
	repo.EXPECT().Update(gomock.Any(), fresh).Return(nil)

	err := PolicyRule().UpdateGroupRefs(transactionContext(), Target{Asset: fresh}, []GroupRef{{ID: "builtin:k8s", Name: "k8s", PolicyType: policyent.PolicyKindCommand}}, true)
	require.NoError(t, err)
	p, err := fresh.GetK8sPolicy()
	require.NoError(t, err)
	require.Equal(t, []string{"builtin:k8s"}, p.Groups)
}

func TestUpdateGroupRefsDoesNotPersistDuplicateAttachInput(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	asset_repo.RegisterAsset(repo)
	fresh := &asset_entity.Asset{ID: 3, Name: "host", Type: asset_entity.AssetTypeSSH}
	repo.EXPECT().Find(gomock.Any(), int64(3)).Return(fresh, nil)
	repo.EXPECT().Update(gomock.Any(), fresh).Return(nil)
	ref := GroupRef{ID: "builtin:linux-readonly", Name: "readonly", PolicyType: policyent.PolicyKindCommand}

	err := PolicyRule().UpdateGroupRefs(transactionContext(), Target{Asset: fresh}, []GroupRef{ref, ref}, true)
	require.NoError(t, err)
	p, err := fresh.GetCommandPolicy()
	require.NoError(t, err)
	require.Equal(t, []string{"builtin:linux-readonly"}, p.Groups)
}

func TestUpdateGroupRefsAcceptsDuplicateDetachInputWithoutLeavingARef(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_asset_repo.NewMockAssetRepo(ctrl)
	asset_repo.RegisterAsset(repo)
	fresh := &asset_entity.Asset{ID: 4, Name: "host", Type: asset_entity.AssetTypeSSH}
	require.NoError(t, fresh.SetCommandPolicy(&policyent.CommandPolicy{Groups: []string{"builtin:linux-readonly"}}))
	repo.EXPECT().Find(gomock.Any(), int64(4)).Return(fresh, nil)
	repo.EXPECT().Update(gomock.Any(), fresh).Return(nil)
	ref := GroupRef{ID: "builtin:linux-readonly", Name: "readonly", PolicyType: policyent.PolicyKindCommand}

	err := PolicyRule().UpdateGroupRefs(transactionContext(), Target{Asset: fresh}, []GroupRef{ref, ref}, false)
	require.NoError(t, err)
	p, err := fresh.GetCommandPolicy()
	require.NoError(t, err)
	require.Empty(t, p.Groups)
}

func TestPlanRulesReturnsLandedWithoutPersistence(t *testing.T) {
	asset := &asset_entity.Asset{ID: 1, Name: "host", Type: asset_entity.AssetTypeSSH}

	plans, err := PolicyRule().PlanRules(context.Background(), permission.RuleAllow, []Target{{
		Asset: asset, Canonical: asset_entity.AssetTypeSSH, Patterns: []string{"uptime", "df -h"},
	}})

	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Len(t, plans[0].Landed, 2)
	assert.Nil(t, plans[0].Shadow)
	p, err := asset.GetCommandPolicy()
	require.NoError(t, err)
	assert.Contains(t, p.AllowList, "uptime")
}

func TestPlanRulesReportsShadowingDeny(t *testing.T) {
	asset := &asset_entity.Asset{ID: 1, Name: "host", Type: asset_entity.AssetTypeSSH}
	require.NoError(t, asset.SetCommandPolicy(&policyent.CommandPolicy{DenyList: []string{"rm *"}}))

	plans, err := PolicyRule().PlanRules(context.Background(), permission.RuleAllow, []Target{{
		Asset: asset, Canonical: asset_entity.AssetTypeSSH, Patterns: []string{"rm -rf /tmp/x"},
	}})

	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.NotNil(t, plans[0].Shadow)
	assert.Equal(t, "rm *", plans[0].Shadow.Rule)
}

func TestPlanPolicyGroupRulesReturnsLandedAndShadow(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_policy_group_repo.NewMockPolicyGroupRepo(ctrl)
	orig := policy_group_repo.PolicyGroup()
	policy_group_repo.RegisterPolicyGroup(repo)
	t.Cleanup(func() { policy_group_repo.RegisterPolicyGroup(orig) })
	pg := &policy_group_entity.PolicyGroup{ID: 5, Name: "ops", PolicyType: policyent.PolicyKindCommand}
	data, err := json.Marshal(&policyent.CommandPolicy{DenyList: []string{"rm *"}})
	require.NoError(t, err)
	pg.Policy = string(data)
	repo.EXPECT().Find(gomock.Any(), int64(5)).Return(pg, nil)

	landed, shadow, err := PolicyRule().PlanPolicyGroupRules(context.Background(), 5, asset_entity.AssetTypeSSH, permission.RuleAllow, []string{"rm -rf /tmp/x"})

	require.NoError(t, err)
	require.Len(t, landed, 1)
	require.NotNil(t, shadow)
	assert.Equal(t, "rm *", shadow.Rule)
	// 遮蔽来源必须带权限组元数据（决策 22）：CLI 的"来源"渲染依赖它。
	assert.Equal(t, permission.RuleSourcePolicyGroup, shadow.Kind)
	assert.Equal(t, "5", shadow.PolicyGroupID)
	assert.Equal(t, "ops", shadow.PolicyGroupName)
}

func TestAppendPolicyGroupRulesGoesThroughDomainServiceInvariants(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_policy_group_repo.NewMockPolicyGroupRepo(ctrl)
	orig := policy_group_repo.PolicyGroup()
	policy_group_repo.RegisterPolicyGroup(repo)
	t.Cleanup(func() { policy_group_repo.RegisterPolicyGroup(orig) })
	pg := &policy_group_entity.PolicyGroup{ID: 5, Name: "ops", PolicyType: policyent.PolicyKindCommand}
	repo.EXPECT().Find(gomock.Any(), int64(5)).Return(pg, nil)
	repo.EXPECT().Update(gomock.Any(), pg).Return(nil)

	err := PolicyRule().AppendPolicyGroupRules(transactionContext(), 5, asset_entity.AssetTypeSSH, permission.RuleAllow, []string{"uptime"})

	require.NoError(t, err)
	// 域服务 Update 的不变式：Validate 通过并刷新 Updatetime。
	assert.NotZero(t, pg.Updatetime)
}

func TestAppendPolicyGroupRulesRejectsInvalidGroupThroughDomainService(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_policy_group_repo.NewMockPolicyGroupRepo(ctrl)
	orig := policy_group_repo.PolicyGroup()
	policy_group_repo.RegisterPolicyGroup(repo)
	t.Cleanup(func() { policy_group_repo.RegisterPolicyGroup(orig) })
	pg := &policy_group_entity.PolicyGroup{ID: 6, Name: "", PolicyType: policyent.PolicyKindCommand}
	repo.EXPECT().Find(gomock.Any(), int64(6)).Return(pg, nil)
	repo.EXPECT().Update(gomock.Any(), gomock.Any()).Times(0)

	err := PolicyRule().AppendPolicyGroupRules(transactionContext(), 6, asset_entity.AssetTypeSSH, permission.RuleAllow, []string{"uptime"})

	require.Error(t, err)
}

func TestRemovePolicyGroupRuleRefreshesUpdatetime(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_policy_group_repo.NewMockPolicyGroupRepo(ctrl)
	orig := policy_group_repo.PolicyGroup()
	policy_group_repo.RegisterPolicyGroup(repo)
	t.Cleanup(func() { policy_group_repo.RegisterPolicyGroup(orig) })
	data, err := json.Marshal(&policyent.CommandPolicy{AllowList: []string{"uptime"}})
	require.NoError(t, err)
	pg := &policy_group_entity.PolicyGroup{ID: 7, Name: "ops", PolicyType: policyent.PolicyKindCommand, Policy: string(data)}
	repo.EXPECT().Find(gomock.Any(), int64(7)).Return(pg, nil)
	repo.EXPECT().Update(gomock.Any(), pg).Return(nil)

	err = PolicyRule().RemovePolicyGroupRule(transactionContext(), 7, asset_entity.AssetTypeSSH, permission.RuleAllow, "uptime")

	require.NoError(t, err)
	assert.NotZero(t, pg.Updatetime)
	p, perr := permission.NewPolicyGroupHolder(pg).GetCommandPolicy()
	require.NoError(t, perr)
	assert.Empty(t, p.AllowList)
}

func TestValidateGroupRefsReportsTypedReasons(t *testing.T) {
	asset := &asset_entity.Asset{ID: 9, Name: "host", Type: asset_entity.AssetTypeSSH}
	require.NoError(t, asset.SetCommandPolicy(&policyent.CommandPolicy{Groups: []string{"g1"}}))

	// 权限组类型与资产类型不匹配。
	err := PolicyRule().ValidateGroupRefs(context.Background(), Target{Asset: asset}, []GroupRef{{ID: "gq", Name: "query-group", PolicyType: policyent.PolicyKindQuery}}, true)
	var refErr *PolicyGroupRefError
	require.ErrorAs(t, err, &refErr)
	require.Equal(t, GroupRefReasonTypeMismatch, refErr.Reason)

	// 已挂载的组再次 attach。
	err = PolicyRule().ValidateGroupRefs(context.Background(), Target{Asset: asset}, []GroupRef{{ID: "g1", Name: "g1", PolicyType: policyent.PolicyKindCommand}}, true)
	var stateErr *GroupRefStateError
	require.ErrorAs(t, err, &stateErr)
	require.True(t, stateErr.Attach)
	require.True(t, stateErr.Attached)

	// 未挂载的组 detach。
	err = PolicyRule().ValidateGroupRefs(context.Background(), Target{Asset: asset}, []GroupRef{{ID: "g2", Name: "g2", PolicyType: policyent.PolicyKindCommand}}, false)
	stateErr = nil
	require.ErrorAs(t, err, &stateErr)
	require.False(t, stateErr.Attach)
	require.False(t, stateErr.Attached)
}
