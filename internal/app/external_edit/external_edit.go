// Package external_edit 实现远程文件外部编辑 binder：
// 仅做依赖装配和 IPC 转发，状态机/编码/审计等业务逻辑都下沉到 external_edit_svc。
package external_edit

import (
	"context"
	"sync"

	"github.com/opskat/opskat/internal/service/external_edit_svc"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

// LangProvider 由 system binder 实现，提供当前 UI 语言。
type LangProvider interface {
	Lang() string
}

// Type aliases 让 Wails 在生成 TS binding 时把 service 内部类型挂在本包下，
// 前端只需要 import 一个稳定的 external_edit 命名空间。
type (
	Settings           = external_edit_svc.Settings
	SettingsInput      = external_edit_svc.SettingsInput
	OpenRequest        = external_edit_svc.OpenRequest
	Session            = external_edit_svc.Session
	SaveResult         = external_edit_svc.SaveResult
	CompareResult      = external_edit_svc.CompareResult
	MergePrepareResult = external_edit_svc.MergePrepareResult
	MergeApplyRequest  = external_edit_svc.MergeApplyRequest
)

// ExternalEdit 外部编辑 binder：只持有已经装配完成的 service，不直接解析下层依赖。
type ExternalEdit struct {
	ctx     context.Context
	lang    LangProvider
	svc     *external_edit_svc.Service
	emitter *EventEmitter
}

// EventEmitter bridges the service's process-lifetime callback to the Wails
// context, which only becomes available during binder startup.
type EventEmitter struct {
	mu  sync.RWMutex
	ctx context.Context
}

func NewEventEmitter() *EventEmitter { return &EventEmitter{} }

func (e *EventEmitter) Startup(ctx context.Context) {
	e.mu.Lock()
	e.ctx = ctx
	e.mu.Unlock()
}

func (e *EventEmitter) Emit(event external_edit_svc.Event) {
	e.mu.RLock()
	ctx := e.ctx
	e.mu.RUnlock()
	if ctx != nil {
		wailsRuntime.EventsEmit(ctx, "external-edit:event", event)
	}
}

// New constructs the IPC binder from an already composed service.
func New(lang LangProvider, svc *external_edit_svc.Service, emitter *EventEmitter) *ExternalEdit {
	return &ExternalEdit{
		lang:    lang,
		svc:     svc,
		emitter: emitter,
	}
}

// Startup saves the Wails context and starts the already composed service.
func (e *ExternalEdit) Startup(ctx context.Context) {
	e.ctx = ctx
	if e.emitter != nil {
		e.emitter.Startup(ctx)
	}
	if e.svc == nil {
		return
	}
	if err := e.svc.Start(context.Background()); err != nil {
		logger.Default().Warn("start external edit service", zap.Error(err))
	}
}

// Cleanup 关闭 service：watcher / 后台 goroutine / 文件句柄都在 service.Close 里收口。
func (e *ExternalEdit) Cleanup() {
	if e.svc == nil {
		return
	}
	if err := e.svc.Close(); err != nil {
		logger.Default().Warn("close external edit service", zap.Error(err))
	}
}
