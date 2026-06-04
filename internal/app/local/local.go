// Package local 实现 local binder：本地终端连接、读写、尺寸调整。
package local

import (
	"context"

	"github.com/opskat/opskat/internal/service/localterm_svc"
	"github.com/opskat/opskat/internal/service/sessionid"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Local binder。
type Local struct {
	appCtx    context.Context
	ctx       context.Context
	lang      LangProvider
	manager   *localterm_svc.Manager
	connIDGen *sessionid.Generator
}

// New 构造 local binder。
func New(appCtx context.Context, lang LangProvider, mgr *localterm_svc.Manager) *Local {
	return &Local{appCtx: appCtx, lang: lang, manager: mgr, connIDGen: sessionid.NewGenerator("local-conn")}
}

// nextConnectionID 生成跨重启唯一的连接中转 ID;保留 local- 前缀以兼容
// restore 时的 transport 推断(issue #141)。
func (l *Local) nextConnectionID() string {
	return l.connIDGen.Next()
}

// Startup 保存 Wails ctx。
func (l *Local) Startup(ctx context.Context) { l.ctx = ctx }

// Cleanup 关闭所有本地终端。
func (l *Local) Cleanup() {
	if l.manager != nil {
		l.manager.CloseAll()
	}
}
