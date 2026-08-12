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
	"github.com/opskat/opskat/internal/ai/tool"
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

	// docGate 记录"某会话内，某内置资产类型的 exec 用法文档已经到过模型面前"。
	// **唯一**的满足条件是模型显式调用过 help——prompt 里那份按类型清单只注入一行描述，
	// 不含命令语法，仅供发现 help 的存在，不满足门禁（原设计的第二个条件已在实施收尾
	// 评审中移除，理由见 spec §4.2 的"实施期修正"）。单实例贯穿 AI binder 的整个
	// 生命周期，内部按 convID 分片——与 systemCfg.LocalToolGate 的 allow-list 同一种存储
	// 形态（sync.Map/mutex 保护的 map[int64]..., 提供按 convID 的 Reset）。之所以不像
	// LocalToolGate 那样在 activateProvider 里随 provider 切换重建：门禁记录的是"模型是否
	// 见过这份文档"，而文档一旦作为 help 工具调用结果写入过会话历史，就会随 conv 消息重放
	// 一直留在模型上下文里，与当前用的是哪个 LLM provider 无关；切 provider 时重置它只会
	// 制造一次不必要的 help 往返，不会带来任何正确性收益。
	docGate *tool.DocGate

	runners               sync.Map // map[int64]*runnerEntry
	currentConversationID int64

	permissionChan     chan runner.PermissionResponse
	pendingAIApprovals sync.Map // map[string]pendingAIApproval

	flushAckCh chan struct{}
}

type pendingAIApproval struct {
	kind  string
	items []permission.ApprovalItem
	ch    chan permission.ApprovalResponse
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
		docGate:        tool.NewDocGate(),
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

// ActiveTaskCount 返回仍在运行、退出后会丢失当前进度的 AI 生成任务数。
func (a *AI) ActiveTaskCount() int {
	count := 0
	a.runners.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// WaitAIFlushAck 暴露给 main.go 的 OnBeforeClose，等待前端 flush 完成。
func (a *AI) WaitAIFlushAck() <-chan struct{} { return a.flushAckCh }

// DrainAIFlushAck 在 emit ai:flush-all 之前清空 channel 上一次残留，避免拿到旧 ack。
func (a *AI) DrainAIFlushAck() {
	select {
	case <-a.flushAckCh:
	default:
	}
}
