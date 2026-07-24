// Package query 实现 query binder：SQL/Mongo/Redis 执行 + 三种面板连接缓存 + 表导出。
package query

import (
	"context"
	"database/sql"
	"time"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/redis/go-redis/v9"
)

const (
	panelConnIdleTTL       = 5 * time.Minute
	panelConnEvictInterval = 30 * time.Second
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Query binder：DB/Mongo/Redis 查询执行 + 面板连接缓存。
type Query struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider

	pool *sshpool.Pool

	dbPanelCache    *panelConnCache[*sql.DB]
	redisPanelCache *panelConnCache[*redis.Client]
	mongoPanelCache *panelConnCache[*connpool.MongoClientCloser]

	evictCtx context.Context
	evictCxl context.CancelFunc
}

// New 构造 query binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *Query {
	q := &Query{appCtx: appCtx, lang: lang, pool: pool}
	conntest.Register(asset_entity.AssetTypeDatabase, q.testDatabaseConnection)
	conntest.Register(asset_entity.AssetTypeRedis, q.testRedisConnection)
	conntest.Register(asset_entity.AssetTypeMongoDB, q.testMongoConnection)
	return q
}

// Startup 初始化三个面板连接缓存 + 各自的 evictor 协程。
func (q *Query) Startup(ctx context.Context) {
	q.ctx = ctx
	q.dbPanelCache = newPanelConnCache[*sql.DB]("database", panelConnIdleTTL)
	q.redisPanelCache = newPanelConnCache[*redis.Client]("redis", panelConnIdleTTL)
	q.mongoPanelCache = newPanelConnCache[*connpool.MongoClientCloser]("mongodb", panelConnIdleTTL)
	q.evictCtx, q.evictCxl = context.WithCancel(ctx)
	go q.dbPanelCache.startEvictor(q.evictCtx, panelConnEvictInterval)
	go q.redisPanelCache.startEvictor(q.evictCtx, panelConnEvictInterval)
	go q.mongoPanelCache.startEvictor(q.evictCtx, panelConnEvictInterval)
	// 在这里而不是 New 里注册：三个缓存到 Startup 才建出来。
	assetconn.RegisterInvalidator("query-panel", q.dropAssetConns)
}

// dropAssetConns 丢弃该资产在三个面板缓存里的连接（一个资产可能缓存了多个库 / 多个
// redis db index）。资产被删除或改了连接配置时由 assetconn 广播；下次查询会按最新
// 配置重新拨号，所以改口令 / 换主机之后不必手动重开面板。
func (q *Query) dropAssetConns(_ context.Context, assetID int64) error {
	q.dbPanelCache.DropAsset(assetID)
	q.redisPanelCache.DropAsset(assetID)
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
	if q.redisPanelCache != nil {
		_ = q.redisPanelCache.Close()
	}
	if q.mongoPanelCache != nil {
		_ = q.mongoPanelCache.Close()
	}
}
