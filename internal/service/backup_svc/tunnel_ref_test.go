package backup_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cago-frame/cago/database/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	_ "github.com/opskat/opskat/internal/dbdriver/builtin"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/pkg/extension"
)

func seedDummyAssets(t *testing.T, ctx context.Context, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		a := &asset_entity.Asset{
			Name:       fmt.Sprintf("seed-%d", i),
			Type:       asset_entity.AssetTypeSSH,
			Status:     asset_entity.StatusActive,
			Config:     `{"host":"127.0.0.1","port":22,"username":"u","auth_type":"password"}`,
			Createtime: 1,
		}
		require.NoError(t, asset_repo.Asset().Create(ctx, a))
	}
}

func findAssetByName(t *testing.T, ctx context.Context, name string) *asset_entity.Asset {
	t.Helper()
	var a asset_entity.Asset
	require.NoError(t, db.Ctx(ctx).Where("name = ?", name).First(&a).Error)
	return &a
}

// effectiveChainForAsset 提取资产的有效代理链，供 mock 测试核对导入后实际拨号请求的 SSH 资产 ID。
func effectiveChainForAsset(t *testing.T, a *asset_entity.Asset) *asset_entity.ProxyChainConfig {
	t.Helper()
	var parsed struct {
		JumpHostID int64                                   `json:"jump_host_id"`
		SSHAssetID int64                                   `json:"ssh_asset_id"`
		Proxy      *asset_entity.ProxyConfig               `json:"proxy"`
		ProxyChain *asset_entity.ProxyChainConfig          `json:"proxy_chain"`
		ExtConn    *asset_entity.ExtensionConnectionConfig `json:"__opskat_connection"`
	}
	if a.Config != "" {
		require.NoError(t, json.Unmarshal([]byte(a.Config), &parsed))
	}
	chain := parsed.ProxyChain
	if chain == nil && parsed.ExtConn != nil {
		chain = parsed.ExtConn.ProxyChain
	}
	tunnelID := a.SSHTunnelID
	if tunnelID == 0 {
		tunnelID = parsed.SSHAssetID
	}
	if tunnelID == 0 {
		tunnelID = parsed.JumpHostID
	}
	return asset_entity.EffectiveProxyChain(chain, tunnelID, parsed.Proxy)
}

