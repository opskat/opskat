// Package serial 实现 serial binder：串口连接、读写、重置。
package serial

import (
	"context"
	"sync"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/serial_svc"
	"github.com/opskat/opskat/internal/service/sessionid"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// Serial binder。
type Serial struct {
	appCtx  context.Context
	ctx     context.Context
	lang    LangProvider
	manager *serial_svc.Manager

	connIDGen          *sessionid.Generator
	pendingConnections sync.Map // map[string]context.CancelFunc 异步连接取消用
}

// New 构造 serial binder。
func New(appCtx context.Context, lang LangProvider, mgr *serial_svc.Manager) *Serial {
	s := &Serial{appCtx: appCtx, lang: lang, manager: mgr, connIDGen: sessionid.NewGenerator("conn")}
	conntest.Register(asset_entity.AssetTypeSerial, s.testConnection)
	return s
}

// nextConnectionID 生成跨重启唯一的连接中转 ID(issue #141)。
func (s *Serial) nextConnectionID() string {
	return s.connIDGen.Next()
}

// Startup 保存 Wails ctx。
func (s *Serial) Startup(ctx context.Context) { s.ctx = ctx }

// Cleanup 关闭所有串口。
func (s *Serial) Cleanup() {
	if s.manager != nil {
		s.manager.CloseAll()
	}
}
