package extension_svc

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// HostAssetConfig is the narrow asset view exposed to the extension host.
type HostAssetConfig struct {
	Name   string
	Type   string
	Config string
}

// GetHostAssetConfig returns only the asset fields available to host config handling.
func (s *Service) GetHostAssetConfig(ctx context.Context, assetID int64) (*HostAssetConfig, error) {
	asset, err := s.assetRepo.Find(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("find extension host asset %d: %w", assetID, err)
	}
	return &HostAssetConfig{Name: asset.Name, Type: asset.Type, Config: asset.Config}, nil
}

// GetHostKV reads extension-scoped host data. A missing key is represented as a nil value.
func (s *Service) GetHostKV(ctx context.Context, extName, key string) ([]byte, error) {
	value, err := s.dataRepo.Get(ctx, extName, key)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get extension host KV: %w", err)
	}
	return value, nil
}

// SetHostKV writes extension-scoped host data.
func (s *Service) SetHostKV(ctx context.Context, extName, key string, value []byte) error {
	if err := s.dataRepo.Set(ctx, extName, key, value); err != nil {
		return fmt.Errorf("set extension host KV: %w", err)
	}
	return nil
}
