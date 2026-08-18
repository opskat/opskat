package policy_rule_repo

import (
	"context"
	"errors"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/grant_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/pkg/dbutil"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/grant_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/policy_group_repo"
)

// PolicyRuleRepo owns the persistence seam used by policy rule services. Its
// default implementation delegates to the domain repositories so callers do
// not need to coordinate several repository getters or the transaction helper.
type PolicyRuleRepo interface {
	WithTransaction(context.Context, func(context.Context) error) error

	FindAsset(context.Context, int64) (*asset_entity.Asset, error)
	UpdateAsset(context.Context, *asset_entity.Asset) error
	FindGroup(context.Context, int64) (*group_entity.Group, error)
	UpdateGroup(context.Context, *group_entity.Group) error
	FindPolicyGroup(context.Context, int64) (*policy_group_entity.PolicyGroup, error)

	ListApprovedGrantItems(context.Context, string) ([]*grant_entity.GrantItem, error)
	GetGrantSession(context.Context, string) (*grant_entity.GrantSession, error)
	UpdateGrantItems(context.Context, string, []*grant_entity.GrantItem) error
}

var defaultPolicyRule PolicyRuleRepo = NewPolicyRule()

// PolicyRule returns the registered policy rule repository.
func PolicyRule() PolicyRuleRepo {
	return defaultPolicyRule
}

// RegisterPolicyRule replaces the policy rule repository implementation.
func RegisterPolicyRule(repo PolicyRuleRepo) {
	defaultPolicyRule = repo
}

// NewPolicyRule creates the default delegating implementation.
func NewPolicyRule() PolicyRuleRepo {
	return &policyRuleRepo{}
}

type policyRuleRepo struct{}

func (r *policyRuleRepo) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	return dbutil.WithTransaction(ctx, fn)
}

func (r *policyRuleRepo) FindAsset(ctx context.Context, id int64) (*asset_entity.Asset, error) {
	return asset_repo.Asset().Find(ctx, id)
}

func (r *policyRuleRepo) UpdateAsset(ctx context.Context, asset *asset_entity.Asset) error {
	return asset_repo.Asset().Update(ctx, asset)
}

func (r *policyRuleRepo) FindGroup(ctx context.Context, id int64) (*group_entity.Group, error) {
	return group_repo.Group().Find(ctx, id)
}

func (r *policyRuleRepo) UpdateGroup(ctx context.Context, group *group_entity.Group) error {
	return group_repo.Group().Update(ctx, group)
}

func (r *policyRuleRepo) FindPolicyGroup(ctx context.Context, id int64) (*policy_group_entity.PolicyGroup, error) {
	return policy_group_repo.PolicyGroup().Find(ctx, id)
}

func (r *policyRuleRepo) ListApprovedGrantItems(ctx context.Context, sessionID string) ([]*grant_entity.GrantItem, error) {
	repo := grant_repo.Grant()
	if repo == nil {
		return nil, errors.New("grant repository unavailable")
	}
	return repo.ListApprovedItems(ctx, sessionID)
}

func (r *policyRuleRepo) GetGrantSession(ctx context.Context, sessionID string) (*grant_entity.GrantSession, error) {
	repo := grant_repo.Grant()
	if repo == nil {
		return nil, errors.New("grant repository unavailable")
	}
	return repo.GetSession(ctx, sessionID)
}

func (r *policyRuleRepo) UpdateGrantItems(ctx context.Context, sessionID string, items []*grant_entity.GrantItem) error {
	repo := grant_repo.Grant()
	if repo == nil {
		return errors.New("grant repository unavailable")
	}
	return repo.UpdateItems(ctx, sessionID, items)
}
