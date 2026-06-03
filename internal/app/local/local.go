// Package local 实现 local binder：本地终端连接、读写、尺寸调整。
package local

import (
	"context"
	"sync/atomic"

	"github.com/opskat/opskat/internal/service/localterm_svc"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Local binder。
type Local struct {
	appCtx      context.Context
	ctx         context.Context
	lang        LangProvider
	manager     *localterm_svc.Manager
	connCounter atomic.Int64
}

// New 构造 local binder。
func New(appCtx context.Context, lang LangProvider, mgr *localterm_svc.Manager) *Local {
	return &Local{appCtx: appCtx, lang: lang, manager: mgr}
}

// Startup 保存 Wails ctx。
func (l *Local) Startup(ctx context.Context) { l.ctx = ctx }

// Cleanup 关闭所有本地终端。
func (l *Local) Cleanup() {
	if l.manager != nil {
		l.manager.CloseAll()
	}
}
