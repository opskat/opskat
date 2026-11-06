package query

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/connpool/connpooltest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/asset_repo/mock_asset_repo"
	"github.com/opskat/opskat/internal/service/redis_svc"
)

// 查询面板的 Redis 命令与 Redis 浏览器共用同一份客户端缓存：同一资产同一库只建一次连接，
// 换库另建一条。
func TestExecuteRedisSharesClientsWithRedisBrowser(t *testing.T) {
	srv := connpooltest.StartRedisServer(t)
	asset := &asset_entity.Asset{ID: 5, Type: asset_entity.AssetTypeRedis}
	require.NoError(t, asset.SetRedisConfig(&asset_entity.RedisConfig{Host: srv.Host(), Port: srv.Port()}))
	repo := mock_asset_repo.NewMockAssetRepo(gomock.NewController(t))
	repo.EXPECT().Find(gomock.Any(), int64(5)).Return(asset, nil).AnyTimes()
	orig := asset_repo.Asset()
	asset_repo.RegisterAsset(repo)
	t.Cleanup(func() { asset_repo.RegisterAsset(orig) })

	redisSvc := redis_svc.New(nil)
	t.Cleanup(redisSvc.Close)
	q := &Query{ctx: context.Background(), lang: stubLang{}, redis: redisSvc}

	_, err := q.ExecuteRedis(5, "PING", "")
	require.NoError(t, err)
	_, err = redisSvc.ScanKeys(context.Background(), redis_svc.RedisScanRequest{AssetID: 5, DB: 0, Match: "*", Count: 10})
	require.NoError(t, err)
	_, err = q.ExecuteRedisArgs(5, []string{"PING"}, "0")
	require.NoError(t, err)
	assert.Equal(t, 1, srv.Accepted(), "panel and browser on asset 5 db0 share one client")

	_, err = q.ExecuteRedis(5, "PING", "2")
	require.NoError(t, err)
	assert.Equal(t, 2, srv.Accepted(), "another db gets its own client")
}
