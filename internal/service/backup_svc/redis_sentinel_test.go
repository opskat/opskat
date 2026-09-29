package backup_svc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
)

// TestRedisSentinelBackupIncludeCredentialsDecryptsSentinelPassword 钉住哨兵密码与现有
// Redis 数据节点密码同等处理：IncludeCredentials 时明文导出（供人类可读的凭据一并导出）。
func TestRedisSentinelBackupIncludeCredentialsDecryptsSentinelPassword(t *testing.T) {
	crypto := taggedCredentialCrypto{tag: "source:"}
	ctx := setupBackupTest(t)
	asset := &asset_entity.Asset{
		Name: "redis-sentinel", Type: asset_entity.AssetTypeRedis, Status: asset_entity.StatusActive,
	}
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{
		Mode: asset_entity.RedisModeSentinel, Nodes: []string{"10.0.0.1:26379"}, MasterName: "mymaster",
		SentinelUsername: "sentuser", SentinelPassword: crypto.tag + "sentinel-plain",
	}))
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))

	data, err := Export(ctx, &ExportOptions{IncludeCredentials: true}, crypto)
	require.NoError(t, err)
	require.Len(t, data.Assets, 1)
	cfg, err := data.Assets[0].GetRedisConfig()
	require.NoError(t, err)
	require.Equal(t, "sentinel-plain", cfg.SentinelPassword)
}

// TestRedisSentinelBackupStripsSentinelPasswordWithoutCredentials 钉住不含凭据的导出会剥离
// 哨兵密码，与现有 Redis 数据节点密码同等处理，避免明文/密文泄漏到不含凭据的备份里。
func TestRedisSentinelBackupStripsSentinelPasswordWithoutCredentials(t *testing.T) {
	crypto := taggedCredentialCrypto{tag: "source:"}
	ctx := setupBackupTest(t)
	asset := &asset_entity.Asset{
		Name: "redis-sentinel", Type: asset_entity.AssetTypeRedis, Status: asset_entity.StatusActive,
	}
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{
		Mode: asset_entity.RedisModeSentinel, Nodes: []string{"10.0.0.1:26379"}, MasterName: "mymaster",
		SentinelPassword: crypto.tag + "sentinel-plain",
	}))
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))

	data, err := Export(ctx, &ExportOptions{}, nil)
	require.NoError(t, err)
	require.Len(t, data.Assets, 1)
	cfg, err := data.Assets[0].GetRedisConfig()
	require.NoError(t, err)
	require.Empty(t, cfg.SentinelPassword)
}

// TestRedisSentinelBackupImportReencryptsSentinelPassword 钉住导入时用目标环境的密钥重新
// 加密哨兵密码（与 Redis 数据节点密码同等处理），而不是原样落盘源环境密文或明文。
func TestRedisSentinelBackupImportReencryptsSentinelPassword(t *testing.T) {
	sourceCrypto := taggedCredentialCrypto{tag: "source:"}
	ctx1 := setupBackupTest(t)
	asset := &asset_entity.Asset{
		Name: "redis-sentinel", Type: asset_entity.AssetTypeRedis, Status: asset_entity.StatusActive,
	}
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{
		Mode: asset_entity.RedisModeSentinel, Nodes: []string{"10.0.0.1:26379"}, MasterName: "mymaster",
		SentinelPassword: sourceCrypto.tag + "sentinel-plain",
	}))
	require.NoError(t, asset_repo.Asset().Create(ctx1, asset))

	backup, err := Export(ctx1, &ExportOptions{IncludeCredentials: true}, sourceCrypto)
	require.NoError(t, err)
	serialized, err := json.Marshal(backup)
	require.NoError(t, err)
	var transported BackupData
	require.NoError(t, json.Unmarshal(serialized, &transported))

	destinationCrypto := taggedCredentialCrypto{tag: "destination:"}
	ctx2 := setupBackupTest(t)
	_, err = Import(ctx2, &transported, &ImportOptions{ImportAssets: true, Mode: "merge"}, destinationCrypto)
	require.NoError(t, err)

	restored, err := asset_repo.Asset().List(ctx2, asset_repo.ListOptions{})
	require.NoError(t, err)
	require.Len(t, restored, 1)
	cfg, err := restored[0].GetRedisConfig()
	require.NoError(t, err)
	require.NotEqual(t, "sentinel-plain", cfg.SentinelPassword)
	decrypted, err := destinationCrypto.Decrypt(cfg.SentinelPassword)
	require.NoError(t, err)
	require.Equal(t, "sentinel-plain", decrypted)
}
