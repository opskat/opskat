package import_svc

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// sshAssetKey 资产去重键：host:port:username
func sshAssetKey(host string, port int, username string) string {
	return fmt.Sprintf("%s:%d:%s", host, port, username)
}

// listSSHAssets 查询全部 SSH 资产
func listSSHAssets(ctx context.Context) ([]*asset_entity.Asset, error) {
	assets, err := asset_svc.Asset().List(ctx, asset_entity.AssetTypeSSH, 0)
	if err != nil {
		return nil, fmt.Errorf("查询已有资产失败: %w", err)
	}
	return assets, nil
}

// buildSSHAssetMap 按去重键索引资产，跳过配置解析失败的项
func buildSSHAssetMap(assets []*asset_entity.Asset) map[string]*asset_entity.Asset {
	existingMap := make(map[string]*asset_entity.Asset, len(assets))
	for _, asset := range assets {
		sshCfg, err := asset.GetSSHConfig()
		if err != nil {
			continue
		}
		existingMap[sshAssetKey(sshCfg.Host, sshCfg.Port, sshCfg.Username)] = asset
	}
	return existingMap
}

// existingSSHAssetMap 加载并按去重键索引已有 SSH 资产
func existingSSHAssetMap(ctx context.Context) (map[string]*asset_entity.Asset, error) {
	assets, err := listSSHAssets(ctx)
	if err != nil {
		return nil, err
	}
	return buildSSHAssetMap(assets), nil
}

// existingSSHAssetSet 加载已有 SSH 资产的去重键集合
func existingSSHAssetSet(ctx context.Context) (map[string]bool, error) {
	existingMap, err := existingSSHAssetMap(ctx)
	if err != nil {
		return nil, err
	}
	existingSet := make(map[string]bool, len(existingMap))
	for key := range existingMap {
		existingSet[key] = true
	}
	return existingSet, nil
}
