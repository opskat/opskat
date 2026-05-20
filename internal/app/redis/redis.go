// Package redis 实现 redis binder：Redis 浏览/编辑（key 扫描、读写、TTL、慢日志等）。
package redis

import (
	"context"

	"github.com/opskat/opskat/internal/service/redis_svc"
	"github.com/opskat/opskat/internal/sshpool"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Redis binder。
type Redis struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	pool    *sshpool.Pool
	service *redis_svc.Service
}

// New 构造 redis binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *Redis {
	return &Redis{
		appCtx:  appCtx,
		lang:    lang,
		pool:    pool,
		service: redis_svc.New(pool),
	}
}

// Startup 保存 Wails ctx。
func (r *Redis) Startup(ctx context.Context) { r.ctx = ctx }

// Cleanup 占位。
func (r *Redis) Cleanup() {}
