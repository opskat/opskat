// Package kafka 实现 kafka binder：Topic/Consumer Group/Schema/Connect/ACL 管理。
package kafka

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/kafka_svc"
	"github.com/opskat/opskat/internal/sshpool"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Kafka binder。
type Kafka struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	pool    *sshpool.Pool
	service *kafka_svc.Service
}

// New 构造 kafka binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *Kafka {
	k := &Kafka{
		appCtx:  appCtx,
		lang:    lang,
		pool:    pool,
		service: kafka_svc.New(pool),
	}
	conntest.Register(asset_entity.AssetTypeKafka, k.testConnection)
	return k
}

// Service 返回底层 kafka 服务，供 ai binder 在 chat ctx 中注入。
func (k *Kafka) Service() *kafka_svc.Service { return k.service }

// Startup 保存 Wails ctx。
func (k *Kafka) Startup(ctx context.Context) { k.ctx = ctx }

// Cleanup 关闭 kafka 客户端连接。
func (k *Kafka) Cleanup() {
	if k.service != nil {
		k.service.Close()
		k.service = nil
	}
}
