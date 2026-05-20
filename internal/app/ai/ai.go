// Package ai 实现 ai binder：会话管理、消息收发、provider 配置、AI 工具审批。
//
// 同时暴露 ToolExecutor 接口给 opsctl，让 opsctl 在外部触发 AI 工具调用时复用同一执行路径。
package ai

import (
	"context"
	"sync"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/runner"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/internal/service/kafka_svc"
	"github.com/opskat/opskat/internal/service/serial_svc"
	"github.com/opskat/opskat/internal/sshpool"
)

// LangProvider 由 system binder 实现。
type LangProvider interface {
	Lang() string
}

// AI binder。
type AI struct {
	appCtx context.Context
	ctx    context.Context
	lang   LangProvider
	pool   *sshpool.Pool

	// 跨 binder 共享的下层服务（main.go 注入；可能为 nil）。
	kafkaSvc  *kafka_svc.Service
	serialMgr *serial_svc.Manager
	extSvc    *extension_svc.Service
	window    WindowActivator

	systemCfg     *runner.SystemConfig
	policyChecker *permission.CommandPolicyChecker

	runners               sync.Map // map[int64]*runnerEntry
	currentConversationID int64

	permissionChan     chan runner.PermissionResponse
	pendingAIApprovals sync.Map // map[string]chan permission.ApprovalResponse

	flushAckCh chan struct{}
}

// SetKafkaService 由 main.go 注入：AI tool 在 chat ctx 中通过 helper.WithKafkaService 暴露给 handler。
func (a *AI) SetKafkaService(svc *kafka_svc.Service) { a.kafkaSvc = svc }

// SetSerialManager 同上。
func (a *AI) SetSerialManager(mgr *serial_svc.Manager) { a.serialMgr = mgr }

// SetExtensionService 由 main.go 注入 extension_svc，供 SendAIMessage 注入 SKILL.md。
func (a *AI) SetExtensionService(svc *extension_svc.Service) { a.extSvc = svc }

// New 构造 ai binder。
func New(appCtx context.Context, lang LangProvider, pool *sshpool.Pool) *AI {
	return &AI{
		appCtx:         appCtx,
		lang:           lang,
		pool:           pool,
		permissionChan: make(chan runner.PermissionResponse, 1),
		flushAckCh:     make(chan struct{}, 1),
	}
}

// Startup 初始化 AI provider、订阅 flush ack 事件。
func (a *AI) Startup(ctx context.Context) {
	a.ctx = ctx
	a.InitAIProvider()
	a.subscribeAIFlushAck()
	aictx.SetDataChangeNotifier(&dataChangeNotifier{ai: a})
}

// Cleanup 占位（ai service 没持有需要主动关的资源）。
func (a *AI) Cleanup() {}

// WaitAIFlushAck 暴露给 main.go 的 OnBeforeClose，等待前端 flush 完成。
func (a *AI) WaitAIFlushAck() <-chan struct{} { return a.flushAckCh }

// DrainAIFlushAck 在 emit ai:flush-all 之前清空 channel 上一次残留，避免拿到旧 ack。
func (a *AI) DrainAIFlushAck() {
	select {
	case <-a.flushAckCh:
	default:
	}
}
