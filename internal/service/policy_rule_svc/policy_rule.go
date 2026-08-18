package policy_rule_svc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/repository/policy_rule_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/group_svc"
	"github.com/opskat/opskat/internal/service/policy_group_svc"
)

type Target struct {
	Asset     *asset_entity.Asset
	Group     *group_entity.Group
	Canonical string
	Patterns  []string
}

func (t Target) Holder() policyent.Holder {
	switch {
	case t.Asset != nil:
		return t.Asset
	case t.Group != nil:
		return t.Group
	default:
		return nil
	}
}

type GroupRef struct{ ID, Name, PolicyType string }

type ShadowedError struct {
	Target int
	Deny   permission.SourcedRule
}

func (e *ShadowedError) Error() string {
	return fmt.Sprintf("allow rule is shadowed by deny %q", e.Deny.Rule)
}

// GroupRefReason 是目标无法挂载权限组的原因类别，CLI 按它渲染本地化文案。
type GroupRefReason uint8

const (
	GroupRefReasonNoColumn     GroupRefReason = iota // 资产类型没有可挂权限组的策略列
	GroupRefReasonTypeMismatch                       // 权限组类型与资产类型不匹配
	GroupRefReasonNoShape                            // 组目标：权限组类型没有可挂载的规则形状
)

// PolicyGroupRefError 描述目标无法挂载某权限组的前置失败（决策 21）。业务判定在
// 服务层；Error() 保持英文供程序消费，CLI 按 Reason 渲染本地化消息（决策 22）。
type PolicyGroupRefError struct {
	Target Target
	Ref    GroupRef
	Reason GroupRefReason
}

func (e *PolicyGroupRefError) Error() string {
	switch e.Reason {
	case GroupRefReasonTypeMismatch:
		return fmt.Sprintf("refusing to attach: policy group %s (%s) has type %s but asset %s has type %s - its rules would never take effect", e.Ref.Name, e.Ref.ID, e.Ref.PolicyType, e.Target.Asset.Name, e.Target.Asset.Type)
	case GroupRefReasonNoShape:
		return fmt.Sprintf("policy group %s has type %q, which has no rule shape opsctl can attach", e.Ref.ID, e.Ref.PolicyType)
	default:
		return fmt.Sprintf("asset %s has type %s, which has no policy column a group can be attached to", e.Target.Asset.Name, e.Target.Asset.Type)
	}
}

// GroupRefStateError 描述 attach/detach 前置校验失败：权限组当前已挂或未挂。
// CLI 用它渲染本地化消息；判定本身只在服务层。
type GroupRefStateError struct {
	Ref      GroupRef
	Attach   bool
	Attached bool
}

func (e *GroupRefStateError) Error() string {
	if e.Attach {
		return fmt.Sprintf("policy group %s (%s) is already attached", e.Ref.Name, e.Ref.ID)
	}
	return fmt.Sprintf("policy group %s (%s) is not attached", e.Ref.Name, e.Ref.ID)
}

// PlannedRules 是提交前对一个写入目标的计划：确切会落的规则，以及 allow 侧被生效
// deny 遮蔽时点名的遮蔽方。CLI 只渲染计划与确认，不重复实现落点/遮蔽判定。
type PlannedRules struct {
	Target Target
	Landed []permission.LandedRule
	Shadow *permission.SourcedRule
}

type PolicyRuleSvc interface {
	FindAsset(context.Context, int64) (*asset_entity.Asset, error)
	FindGroup(context.Context, int64) (*group_entity.Group, error)
	PlanRules(context.Context, permission.RuleSide, []Target) ([]PlannedRules, error)
	AppendRules(context.Context, permission.RuleSide, []Target) error
	RemoveTypeRule(context.Context, Target, string, permission.RuleSide, string) error
	RemoveShapeRule(context.Context, Target, string, permission.RuleSide, string) error
	PlanPolicyGroupRules(context.Context, int64, string, permission.RuleSide, []string) ([]permission.LandedRule, *permission.SourcedRule, error)
	AppendPolicyGroupRules(context.Context, int64, string, permission.RuleSide, []string) error
	RemovePolicyGroupRule(context.Context, int64, string, permission.RuleSide, string) error
	ValidateGroupRefs(context.Context, Target, []GroupRef, bool) error
	UpdateGroupRefs(context.Context, Target, []GroupRef, bool) error
	ActiveGrantItems(context.Context, string) ([]*grant_entity.GrantItem, *grant_entity.GrantSession, error)
	RemoveGrantItem(context.Context, string, int64) (string, error)
}