func TestImportRemapsTunnelRefsAcrossAllAssetTypes(t *testing.T) {
	srcCtx := setupBackupTest(t)

	hop1 := &asset_entity.Asset{
		Name:       "hop-1",
		Type:       asset_entity.AssetTypeSSH,
		Status:     asset_entity.StatusActive,
		Config:     `{"host":"10.0.0.1","port":22,"username":"u","auth_type":"password"}`,
		Createtime: 1,
	}
	require.NoError(t, asset_repo.Asset().Create(srcCtx, hop1))

	hop2 := &asset_entity.Asset{
		Name:        "hop-2",
		Type:        asset_entity.AssetTypeSSH,
		Status:      asset_entity.StatusActive,
		SSHTunnelID: hop1.ID,
		Config: fmt.Sprintf(
			`{"host":"10.0.0.2","port":22,"username":"u","auth_type":"password","jump_host_id":%d,`+
				`"proxy_chain":{"layers":[{"id":"legacy-ssh-%d","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
			hop1.ID, hop1.ID, hop1.ID,
		),
		Createtime: 1,
	}
	require.NoError(t, asset_repo.Asset().Create(srcCtx, hop2))

	// 覆盖全部 12 种内置资产类型 + 扩展资产类型
	fixtures := []*asset_entity.Asset{
		{
			Name:        "db-pg",
			Type:        asset_entity.AssetTypeDatabase,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop2.ID,
			Config: fmt.Sprintf(
				`{"driver":"postgresql","host":"127.0.0.1","port":5432,"username":"u","ssh_asset_id":%d,`+
					`"proxy_chain":{"layers":[{"id":"legacy-ssh-%d","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
				hop2.ID, hop2.ID, hop2.ID,
			),
			Createtime: 1,
		},
		{
			Name:        "db-sqlite-remote",
			Type:        asset_entity.AssetTypeDatabase,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop1.ID,
			Config:      fmt.Sprintf(`{"driver":"sqlite","sqlite_source":"remote_ssh_vfs","path":"/data/app.db","ssh_asset_id":%d}`, hop1.ID),
			Createtime:  1,
		},
		{
			Name:        "redis",
			Type:        asset_entity.AssetTypeRedis,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop2.ID,
			Config: fmt.Sprintf(
				`{"host":"127.0.0.1","port":6379,"ssh_asset_id":%d,`+
					`"proxy_chain":{"layers":[{"id":"legacy-ssh-%d","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
				hop2.ID, hop2.ID, hop2.ID,
			),
			Createtime: 1,
		},
		{
			Name:        "mongo",
			Type:        asset_entity.AssetTypeMongoDB,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop1.ID,
			Config: fmt.Sprintf(
				`{"host":"127.0.0.1","port":27017,"ssh_asset_id":%d,`+
					`"proxy_chain":{"layers":[{"id":"legacy-ssh-%d","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
				hop1.ID, hop1.ID, hop1.ID,
			),
			Createtime: 1,
		},
		{
			Name:        "kafka",
			Type:        asset_entity.AssetTypeKafka,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop1.ID,
			Config: fmt.Sprintf(
				`{"brokers":["127.0.0.1:9092"],"ssh_asset_id":%d,`+
					`"proxy_chain":{"layers":[{"id":"legacy-ssh-%d","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
				hop1.ID, hop1.ID, hop1.ID,
			),
			Createtime: 1,
		},
		{
			Name:        "k8s-tunnel-and-chain",
			Type:        asset_entity.AssetTypeK8s,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop1.ID,
			Config: fmt.Sprintf(
				`{"kubeconfig":"enc","proxy_chain":{"layers":[{"id":"ssh-layer","name":"SSH Layer","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
				hop1.ID,
			),
			Createtime: 1,
		},
		{
			Name:        "k8s-column-only",
			Type:        asset_entity.AssetTypeK8s,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop2.ID,
			Config:      `{"kubeconfig":"enc"}`,
			Createtime:  1,
		},
		{
			Name:        "etcd",
			Type:        asset_entity.AssetTypeEtcd,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop1.ID,
			Config: fmt.Sprintf(
				`{"endpoints":["127.0.0.1:2379"],"ssh_asset_id":%d,`+
					`"proxy_chain":{"layers":[{"id":"legacy-ssh-%d","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
				hop1.ID, hop1.ID, hop1.ID,
			),
			Createtime: 1,
		},
		{
			Name:        "rdp",
			Type:        asset_entity.AssetTypeRDP,
			Status:      asset_entity.StatusActive,
			SSHTunnelID: hop1.ID,
			Config: fmt.Sprintf(
				`{"host":"127.0.0.1","port":3389,"username":"admin","ssh_asset_id":%d,`+
					`"proxy_chain":{"layers":[{"id":"legacy-ssh-%d","name":"SSH Tunnel","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
				hop1.ID, hop1.ID, hop1.ID,
			),
			Createtime: 1,
		},
		{
			Name:   "vnc",
			Type:   asset_entity.AssetTypeVNC,
			Status: asset_entity.StatusActive,
			Config: fmt.Sprintf(
				`{"host":"127.0.0.1","port":5900,"file_ssh_asset_id":%d,`+
					`"proxy_chain":{"layers":[{"id":"ssh-1","name":"SSH Layer","enabled":true,"type":"ssh","order":1,"ssh_asset_id":%d}]}}`,
				hop1.ID, hop2.ID,
			),
			Createtime: 1,
		},
		{
			Name:   "oss",
			Type:   asset_entity.AssetTypeOSS,
			Status: asset_entity.StatusActive,
			Config: fmt.Sprintf(
				`{"provider":"s3","endpoint":"https://s3.internal","region":"us-east-1","bucket":"b","access_key_id":"ak",`+
					`"proxy_chain":{"layers":[{"id":"ssh-1","type":"ssh","order":1,"ssh_asset_id":%d},{"id":"ssh-2","type":"ssh","order":2,"ssh_asset_id":%d}]}}`,
				hop1.ID, hop2.ID,
			),
			Createtime: 1,
		},
		{
			Name:       "serial",
			Type:       asset_entity.AssetTypeSerial,
			Status:     asset_entity.StatusActive,
			Config:     `{"port_path":"COM3","baud_rate":115200,"data_bits":8,"stop_bits":"1","parity":"none","flow_control":"none"}`,
			Createtime: 1,
		},
		{
			Name:       "local",
			Type:       asset_entity.AssetTypeLocal,
			Status:     asset_entity.StatusActive,
			Config:     `{"shell":"pwsh","cwd":"/tmp"}`,
			Createtime: 1,
		},
		{
			Name:          "ext-asset",
			Type:          "acme-store",
			ExtensionName: "acme",
			Status:        asset_entity.StatusActive,
			SSHTunnelID:   hop1.ID,
			Config: fmt.Sprintf(
				`{"endpoint":"https://acme.internal",%q:{"proxyChain":{"layers":[{"id":"ssh-1","type":"ssh","order":1,"ssh_asset_id":%d}]}}}`,
				extension.HostConnectionConfigKey, hop2.ID,
			),
			Createtime: 1,
		},
	}
	for _, a := range fixtures {
		require.NoError(t, asset_repo.Asset().Create(srcCtx, a))
	}

	data, err := Export(srcCtx, &ExportOptions{}, nil)
	require.NoError(t, err)
	// 反转备份中的资产顺序（子资产排在跳板机之前），确认回填不依赖资产在备份中的先后顺序。
	for i, j := 0, len(data.Assets)-1; i < j; i, j = i+1, j-1 {
		data.Assets[i], data.Assets[j] = data.Assets[j], data.Assets[i]
	}

	dstCtx := setupBackupTest(t)
	seedDummyAssets(t, dstCtx, 20)
	_, err = Import(dstCtx, data, &ImportOptions{ImportAssets: true, Mode: "replace"}, nil)
	require.NoError(t, err)

	newHop1 := findAssetByName(t, dstCtx, "hop-1")
	newHop2 := findAssetByName(t, dstCtx, "hop-2")
	require.NotEqual(t, hop1.ID, newHop1.ID)
	require.NotEqual(t, hop2.ID, newHop2.ID)

	// 1. 逐类型断言持久化字段已重映射为新 ID，且通过实体 Validate()
	hop2Cfg, err := newHop2.GetSSHConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop1.ID, newHop2.SSHTunnelID)
	assert.Equal(t, newHop1.ID, hop2Cfg.JumpHostID)
	require.NotNil(t, hop2Cfg.ProxyChain)
	assert.Equal(t, newHop1.ID, hop2Cfg.ProxyChain.Layers[0].SSHAssetID)
	assert.Equal(t, fmt.Sprintf("legacy-ssh-%d", newHop1.ID), hop2Cfg.ProxyChain.Layers[0].ID)
	require.NoError(t, newHop2.Validate())

	newDBPG := findAssetByName(t, dstCtx, "db-pg")
	dbPGCfg, err := newDBPG.GetDatabaseConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop2.ID, newDBPG.SSHTunnelID)
	assert.Equal(t, newHop2.ID, dbPGCfg.SSHAssetID)
	require.NotNil(t, dbPGCfg.ProxyChain)
	assert.Equal(t, newHop2.ID, dbPGCfg.ProxyChain.Layers[0].SSHAssetID)
	assert.Equal(t, fmt.Sprintf("legacy-ssh-%d", newHop2.ID), dbPGCfg.ProxyChain.Layers[0].ID)
	require.NoError(t, newDBPG.Validate())

	newDBSQLite := findAssetByName(t, dstCtx, "db-sqlite-remote")
	dbSQLiteCfg, err := newDBSQLite.GetDatabaseConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop1.ID, newDBSQLite.SSHTunnelID)
	assert.Equal(t, newHop1.ID, dbSQLiteCfg.SSHAssetID)
	require.NoError(t, newDBSQLite.Validate())

	newRedis := findAssetByName(t, dstCtx, "redis")
	redisCfg, err := newRedis.GetRedisConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop2.ID, newRedis.SSHTunnelID)
	assert.Equal(t, newHop2.ID, redisCfg.SSHAssetID)
	require.NotNil(t, redisCfg.ProxyChain)
	assert.Equal(t, newHop2.ID, redisCfg.ProxyChain.Layers[0].SSHAssetID)
	assert.Equal(t, fmt.Sprintf("legacy-ssh-%d", newHop2.ID), redisCfg.ProxyChain.Layers[0].ID)
	require.NoError(t, newRedis.Validate())

	newMongo := findAssetByName(t, dstCtx, "mongo")
	mongoCfg, err := newMongo.GetMongoDBConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop1.ID, newMongo.SSHTunnelID)
	assert.Equal(t, newHop1.ID, mongoCfg.SSHAssetID)
	require.NotNil(t, mongoCfg.ProxyChain)
	assert.Equal(t, newHop1.ID, mongoCfg.ProxyChain.Layers[0].SSHAssetID)
	require.NoError(t, newMongo.Validate())

	newKafka := findAssetByName(t, dstCtx, "kafka")
	kafkaCfg, err := newKafka.GetKafkaConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop1.ID, newKafka.SSHTunnelID)
	assert.Equal(t, newHop1.ID, kafkaCfg.SSHAssetID)
	require.NotNil(t, kafkaCfg.ProxyChain)
	assert.Equal(t, newHop1.ID, kafkaCfg.ProxyChain.Layers[0].SSHAssetID)
	require.NoError(t, newKafka.Validate())

	newK8s := findAssetByName(t, dstCtx, "k8s-tunnel-and-chain")
	k8sCfg, err := newK8s.GetK8sConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop1.ID, newK8s.SSHTunnelID)
	require.NotNil(t, k8sCfg.ProxyChain)
	assert.Equal(t, newHop1.ID, k8sCfg.ProxyChain.Layers[0].SSHAssetID)
	require.NoError(t, newK8s.Validate())

	newK8sCol := findAssetByName(t, dstCtx, "k8s-column-only")
	assert.Equal(t, newHop2.ID, newK8sCol.SSHTunnelID)
	require.NoError(t, newK8sCol.Validate())

	newEtcd := findAssetByName(t, dstCtx, "etcd")
	etcdCfg, err := newEtcd.GetEtcdConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop1.ID, newEtcd.SSHTunnelID)
	assert.Equal(t, newHop1.ID, etcdCfg.SSHAssetID)
	require.NotNil(t, etcdCfg.ProxyChain)
	assert.Equal(t, newHop1.ID, etcdCfg.ProxyChain.Layers[0].SSHAssetID)
	require.NoError(t, newEtcd.Validate())

	newRDP := findAssetByName(t, dstCtx, "rdp")
	rdpCfg, err := newRDP.GetRDPConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop1.ID, newRDP.SSHTunnelID)
	assert.Equal(t, newHop1.ID, rdpCfg.SSHAssetID)
	require.NotNil(t, rdpCfg.ProxyChain)
	assert.Equal(t, newHop1.ID, rdpCfg.ProxyChain.Layers[0].SSHAssetID)
	require.NoError(t, newRDP.Validate())

	newVNC := findAssetByName(t, dstCtx, "vnc")
	vncCfg, err := newVNC.GetVNCConfig()
	require.NoError(t, err)
	assert.Equal(t, newHop1.ID, vncCfg.FileSSHAssetID)
	require.NotNil(t, vncCfg.ProxyChain)
	assert.Equal(t, newHop2.ID, vncCfg.ProxyChain.Layers[0].SSHAssetID)
	require.NoError(t, newVNC.Validate())

	newOSS := findAssetByName(t, dstCtx, "oss")
	ossCfg, err := newOSS.GetOSSConfig()
	require.NoError(t, err)
	require.NotNil(t, ossCfg.ProxyChain)
	require.Len(t, ossCfg.ProxyChain.Layers, 2)
	assert.Equal(t, newHop1.ID, ossCfg.ProxyChain.Layers[0].SSHAssetID)
	assert.Equal(t, newHop2.ID, ossCfg.ProxyChain.Layers[1].SSHAssetID)
	require.NoError(t, newOSS.Validate())

	newSerial := findAssetByName(t, dstCtx, "serial")
	assert.Zero(t, newSerial.SSHTunnelID)
	require.NoError(t, newSerial.Validate())

	newLocal := findAssetByName(t, dstCtx, "local")
	assert.Zero(t, newLocal.SSHTunnelID)
	require.NoError(t, newLocal.Validate())

	newExt := findAssetByName(t, dstCtx, "ext-asset")
	assert.Equal(t, newHop1.ID, newExt.SSHTunnelID)
	require.NoError(t, newExt.Validate())

	// 2. 用 MockAssetRepo 验证全部类型的资产在运行时经 ResolveProxyChain 解析跳板路径时，
	// 均按重映射后的新 ID（newHop1.ID / newHop2.ID）查询跳板机，绝不查询旧 ID（1 / 2）。
	restoredAssets := map[string]*asset_entity.Asset{
		"hop-2":                newHop2,
		"db-pg":                newDBPG,
		"db-sqlite-remote":     newDBSQLite,
		"redis":                newRedis,
		"mongo":                newMongo,
		"kafka":                newKafka,
		"k8s-tunnel-and-chain": newK8s,
		"k8s-column-only":      newK8sCol,
		"etcd":                 newEtcd,
		"rdp":                  newRDP,
		"vnc":                  newVNC,
		"oss":                  newOSS,
		"ext-asset":            newExt,
	}

	ctrl := gomock.NewController(t)
	mockRepo := mock_asset_repo.NewMockAssetRepo(ctrl)
	origRepo := asset_repo.Asset()
	asset_repo.RegisterAsset(mockRepo)
	t.Cleanup(func() { asset_repo.RegisterAsset(origRepo) })

	// 凡是查 newHop1.ID，返回 newHop1；凡是查 newHop2.ID，返回 newHop2（其第一跳又会递归查上游 newHop1.ID）。
	// 倘若有任一类型仍携带旧 ID（hop1.ID=1 或 hop2.ID=2），gomock 会直接因 Unexpected call 失败。
	mockRepo.EXPECT().Find(gomock.Any(), newHop1.ID).DoAndReturn(func(_ context.Context, _ int64) (*asset_entity.Asset, error) {
		return newHop1, nil
	}).MinTimes(1)
	mockRepo.EXPECT().Find(gomock.Any(), newHop2.ID).DoAndReturn(func(_ context.Context, _ int64) (*asset_entity.Asset, error) {
		return newHop2, nil
	}).MinTimes(1)

	for name, asset := range restoredAssets {
		t.Run("resolve_proxy_chain_"+name, func(t *testing.T) {
			chain := effectiveChainForAsset(t, asset)
			require.NotNil(t, chain, "asset %s should have an effective proxy chain", name)
			layers, err := credential_resolver.Default().ResolveProxyChain(dstCtx, chain, 5)
			require.NoError(t, err, "asset %s failed to resolve proxy chain", name)
			require.NotEmpty(t, layers, "asset %s resolved empty proxy chain layers", name)
		})
	}
}

func TestImportClearsUnmappedTunnelRefs(t *testing.T) {
	ctx := setupBackupTest(t)
	// 先建一个本地资产占住 ID=1，确认悬空引用（oldID=1）不会误连到目标库的 ID=1。
	seedDummyAssets(t, ctx, 1)

	orphanSSH := &asset_entity.Asset{
		ID:          9,
		Name:        "orphan-ssh",
		Type:        asset_entity.AssetTypeSSH,
		Status:      asset_entity.StatusActive,
		SSHTunnelID: 1,
		Config:      `{"host":"10.0.0.9","port":22,"username":"u","auth_type":"password","jump_host_id":1}`,
		Createtime:  1,
	}
	orphanDB := &asset_entity.Asset{
		ID:          10,
		Name:        "orphan-db",
		Type:        asset_entity.AssetTypeDatabase,
		Status:      asset_entity.StatusActive,
		SSHTunnelID: 1,
		Config:      `{"driver":"postgresql","host":"127.0.0.1","port":5432,"username":"u","ssh_asset_id":1,"proxy_chain":{"layers":[{"id":"legacy-ssh-1","type":"ssh","order":1,"ssh_asset_id":1}]}}`,
		Createtime:  1,
	}
	orphanVNC := &asset_entity.Asset{
		ID:         11,
		Name:       "orphan-vnc",
		Type:       asset_entity.AssetTypeVNC,
		Status:     asset_entity.StatusActive,
		Config:     `{"host":"127.0.0.1","port":5900,"file_ssh_asset_id":1,"proxy_chain":{"layers":[{"id":"socks-1","type":"socks5","order":1,"host":"127.0.0.1","port":1080},{"id":"ssh-2","type":"ssh","order":2,"ssh_asset_id":1}]}}`,
		Createtime: 1,
	}

	_, err := Import(ctx, &BackupData{Assets: []*asset_entity.Asset{orphanSSH, orphanDB, orphanVNC}}, &ImportOptions{
		ImportAssets: true,
		Mode:         "merge",
	}, nil)
	require.NoError(t, err)

	gotSSH := findAssetByName(t, ctx, "orphan-ssh")
	sshCfg, err := gotSSH.GetSSHConfig()
	require.NoError(t, err)
	assert.Zero(t, gotSSH.SSHTunnelID)
	assert.Zero(t, sshCfg.JumpHostID)
	require.NoError(t, gotSSH.Validate())

	gotDB := findAssetByName(t, ctx, "orphan-db")
	dbCfg, err := gotDB.GetDatabaseConfig()
	require.NoError(t, err)
	assert.Zero(t, gotDB.SSHTunnelID)
	assert.Zero(t, dbCfg.SSHAssetID)
	assert.Nil(t, dbCfg.ProxyChain)
	require.NoError(t, gotDB.Validate())

	gotVNC := findAssetByName(t, ctx, "orphan-vnc")
	vncCfg, err := gotVNC.GetVNCConfig()
	require.NoError(t, err)
	assert.Zero(t, vncCfg.FileSSHAssetID)
	require.NotNil(t, vncCfg.ProxyChain)
	require.Len(t, vncCfg.ProxyChain.Layers, 1)
	assert.Equal(t, asset_entity.ProxyChainLayerSOCKS5, vncCfg.ProxyChain.Layers[0].Type)
	require.NoError(t, gotVNC.Validate())
}

func TestExportResolvesDependentTunnelAssets(t *testing.T) {
	ctx := setupBackupTest(t)

	hop1 := &asset_entity.Asset{
		Name:       "hop-1",
		Type:       asset_entity.AssetTypeSSH,
		Status:     asset_entity.StatusActive,
		Config:     `{"host":"10.0.0.1","port":22,"username":"u","auth_type":"password"}`,
		Createtime: 1,
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, hop1))

	hop2 := &asset_entity.Asset{
		Name:        "hop-2",
		Type:        asset_entity.AssetTypeSSH,
		Status:      asset_entity.StatusActive,
		SSHTunnelID: hop1.ID,
		Config:      `{"host":"10.0.0.2","port":22,"username":"u","auth_type":"password"}`,
		Createtime:  1,
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, hop2))

	hop3 := &asset_entity.Asset{
		Name:       "hop-3",
		Type:       asset_entity.AssetTypeSSH,
		Status:     asset_entity.StatusActive,
		Config:     `{"host":"10.0.0.3","port":22,"username":"u","auth_type":"password"}`,
		Createtime: 1,
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, hop3))

	unused := &asset_entity.Asset{
		Name:       "unused",
		Type:       asset_entity.AssetTypeSSH,
		Status:     asset_entity.StatusActive,
		Config:     `{"host":"10.0.0.99","port":22,"username":"u","auth_type":"password"}`,
		Createtime: 1,
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, unused))

	// k8s 仅通过 SSHTunnelID 引用 hop2（而 hop2 又通过 SSHTunnelID 引用 hop1）
	k8sAsset := &asset_entity.Asset{
		Name:        "k8s",
		Type:        asset_entity.AssetTypeK8s,
		Status:      asset_entity.StatusActive,
		SSHTunnelID: hop2.ID,
		Config:      `{"kubeconfig":"enc"}`,
		Createtime:  1,
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, k8sAsset))

	// oss 仅通过 proxy_chain 引用 hop3
	ossAsset := &asset_entity.Asset{
		Name:       "oss",
		Type:       asset_entity.AssetTypeOSS,
		Status:     asset_entity.StatusActive,
		Config:     fmt.Sprintf(`{"provider":"s3","endpoint":"https://s3.internal","proxy_chain":{"layers":[{"id":"ssh-1","type":"ssh","order":1,"ssh_asset_id":%d}]}}`, hop3.ID),
		Createtime: 1,
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, ossAsset))

	data, err := Export(ctx, &ExportOptions{AssetIDs: []int64{k8sAsset.ID, ossAsset.ID}}, nil)
	require.NoError(t, err)

	exportedNames := make([]string, 0, len(data.Assets))
	for _, a := range data.Assets {
		exportedNames = append(exportedNames, a.Name)
	}
	assert.ElementsMatch(t, []string{"hop-1", "hop-2", "hop-3", "k8s", "oss"}, exportedNames)
}
