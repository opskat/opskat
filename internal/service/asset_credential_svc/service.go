package asset_credential_svc

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/credential_mgr_svc"
)

// Service owns credential-related asset projections used by the IPC layer.
// AssetUsage is the safe asset projection returned by credential queries.
type AssetUsage struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type Service interface {
	ResolvePassword(ctx context.Context, assetID int64) (string, error)
	UsageAssets(ctx context.Context, credentialID int64) ([]AssetUsage, error)
	UsageAssetNames(ctx context.Context, credentialID int64) ([]string, error)
}

type service struct{}

var defaultService Service = &service{}

// Default returns the asset credential service.
func Default() Service {
	return defaultService
}

func init() {
	credential_mgr_svc.SetReferencingAssetsLookup(referencingAssetIDs)
}

// referencingAssetIDs 供 credential_mgr_svc 在凭证认证内容变更后失效相关连接。
func referencingAssetIDs(ctx context.Context, credentialID int64) ([]int64, error) {
	usages, err := Default().UsageAssets(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(usages))
	for i, u := range usages {
		ids[i] = u.ID
	}
	return ids, nil
}

func (s *service) ResolvePassword(ctx context.Context, assetID int64) (string, error) {
	asset, err := asset_repo.Asset().Find(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("asset not found: %w", err)
	}
	h, ok := assettype.Get(asset.Type)
	if !ok {
		return "", fmt.Errorf("unsupported asset type: %s", asset.Type)
	}
	return h.ResolvePassword(ctx, asset)
}

func (s *service) UsageAssets(ctx context.Context, credentialID int64) ([]AssetUsage, error) {
	assets, err := asset_repo.Asset().FindByCredentialID(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	usages := make([]AssetUsage, len(assets))
	for i, asset := range assets {
		usages[i] = AssetUsage{ID: asset.ID, Name: asset.Name, Type: asset.Type}
	}
	return usages, nil
}

func (s *service) UsageAssetNames(ctx context.Context, credentialID int64) ([]string, error) {
	assets, err := asset_repo.Asset().FindByCredentialID(ctx, credentialID)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(assets))
	for i, asset := range assets {
		names[i] = asset.Name
	}
	return names, nil
}
