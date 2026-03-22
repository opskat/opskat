package asset_svc

import (
	"context"
	"time"

	"ops-cat/internal/model/entity/asset_entity"
	"ops-cat/internal/repository/asset_repo"
)

// AssetSvc 资产业务接口
type AssetSvc interface {
	Get(ctx context.Context, id int64) (*asset_entity.Asset, error)
	List(ctx context.Context, assetType string, groupID int64) ([]*asset_entity.Asset, error)
	Create(ctx context.Context, asset *asset_entity.Asset) error
	Update(ctx context.Context, asset *asset_entity.Asset) error
	Delete(ctx context.Context, id int64) error
}

type assetSvc struct{}

var defaultAsset = &assetSvc{}

// Asset 获取AssetSvc实例
func Asset() AssetSvc {
	return defaultAsset
}

func (s *assetSvc) Get(ctx context.Context, id int64) (*asset_entity.Asset, error) {
	return asset_repo.Asset().Find(ctx, id)
}

func (s *assetSvc) List(ctx context.Context, assetType string, groupID int64) ([]*asset_entity.Asset, error) {
	return asset_repo.Asset().List(ctx, asset_repo.ListOptions{
		Type:    assetType,
		GroupID: groupID,
	})
}

func (s *assetSvc) Create(ctx context.Context, asset *asset_entity.Asset) error {
	if err := asset.Validate(); err != nil {
		return err
	}
	now := time.Now().Unix()
	asset.Createtime = now
	asset.Updatetime = now
	asset.Status = asset_entity.StatusActive
	return asset_repo.Asset().Create(ctx, asset)
}

func (s *assetSvc) Update(ctx context.Context, asset *asset_entity.Asset) error {
	if err := asset.Validate(); err != nil {
		return err
	}
	asset.Updatetime = time.Now().Unix()
	return asset_repo.Asset().Update(ctx, asset)
}

func (s *assetSvc) Delete(ctx context.Context, id int64) error {
	return asset_repo.Asset().Delete(ctx, id)
}
