// Package etcd 实现 etcd binder:KV 浏览、查询面板、连接测试。
package etcd

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/etcd_svc"
	"github.com/opskat/opskat/internal/sshpool"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Etcd binder。
type Etcd struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	pool    *sshpool.Pool
	service *etcd_svc.Service
}

// New 构造 etcd binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *Etcd {
	e := &Etcd{
		appCtx:  appCtx,
		lang:    lang,
		pool:    pool,
		service: etcd_svc.New(pool),
	}
	conntest.Register(asset_entity.AssetTypeEtcd, e.testConnection)
	return e
}

// Startup 保存 Wails ctx。
func (e *Etcd) Startup(ctx context.Context) { e.ctx = ctx }

// Cleanup 占位。
func (e *Etcd) Cleanup() {}
