package command_review_repo

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/opskat/opskat/internal/model/entity/command_review_entity"
)

//go:generate mockgen -source=command_review.go -destination=mock_command_review_repo/command_review.go -package=mock_command_review_repo

// CommandReviewRepo 读写模型审核结果的缓存。
type CommandReviewRepo interface {
	// Get 返回 now 时刻仍有效的缓存；找不到或已过期时返回 (nil, nil)。
	Get(ctx context.Context, cacheKey string, now int64) (*command_review_entity.CommandReview, error)
	// Put 写入缓存，同一个 CacheKey 覆盖旧值。
	Put(ctx context.Context, r *command_review_entity.CommandReview) error
}

var defaultCommandReview CommandReviewRepo

// CommandReview 获取 CommandReviewRepo 实例
func CommandReview() CommandReviewRepo {
	return defaultCommandReview
}

// RegisterCommandReview 注册 CommandReviewRepo 实现
func RegisterCommandReview(r CommandReviewRepo) {
	defaultCommandReview = r
}

type commandReviewRepo struct{}

// NewCommandReview 创建默认实现
func NewCommandReview() CommandReviewRepo { return &commandReviewRepo{} }

func (r *commandReviewRepo) Get(ctx context.Context, cacheKey string, now int64) (*command_review_entity.CommandReview, error) {
	var e command_review_entity.CommandReview
	err := db.Ctx(ctx).Where("cache_key = ? AND expiretime > ?", cacheKey, now).First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *commandReviewRepo) Put(ctx context.Context, e *command_review_entity.CommandReview) error {
	return db.Ctx(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "cache_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"result", "createtime", "expiretime"}),
	}).Create(e).Error
}
