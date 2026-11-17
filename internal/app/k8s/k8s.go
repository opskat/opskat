// Package k8s 实现 k8s binder：集群资源读取、Pod 日志流与 Pod 终端（exec）会话。
package k8s

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/cago-frame/cago/pkg/logger"

	"github.com/opskat/opskat/internal/assetconn"
	"github.com/opskat/opskat/internal/connpool"
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
	// loadCall 按资产当前配置构造调用上下文；测试替换它来接假集群。
	loadCall func(ctx context.Context, assetID int64) (*k8sCallContext, error)
	// conns 按资产缓存调用上下文，同一资产的请求复用到集群的连接（经 SSH 跳板时只需少量握手）。
	conns       *connpool.AssetConnCache[*k8sCallContext]
	evictCancel context.CancelFunc

	logStreams       sync.Map // map[string]context.CancelFunc
	logStreamCounter atomic.Int64

	execSessions sync.Map // map[string]*execSession
	// execEnding 跟踪已连上、尚未跑完结束流程（含容器内 shell 清理）的会话；execLive 是其个数。
	execEnding sync.WaitGroup
	execLive   atomic.Int64
}

// New 构造 k8s binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *K8s {
	k := &K8s{appCtx: appCtx, lang: lang, pool: pool}
	k.emit = func(event string, payload any) { wailsRuntime.EventsEmit(k.ctx, event, payload) }
	k.loadCall = k.loadK8sCall
	k.conns = connpool.NewAssetConnCache[*k8sCallContext]("k8s", connpool.DefaultConnIdleTTL)
	assetconn.RegisterInvalidator("k8s", func(ctx context.Context, assetID int64) error {
		k.dropAssetConns(ctx, assetID)
		return nil
	})
	return k
}

// Startup 保存 Wails ctx 并启动空闲连接的驱逐协程。
func (k *K8s) Startup(ctx context.Context) {
	k.ctx = ctx
	evictCtx, cancel := context.WithCancel(ctx)
	k.evictCancel = cancel
	go k.conns.RunEvictor(evictCtx, connpool.DefaultConnEvictInterval)
}

// Cleanup 取消所有正在跑的 pod log 流、结束全部终端会话并关闭所有缓存的集群连接（应用退出）。
func (k *K8s) Cleanup() {
	if k.evictCancel != nil {
		k.evictCancel()
	}
	_ = k.conns.Close()
	logger.Default().Info("k8s connections closed")
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
