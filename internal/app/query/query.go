// Package query 实现 query binder：SQL/Mongo/Redis 执行 + 面板连接缓存 + 表导出。
package query

import (
	"context"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/redis_svc"
	"github.com/opskat/opskat/internal/sshpool"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Query binder：DB/Mongo/Redis 查询执行 + 面板连接缓存。
// Redis 的连接不在这里缓存：与 Redis 浏览器共用 redis_svc.Service 的客户端缓存（由 redis binder 持有）。
type Query struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider

	pool  *sshpool.Pool
	redis *redis_svc.Service

	dbPanelCache    *connpool.AssetConnCache[dbdriver.Session]
	mongoPanelCache *connpool.AssetConnCache[*connpool.MongoClientCloser]

	evictCtx context.Context
	evictCxl context.CancelFunc
}

// New 构造 query binder。
// redisSvc 是与 redis binder 共用的 Redis 服务，面板的 Redis 命令走它的客户端缓存。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool, redisSvc *redis_svc.Service) *Query {
	q := &Query{appCtx: appCtx, lang: lang, pool: pool, redis: redisSvc}
	conntest.Register(asset_entity.AssetTypeDatabase, q.testDatabaseConnection)
	conntest.Register(asset_entity.AssetTypeRedis, q.testRedisConnection)
	conntest.Register(asset_entity.AssetTypeMongoDB, q.testMongoConnection)
	return q
}

// Startup 初始化数据库 / Mongo 面板连接缓存 + 各自的 evictor 协程。
func (q *Query) Startup(ctx context.Context) {
	q.ctx = ctx
	q.dbPanelCache = connpool.NewAssetConnCache[dbdriver.Session]("database", connpool.DefaultConnIdleTTL)
	q.mongoPanelCache = connpool.NewAssetConnCache[*connpool.MongoClientCloser]("mongodb", connpool.DefaultConnIdleTTL)
	q.evictCtx, q.evictCxl = context.WithCancel(ctx)
	go q.dbPanelCache.RunEvictor(q.evictCtx, connpool.DefaultConnEvictInterval)
	go q.mongoPanelCache.RunEvictor(q.evictCtx, connpool.DefaultConnEvictInterval)
	// 在这里而不是 New 里注册：两个缓存到 Startup 才建出来。
	assetconn.RegisterInvalidator("query-panel", q.dropAssetConns)
}

// dropAssetConns 丢弃该资产在面板缓存里的连接（一个资产可能缓存了多个库）。资产被删除
// 或改了连接配置时由 assetconn 广播；下次查询会按最新配置重新拨号，所以改口令 / 换主机
// 之后不必手动重开面板。Redis 客户端由 redis binder 登记的失效钩子丢弃。
func (q *Query) dropAssetConns(_ context.Context, assetID int64) error {
	q.dbPanelCache.DropAsset(assetID)
	q.mongoPanelCache.DropAsset(assetID)
	return nil
}

// Cleanup 关闭 evictor 并释放所有缓存连接。
func (q *Query) Cleanup() {
	if q.evictCxl != nil {
		q.evictCxl()
	}
	if q.dbPanelCache != nil {
		_ = q.dbPanelCache.Close()
	}
	if q.mongoPanelCache != nil {
		_ = q.mongoPanelCache.Close()
	}
}
