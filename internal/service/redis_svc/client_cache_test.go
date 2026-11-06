package redis_svc

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/connpool/connpooltest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
)

func standaloneConfig(node *connpooltest.RedisServer) *asset_entity.RedisConfig {
	return &asset_entity.RedisConfig{Host: node.Host(), Port: node.Port()}
}

func clusterConfig(node *connpooltest.RedisServer) *asset_entity.RedisConfig {
	return &asset_entity.RedisConfig{Mode: asset_entity.RedisModeCluster, Nodes: []string{node.Addr()}}
}

// useRedisAsset 让资产 id 解析为 config(srv) 这份 Redis 配置；返回的函数把它改指向另一个节点，
// 模拟用户改了连接配置。
func useRedisAsset(t *testing.T, id int64, srv *connpooltest.RedisServer, config func(*connpooltest.RedisServer) *asset_entity.RedisConfig) (repoint func(*connpooltest.RedisServer)) {
	t.Helper()
	var current atomic.Pointer[connpooltest.RedisServer]
	current.Store(srv)
	repo := mock_asset_repo.NewMockAssetRepo(gomock.NewController(t))
	repo.EXPECT().Find(gomock.Any(), id).DoAndReturn(func(context.Context, int64) (*asset_entity.Asset, error) {
		asset := &asset_entity.Asset{ID: id, Type: asset_entity.AssetTypeRedis}
		require.NoError(t, asset.SetRedisConfig(config(current.Load())))
		return asset, nil
	}).AnyTimes()
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(repo)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })
	return current.Store
}

// 浏览器连续翻页、点 key 时，同一资产同一库只建一次连接。
func TestServiceReusesConnectionForSameAssetAndDB(t *testing.T) {
	srv := connpooltest.StartRedisServer(t)
	useRedisAsset(t, 7, srv, standaloneConfig)
	s := New(nil)
	t.Cleanup(s.Close)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := s.ScanKeys(ctx, RedisScanRequest{AssetID: 7, DB: 0, Match: "*", Count: 10})
		require.NoError(t, err)
		_, err = s.GetKeyDetail(ctx, RedisKeyRequest{AssetID: 7, DB: 0, Key: "k"})
		require.NoError(t, err)
	}

	assert.Equal(t, 1, srv.Accepted(), "10 consecutive operations on asset 7 db0 should dial once")
}

// 不同库各自一个客户端；同一库的后续操作仍复用。
func TestServiceKeysClientsByDB(t *testing.T) {
	srv := connpooltest.StartRedisServer(t)
	useRedisAsset(t, 7, srv, standaloneConfig)
	s := New(nil)
	t.Cleanup(s.Close)
	ctx := context.Background()

	for _, db := range []int{0, 3, 0, 3} {
		_, err := s.ScanKeys(ctx, RedisScanRequest{AssetID: 7, DB: db, Match: "*", Count: 10})
		require.NoError(t, err)
	}

	assert.Equal(t, 2, srv.Accepted())
}

// 运维面板每 2s 刷新集群概览：首次建好集群客户端后，后续刷新不再新建连接。
func TestServiceClusterOverviewReusesClusterClient(t *testing.T) {
	node := connpooltest.StartRedisClusterNode(t)
	useRedisAsset(t, 9, node, clusterConfig)
	s := New(nil)
	t.Cleanup(s.Close)
	ctx := context.Background()

	_, err := s.ClusterOverview(ctx, 9, "")
	require.NoError(t, err)
	afterFirst := node.Accepted()
	for i := 0; i < 5; i++ {
		_, err := s.ClusterOverview(ctx, 9, "")
		require.NoError(t, err)
	}

	assert.Equal(t, afterFirst, node.Accepted(), "refreshes after the first should not open connections")
}

// 资产改了连接配置（assetconn 失效广播 → DropAsset）后，下次操作按新配置重新拨号。
func TestServiceRedialsWithNewConfigAfterDropAsset(t *testing.T) {
	oldNode, newNode := connpooltest.StartRedisServer(t), connpooltest.StartRedisServer(t)
	repoint := useRedisAsset(t, 7, oldNode, standaloneConfig)
	s := New(nil)
	t.Cleanup(s.Close)
	ctx := context.Background()
	scan := func() {
		t.Helper()
		_, err := s.ScanKeys(ctx, RedisScanRequest{AssetID: 7, DB: 0, Match: "*", Count: 10})
		require.NoError(t, err)
	}

	scan()
	repoint(newNode)
	scan() // 配置改了但还没广播失效：仍是缓存里的旧客户端
	assert.Equal(t, 1, oldNode.Accepted())
	assert.Equal(t, 0, newNode.Accepted())

	s.DropAsset(7)
	scan()
	scan()
	assert.Equal(t, 1, oldNode.Accepted())
	assert.Equal(t, 1, newNode.Accepted(), "the next operation after invalidation dials the new config once")
}
