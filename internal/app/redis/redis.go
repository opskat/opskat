// Package redis 实现 redis binder：Redis 浏览/编辑（key 扫描、读写、TTL、慢日志等）。
package redis

import (
	"context"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/service/redis_svc"
	"github.com/opskat/opskat/internal/sshpool"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Redis binder。持有 redis_svc.Service（含按资产缓存的 Redis 客户端），query binder 经
// Service() 共用同一份。
type Redis struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	pool    *sshpool.Pool
	service *redis_svc.Service

	evictCxl context.CancelFunc
}

// New 构造 redis binder，并登记资产改配置 / 删除时丢弃缓存客户端的失效钩子。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *Redis {
	r := &Redis{
		appCtx:  appCtx,
		lang:    lang,
		pool:    pool,
		service: redis_svc.New(pool),
	}
	assetconn.RegisterInvalidator("redis", func(_ context.Context, assetID int64) error {
		r.service.DropAsset(assetID)
		return nil
	})
	return r
}

// Service 返回底层 Redis 服务，供 query binder 的 Redis 命令共用客户端缓存。
func (r *Redis) Service() *redis_svc.Service { return r.service }

// Startup 保存 Wails ctx，启动缓存客户端的空闲驱逐。
func (r *Redis) Startup(ctx context.Context) {
	r.ctx = ctx
	var evictCtx context.Context
	evictCtx, r.evictCxl = context.WithCancel(ctx)
	go r.service.RunEvictor(evictCtx)
}

// Cleanup 停止空闲驱逐并关闭全部缓存客户端。
func (r *Redis) Cleanup() {
	if r.evictCxl != nil {
		r.evictCxl()
	}
	r.service.Close()
}