func (s *policyRuleSvc) AppendPolicyGroupRules(ctx context.Context, id int64, canonical string, side permission.RuleSide, patterns []string) error {
	repo := policy_rule_repo.PolicyRule()
	return repo.WithTransaction(ctx, func(txCtx context.Context) error {
		pg, err := repo.FindPolicyGroup(txCtx, id)
		if err != nil {
			return err
		}
		_, shadow, err := planPolicyGroupHolder(pg, canonical, side, patterns)
		if err != nil {
			return err
		}
		if shadow != nil {
			return fmt.Errorf("allow rule is shadowed by deny %q", shadow.Rule)
		}
		// 走权限组域服务：与 create/copy 相同的不变式（Validate + Updatetime 刷新）。
		return policy_group_svc.PolicyGroup().Update(txCtx, pg)
	})
}

func (s *policyRuleSvc) RemovePolicyGroupRule(ctx context.Context, id int64, canonical string, side permission.RuleSide, rule string) error {
	repo := policy_rule_repo.PolicyRule()
	return repo.WithTransaction(ctx, func(txCtx context.Context) error {
		pg, err := repo.FindPolicyGroup(txCtx, id)
		if err != nil {
			return err
		}
		if err := permission.RemoveTypeRule(permission.NewPolicyGroupHolder(pg), canonical, side, rule); err != nil {
			return err
		}
		return policy_group_svc.PolicyGroup().Update(txCtx, pg)
	})
}

type policyRuleSvc struct{}

var defaultSvc PolicyRuleSvc = &policyRuleSvc{}

func PolicyRule() PolicyRuleSvc          { return defaultSvc }
func RegisterPolicyRule(s PolicyRuleSvc) { defaultSvc = s }

func (s *policyRuleSvc) FindAsset(ctx context.Context, id int64) (*asset_entity.Asset, error) {
	return asset_svc.Asset().Get(ctx, id)
}
func (s *policyRuleSvc) FindGroup(ctx context.Context, id int64) (*group_entity.Group, error) {
	return group_svc.Group().Get(ctx, id)
}

func freshTarget(ctx context.Context, t Target) (policyent.Holder, func(context.Context, policyent.Holder) error, error) {
	repo := policy_rule_repo.PolicyRule()
	if t.Asset != nil {
		a, err := repo.FindAsset(ctx, t.Asset.ID)
		if err != nil {
			return nil, nil, err
		}
		return a, func(c context.Context, h policyent.Holder) error {
			return repo.UpdateAsset(c, h.(*asset_entity.Asset))
		}, nil
	}
	if t.Group != nil {
		g, err := repo.FindGroup(ctx, t.Group.ID)
		if err != nil {
			return nil, nil, err
		}
		return g, func(c context.Context, h policyent.Holder) error {
			return repo.UpdateGroup(c, h.(*group_entity.Group))
		}, nil
	}
	return nil, nil, fmt.Errorf("policy target has neither asset nor group")
}

// shadowForLanded 收集 holder 的生效规则视图，返回第一条会遮蔽 landed 的 deny。
// 资产与组目标共用；组目标用 CollectHolderTypeRules（自身列 + 引用权限组）。
func shadowForLanded(ctx context.Context, holder policyent.Holder, canonical string, landed []permission.LandedRule) (*permission.SourcedRule, error) {
	var view *permission.TypeRuleView
	var err error
	if a, ok := holder.(*asset_entity.Asset); ok {
		view, err = permission.CollectTypeRules(ctx, a, canonical)
	} else {
		view, err = permission.CollectHolderTypeRules(ctx, holder.(*group_entity.Group), canonical)
	}
	if err != nil {
		return nil, err
	}
	for _, l := range landed {
		if sh := permission.ShadowingDeny(view, canonical, l.Rule); sh != nil {
			return sh, nil
		}
	}
	return nil, nil
}

