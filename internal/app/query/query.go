// Package query 实现 query binder：SQL/Mongo/Redis 执行 + 三种面板连接缓存 + 表导出。
package query

import (
	"context"
	"database/sql"
	"time"

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
