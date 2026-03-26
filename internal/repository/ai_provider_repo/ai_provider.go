package ai_provider_repo

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"

	"github.com/cago-frame/cago/database/db"
)

type AIProviderRepo interface {
	List(ctx context.Context) ([]*ai_provider_entity.AIProvider, error)
	Find(ctx context.Context, id int64) (*ai_provider_entity.AIProvider, error)
	GetActive(ctx context.Context) (*ai_provider_entity.AIProvider, error)
	Create(ctx context.Context, p *ai_provider_entity.AIProvider) error
	Update(ctx context.Context, p *ai_provider_entity.AIProvider) error
	Delete(ctx context.Context, id int64) error
	ClearActive(ctx context.Context) error
}

var defaultAIProvider AIProviderRepo

func AIProvider() AIProviderRepo {
	return defaultAIProvider
}

func RegisterAIProvider(i AIProviderRepo) {
	defaultAIProvider = i
}

type aiProviderRepo struct{}

func NewAIProvider() AIProviderRepo {
	return &aiProviderRepo{}
}

func (r *aiProviderRepo) List(ctx context.Context) ([]*ai_provider_entity.AIProvider, error) {
	var list []*ai_provider_entity.AIProvider
	if err := db.Ctx(ctx).Order("createtime DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *aiProviderRepo) Find(ctx context.Context, id int64) (*ai_provider_entity.AIProvider, error) {
	var p ai_provider_entity.AIProvider
	if err := db.Ctx(ctx).Where("id = ?", id).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *aiProviderRepo) GetActive(ctx context.Context) (*ai_provider_entity.AIProvider, error) {
	var p ai_provider_entity.AIProvider
	if err := db.Ctx(ctx).Where("is_active = ?", true).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *aiProviderRepo) Create(ctx context.Context, p *ai_provider_entity.AIProvider) error {
	return db.Ctx(ctx).Create(p).Error
}

func (r *aiProviderRepo) Update(ctx context.Context, p *ai_provider_entity.AIProvider) error {
	return db.Ctx(ctx).Save(p).Error
}

func (r *aiProviderRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Where("id = ?", id).Delete(&ai_provider_entity.AIProvider{}).Error
}

func (r *aiProviderRepo) ClearActive(ctx context.Context) error {
	return db.Ctx(ctx).Model(&ai_provider_entity.AIProvider{}).
		Where("is_active = ?", true).
		Update("is_active", false).Error
}