// PlanRules 在内存 holder 上落 pattern 并做遮蔽检测，产出提交前的计划。只读不落库；
// AppendRules 在事务内对最新状态重放同一业务并持久化。
func (s *policyRuleSvc) PlanRules(ctx context.Context, side permission.RuleSide, targets []Target) ([]PlannedRules, error) {
	plans := make([]PlannedRules, 0, len(targets))
	for _, target := range targets {
		holder := target.Holder()
		if holder == nil {
			return nil, fmt.Errorf("policy target has neither asset nor group")
		}
		landed, err := permission.AppendTypeRules(holder, target.Canonical, side, target.Patterns)
		if err != nil {
			return nil, err
		}
		plan := PlannedRules{Target: target, Landed: landed}
		if side == permission.RuleAllow {
			plan.Shadow, err = shadowForLanded(ctx, holder, target.Canonical, landed)
			if err != nil {
				return nil, err
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (s *policyRuleSvc) AppendRules(ctx context.Context, side permission.RuleSide, targets []Target) error {
	repo := policy_rule_repo.PolicyRule()
	return repo.WithTransaction(ctx, func(txCtx context.Context) error {
		for i, target := range targets {
			holder, save, err := freshTarget(txCtx, target)
			if err != nil {
				return err
			}
			landed, err := permission.AppendTypeRules(holder, target.Canonical, side, target.Patterns)
			if err != nil {
				return err
			}
			if side == permission.RuleAllow {
				sh, err := shadowForLanded(txCtx, holder, target.Canonical, landed)
				if err != nil {
					return err
				}
				if sh != nil {
					return &ShadowedError{Target: i, Deny: *sh}
				}
			}
			if err := save(txCtx, holder); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *policyRuleSvc) RemoveTypeRule(ctx context.Context, target Target, canonical string, side permission.RuleSide, rule string) error {
	return s.remove(ctx, target, func(h policyent.Holder) error { return permission.RemoveTypeRule(h, canonical, side, rule) })
}
func (s *policyRuleSvc) RemoveShapeRule(ctx context.Context, target Target, shape string, side permission.RuleSide, rule string) error {
	return s.remove(ctx, target, func(h policyent.Holder) error { return permission.RemoveShapeRule(h, shape, side, rule) })
}
func (s *policyRuleSvc) remove(ctx context.Context, target Target, fn func(policyent.Holder) error) error {
	repo := policy_rule_repo.PolicyRule()
	return repo.WithTransaction(ctx, func(txCtx context.Context) error {
		h, save, err := freshTarget(txCtx, target)
		if err != nil {
			return err
		}
		if err := fn(h); err != nil {
			return err
		}
		return save(txCtx, h)
	})
}

// planPolicyGroupHolder 在权限组 holder 上落 pattern 并做组内遮蔽检测；Append 侧
// 用它做提交前重放，Plan 侧用它做回显计划，落点业务只实现一份。遮蔽来源带权限组
// 元数据（决策 22），供 CLI 渲染"来源"；与 Collect*Rules 的 SourcedRule 一致。
func planPolicyGroupHolder(pg *policy_group_entity.PolicyGroup, canonical string, side permission.RuleSide, patterns []string) ([]permission.LandedRule, *permission.SourcedRule, error) {
	holder := permission.NewPolicyGroupHolder(pg)
	landed, err := permission.AppendTypeRules(holder, canonical, side, patterns)
	if err != nil {
		return nil, nil, err
	}
	if side != permission.RuleAllow {
		return landed, nil, nil
	}
	allow, deny, err := permission.HolderOwnTypeRules(holder, canonical)
	if err != nil {
		return nil, nil, err
	}
	id := strconv.FormatInt(pg.ID, 10)
	if pg.BuiltinID != "" {
		id = pg.BuiltinID
	}
	src := func(rule string) permission.SourcedRule {
		return permission.SourcedRule{
			Rule:            rule,
			Kind:            permission.RuleSourcePolicyGroup,
			PolicyGroupID:   id,
			PolicyGroupName: pg.Name,
		}
	}
	view := &permission.TypeRuleView{}
	for _, r := range allow {
		view.Allow = append(view.Allow, src(r))
	}
	for _, r := range deny {
		view.Deny = append(view.Deny, src(r))
	}
	for _, l := range landed {
		if sh := permission.ShadowingDeny(view, canonical, l.Rule); sh != nil {
			return landed, sh, nil
		}
	}
	return landed, nil, nil
}

func (s *policyRuleSvc) PlanPolicyGroupRules(ctx context.Context, id int64, canonical string, side permission.RuleSide, patterns []string) ([]permission.LandedRule, *permission.SourcedRule, error) {
	pg, err := policy_rule_repo.PolicyRule().FindPolicyGroup(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return planPolicyGroupHolder(pg, canonical, side, patterns)
}

func refColumn(target Target, ref GroupRef) (string, error) {
	if target.Asset != nil {
		column, accepted, ok := permission.PolicyShapeForType(target.Asset.Type)
		if !ok {
			return "", &PolicyGroupRefError{Target: target, Ref: ref, Reason: GroupRefReasonNoColumn}
		}
		if ref.PolicyType != accepted {
			return "", &PolicyGroupRefError{Target: target, Ref: ref, Reason: GroupRefReasonTypeMismatch}
		}
		return column, nil
	}
	if target.Group == nil {
		return "", fmt.Errorf("policy target has neither asset nor group")
	}
	if _, ok := permission.CanonicalForPolicyKind(ref.PolicyType); !ok {
		return "", &PolicyGroupRefError{Target: target, Ref: ref, Reason: GroupRefReasonNoShape}
	}
	return ref.PolicyType, nil
}
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
func validateRefs(holder policyent.Holder, target Target, refs []GroupRef, attach bool) error {
	for _, ref := range refs {
		column, err := refColumn(target, ref)
		if err != nil {
			return err
		}
		current, err := permission.PolicyShapeRefs(holder, column)
		if err != nil {
			return err
		}
		if attach && contains(current, ref.ID) {
			return &GroupRefStateError{Ref: ref, Attach: true, Attached: true}
		}
		if !attach && !contains(current, ref.ID) {
			return &GroupRefStateError{Ref: ref, Attach: false, Attached: false}
		}
	}
	return nil
}

func uniqueRefs(target Target, refs []GroupRef) ([]GroupRef, error) {
	unique := make([]GroupRef, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		column, err := refColumn(target, ref)
		if err != nil {
			return nil, err
		}
		key := column + "\x00" + ref.ID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, ref)
	}
	return unique, nil
}

func (s *policyRuleSvc) ValidateGroupRefs(_ context.Context, target Target, refs []GroupRef, attach bool) error {
	return validateRefs(target.Holder(), target, refs, attach)
}
func (s *policyRuleSvc) UpdateGroupRefs(ctx context.Context, target Target, refs []GroupRef, attach bool) error {
	repo := policy_rule_repo.PolicyRule()
	return repo.WithTransaction(ctx, func(txCtx context.Context) error {
		h, save, err := freshTarget(txCtx, target)
		if err != nil {
			return err
		}
		fresh := target
		if a, ok := h.(*asset_entity.Asset); ok {
			fresh.Asset = a
		} else {
			fresh.Group = h.(*group_entity.Group)
		}
		refs, err = uniqueRefs(fresh, refs)
		if err != nil {
			return err
		}
		if err := validateRefs(h, fresh, refs, attach); err != nil {
			return err
		}
		for _, ref := range refs {
			column, _ := refColumn(fresh, ref)
			var err error
			if attach {
				err = permission.AddPolicyShapeRef(h, column, ref.ID)
			} else {
				err = permission.RemovePolicyShapeRef(h, column, ref.ID)
			}
			if err != nil {
				return err
			}
		}
		return save(txCtx, h)
	})
}

func (s *policyRuleSvc) ActiveGrantItems(ctx context.Context, session string) ([]*grant_entity.GrantItem, *grant_entity.GrantSession, error) {
	repo := policy_rule_repo.PolicyRule()
	items, err := repo.ListApprovedGrantItems(ctx, session)
	if err != nil {
		return nil, nil, err
	}
	sess, err := repo.GetGrantSession(ctx, session)
	return items, sess, err
}
func (s *policyRuleSvc) RemoveGrantItem(ctx context.Context, session string, itemID int64) (string, error) {
	repo := policy_rule_repo.PolicyRule()
	items, err := repo.ListApprovedGrantItems(ctx, session)
	if err != nil {
		return "", err
	}
	remaining := make([]*grant_entity.GrantItem, 0, len(items))
	removed := ""
	for _, item := range items {
		if item.ID == itemID {
			removed = item.Command
		} else {
			remaining = append(remaining, item)
		}
	}
	if removed == "" {
		return "", fmt.Errorf("grant item %d not found in the active session", itemID)
	}
	err = repo.WithTransaction(ctx, func(txCtx context.Context) error { return repo.UpdateGrantItems(txCtx, session, remaining) })
	return removed, err
}
