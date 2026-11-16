// Package k8s 实现 k8s binder：集群资源读取、Pod 日志流与 Pod 终端（exec）会话。
package k8s

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/opskat/opskat/internal/sshpool"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
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

	// emit 向前端发事件；测试替换它来观察事件。
	emit func(event string, payload any)

	logStreams       sync.Map // map[string]context.CancelFunc
	logStreamCounter atomic.Int64

	execSessions sync.Map // map[string]*execSession
	execCounter  atomic.Int64
}

// New 构造 k8s binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *K8s {
	k := &K8s{appCtx: appCtx, lang: lang, pool: pool}
	k.emit = func(event string, payload any) { wailsRuntime.EventsEmit(k.ctx, event, payload) }
	return k
}

// Startup 保存 Wails ctx。
func (k *K8s) Startup(ctx context.Context) { k.ctx = ctx }

// Cleanup 取消所有正在跑的 pod log 流并结束全部终端会话（应用退出）。
func (k *K8s) Cleanup() {
	k.logStreams.Range(func(_, v any) bool {
		if cancel, ok := v.(context.CancelFunc); ok {
			cancel()
		}
		return true
	})
	k.execSessions.Range(func(_, v any) bool {
		v.(*execSession).cancel()
		return true
	})
}
