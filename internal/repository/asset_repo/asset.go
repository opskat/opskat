package asset_repo

import (
	"context"

	"ops-cat/internal/model/entity/asset_entity"

	"github.com/cago-frame/cago/database/db"
)

// AssetRepo 资产数据访问接口
type AssetRepo interface {
	Find(ctx context.Context, id int64) (*asset_entity.Asset, error)
	List(ctx context.Context, opts ListOptions) ([]*asset_entity.Asset, error)
	Create(ctx context.Context, asset *asset_entity.Asset) error
	Update(ctx context.Context, asset *asset_entity.Asset) error
	Delete(ctx context.Context, id int64) error
}

// ListOptions 列表查询选项
type ListOptions struct {
	Type    string
	GroupID int64
}

var defaultAsset AssetRepo

// Asset 获取AssetRepo实例
func Asset() AssetRepo {
	return defaultAsset
}

// RegisterAsset 注册AssetRepo实现
func RegisterAsset(i AssetRepo) {
	defaultAsset = i
}

// assetRepo 默认实现
type assetRepo struct{}

// NewAsset 创建默认实现
func NewAsset() AssetRepo {
	return &assetRepo{}
}

func (r *assetRepo) Find(ctx context.Context, id int64) (*asset_entity.Asset, error) {
	var asset asset_entity.Asset
	if err := db.Ctx(ctx).Where("id = ? AND status = ?", id, asset_entity.StatusActive).First(&asset).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *assetRepo) List(ctx context.Context, opts ListOptions) ([]*asset_entity.Asset, error) {
	var assets []*asset_entity.Asset
	query := db.Ctx(ctx).Where("status = ?", asset_entity.StatusActive)
	if opts.Type != "" {
		query = query.Where("type = ?", opts.Type)
	}
	if opts.GroupID > 0 {
		query = query.Where("group_id = ?", opts.GroupID)
	}
	if err := query.Order("sort_order ASC, id ASC").Find(&assets).Error; err != nil {
		return nil, err
	}
	return assets, nil
}

func (r *assetRepo) Create(ctx context.Context, asset *asset_entity.Asset) error {
	return db.Ctx(ctx).Create(asset).Error
}

func (r *assetRepo) Update(ctx context.Context, asset *asset_entity.Asset) error {
	return db.Ctx(ctx).Save(asset).Error
}

func (r *assetRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&asset_entity.Asset{}).Where("id = ?", id).
		Update("status", asset_entity.StatusDeleted).Error
}
