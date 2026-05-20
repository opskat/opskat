// Package k8s 实现 k8s binder：集群/命名空间资源、Pod 详情与日志流。
package k8s

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/opskat/opskat/internal/sshpool"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// K8s binder。
type K8s struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider
	pool   *sshpool.Pool

	logStreams       sync.Map // map[string]context.CancelFunc
	logStreamCounter atomic.Int64
}

// New 构造 k8s binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *K8s {
	return &K8s{appCtx: appCtx, lang: lang, pool: pool}
}

// Startup 保存 Wails ctx。
func (k *K8s) Startup(ctx context.Context) { k.ctx = ctx }

// Cleanup 取消所有正在跑的 pod log 流。
func (k *K8s) Cleanup() {
	k.logStreams.Range(func(_, v any) bool {
		if cancel, ok := v.(context.CancelFunc); ok {
			cancel()
		}
		return true
	})
}
