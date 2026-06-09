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

// preserveSSHSecretsOnOverwrite 覆盖导入时用旧配置补齐新配置缺失的敏感字段。
//
// 导入源（SSH Config / Tabby / WindTerm）往往不含完整凭据，直接覆盖会清空已有
// 密码 / 统一凭证 / 本地密钥 / passphrase。规则：
//   - 逐字段「新值为空才用旧值补」（Password / PrivateKeyPassphrase / PrivateKeys）；
//   - 当新导入完全不含任何认证材料时（如 WindTerm 只导入 IP/端口/分组），额外保留
//     旧的统一凭证 CredentialID 与 AuthType，避免把可用资产覆盖成无凭据状态；
//   - 新导入自带认证材料（密码/凭证/密钥）时以新数据为准，不复活旧的 CredentialID，
//     以免旧凭证按解析优先级（CredentialID 优先）遮蔽新导入的密码/密钥。
func preserveSSHSecretsOnOverwrite(oldCfg, newCfg *asset_entity.SSHConfig) {
	newHasAuthMaterial := newCfg.Password != "" || newCfg.CredentialID != 0 || len(newCfg.PrivateKeys) > 0

	if newCfg.Password == "" {
		newCfg.Password = oldCfg.Password
	}
	if newCfg.PrivateKeyPassphrase == "" {
		newCfg.PrivateKeyPassphrase = oldCfg.PrivateKeyPassphrase
	}
	if len(newCfg.PrivateKeys) == 0 {
		newCfg.PrivateKeys = oldCfg.PrivateKeys
	}
	if !newHasAuthMaterial {
		newCfg.CredentialID = oldCfg.CredentialID
		newCfg.AuthType = oldCfg.AuthType
	}
}
