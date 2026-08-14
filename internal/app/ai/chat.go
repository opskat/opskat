package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/runner"
	"github.com/opskat/opskat/internal/ai/skills"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
	"github.com/opskat/opskat/internal/model/entity/conversation_entity"
	"github.com/opskat/opskat/internal/pkg/auditredact"
	"github.com/opskat/opskat/internal/service/ai_provider_svc"
	"github.com/opskat/opskat/internal/service/conversation_svc"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/app/coding"
	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// safeOutwardError 把面向前端的错误正文经 canonical redactor 投影（spec Logs and audit，
// "外发错误仍必须安全"）：provider/工具错误文本可能内嵌凭据材料（API key、PEM、
// Authorization 等），chat.go 直接构造的 error 事件不经过 runner 的 StreamEvent 翻译器，
// 因此必须在这里统一脱敏后再外发。非敏感文本原样保留，不影响诊断。
func safeOutwardError(err error) string {
	if err == nil {
		return ""
	}
	return auditredact.Text(err.Error())
}

// runnerEntry 持有一个活跃会话的 cago 运行栈。
type runnerEntry struct {
	sys        *coding.System
	runner     *agent.Runner
	done       chan struct{}
	sshCache   *tool.SSHClientCache
	dbCache    *helper.DatabaseClientCache
	redisCache *helper.RedisClientCache
	mongoCache *helper.MongoDBClientCache
}

func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// allBuiltinAssetTypeSkills 返回全部已内嵌用法文档的资产类型（skills.Types()——9 个
// exec 类型 + rdp/vnc/local 3 个 doc-only 类型，见 permission.RegisterHelpDoc）的
// 一行技能描述（skills.Description），用于 PromptBuilder 的技能清单。
//
// 无条件全量返回，不看 openTabs：这份清单是**发现**用的，让模型知道 help 存在、以及
// exec 覆盖了哪些类型。doc-only 类型（有 help 无 exec）也混在这同一份返回值里——
// 拆成"exec 真的覆盖"与"仅有配置文档"两段是 PromptBuilder.buildAssetTypeSkills 的
// 职责（按 permission.ExecutorFor 拆分），本函数只负责提供全量的 类型→一行描述 映射，
// 不在这里预判归属。按 Tab 过滤会让没开对应 Tab 的会话完全看不到这条路径。一行一
// 类型，成本可以忽略。
//
// 它**不**满足 exec 的门禁——门禁只认模型显式调用过 help（见 tool.DocGate 的注释）。
// 未内嵌文档、仅由已安装 extension 提供的资产类型不在这里，走的是另一条 extension
// SKILL.md 注入路径（见下方 bridge.GetSkillMDWithExtension）。
func allBuiltinAssetTypeSkills() map[string]string {
	out := make(map[string]string)
	for _, assetType := range skills.Types() {
		if desc, ok := skills.Description(assetType); ok {
			out[assetType] = desc
		}
	}
	return out
}

// normalizeConversationTitle 统一会话标题规则。
func normalizeConversationTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "新对话"
	}
	titleRunes := []rune(title)
	if len(titleRunes) > 50 {
		title = string(titleRunes[:50])
	}
	return title
}

// activateProvider 根据 Provider 配置准备 BuildSystem 所需的依赖。
func (a *AI) activateProvider(p *ai_provider_entity.AIProvider) error {
	apiKey, err := ai_provider_svc.AIProvider().DecryptAPIKey(p)
	if err != nil {
		return fmt.Errorf("解密 API Key 失败: %w", err)
	}

	checker := permission.NewCommandPolicyChecker(a.makeCommandConfirmFunc())
	checker.SetGrantRequestFunc(a.makeGrantRequestFunc())
	a.policyChecker = checker

	cwd, err := defaultAICwd()
	if err != nil {
		return fmt.Errorf("准备 AI 工作目录失败: %w", err)
	}

	a.systemCfg = &runner.SystemConfig{
		ProviderEntity: p,
		APIKey:         apiKey,
		Cwd:            cwd,
		Tools:          tool.Tools(),
		LocalToolGate:  tool.NewLocalToolGate(a.makeLocalToolConfirmFunc()),
	}
	a.resetRunners()
	return nil
}

// buildSendConfig 决定本次发送使用哪个 Provider（模型）：优先会话自身选定的
// ProviderID（「按会话切换模型」#246），为 0 或对应 Provider 已被删除/解密失败时
// 回退到全局激活的 systemCfg。返回值已按最终 Provider 填好 Model；调用方只需再补
// SystemPrompt。工作目录 / 工具 / LocalToolGate 与具体 LLM Provider 无关，直接复用
// systemCfg 的那份，不随会话重建。
func (a *AI) buildSendConfig(ctx context.Context, conv *conversation_entity.Conversation) runner.SystemConfig {
	cfg := *a.systemCfg
	if conv != nil && conv.ProviderID != 0 &&
		(cfg.ProviderEntity == nil || conv.ProviderID != cfg.ProviderEntity.ID) {
		p, err := ai_provider_svc.AIProvider().Get(ctx, conv.ProviderID)
		if err != nil {
			logger.Default().Warn("会话选定的 Provider 不存在，回退全局激活 Provider",
				zap.Int64("conv_id", conv.ID), zap.Int64("provider_id", conv.ProviderID), zap.Error(err))
		} else if apiKey, derr := ai_provider_svc.AIProvider().DecryptAPIKey(p); derr != nil {
			logger.Default().Warn("解密会话 Provider API Key 失败，回退全局激活 Provider",
				zap.Int64("conv_id", conv.ID), zap.Int64("provider_id", conv.ProviderID), zap.Error(derr))
		} else {
			cfg.ProviderEntity = p
			cfg.APIKey = apiKey
		}
	}
	if cfg.ProviderEntity != nil {
		cfg.Model = cfg.ProviderEntity.Model
	}
	return cfg
}

// defaultAICwd 默认 AI 工作目录 = ~/.opskat。不存在时自动创建。
func defaultAICwd() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cwd := filepath.Join(home, ".opskat")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return "", err
	}
	return cwd, nil
}

// resetRunners 停止并清空所有缓存的 runnerEntry。
func (a *AI) resetRunners() {
	var wg sync.WaitGroup
	a.runners.Range(func(key, value any) bool {
		if e, ok := value.(*runnerEntry); ok {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.stopEntry(e)
			}()
		}
		a.runners.Delete(key)
		return true
	})

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		logger.Default().Warn("resetRunners: 部分 runner 退出未在 3s 内完成，放行关闭")
	}
}

// stopEntry 取消正在跑的 turn 并等待事件消费 goroutine 退出，最后释放 Runner / System。
func (a *AI) stopEntry(e *runnerEntry) {
	if e == nil {
		return
	}
	if e.runner != nil {
		_ = e.runner.Cancel("user_stop")
	}
	if e.done != nil {
		select {
		case <-e.done:
		case <-time.After(3 * time.Second):
		}
	}
	if e.sshCache != nil {
		if err := e.sshCache.Close(); err != nil {
			logger.Default().Warn("close SSH cache", zap.Error(err))
		}
	}
	if e.dbCache != nil {
		if err := e.dbCache.Close(); err != nil {
			logger.Default().Warn("close database cache", zap.Error(err))
		}
	}
	if e.redisCache != nil {
		if err := e.redisCache.Close(); err != nil {
			logger.Default().Warn("close Redis cache", zap.Error(err))
		}
	}
	if e.mongoCache != nil {
		if err := e.mongoCache.Close(); err != nil {
			logger.Default().Warn("close MongoDB cache", zap.Error(err))
		}
	}
	if e.runner != nil {
		_ = e.runner.Close()
	}
	if e.sys != nil {
		if cerr := e.sys.Close(context.Background()); cerr != nil {
			logger.Default().Warn("close coding system", zap.Error(cerr))
		}
	}
}

// InitAIProvider 启动时加载激活的 Provider。
func (a *AI) InitAIProvider() {
	p, err := ai_provider_svc.AIProvider().GetActive(i18n.Ctx(a.ctx, a.lang.Lang()))
	if err != nil {
		return // 无激活 provider，跳过
	}
	if err := a.activateProvider(p); err != nil {
		logger.Default().Warn("activate AI provider on startup", zap.Error(err))
	}
}

// --- AI 操作 ---

// ConversationDisplayMessage 返回给前端的会话消息（用于恢复显示）
type ConversationDisplayMessage struct {
	Role       string                             `json:"role"`
	Content    string                             `json:"content"`
	Blocks     []conversation_entity.ContentBlock `json:"blocks"`
	TokenUsage *conversation_entity.TokenUsage    `json:"tokenUsage,omitempty"`
}

// CreateConversation 创建新会话
func (a *AI) CreateConversation() (*conversation_entity.Conversation, error) {
	if a.systemCfg == nil {
		return nil, fmt.Errorf("请先配置 AI Provider")
	}
	ctx := i18n.Ctx(a.ctx, a.lang.Lang())

	// 获取激活 Provider ID
	activeProvider, _ := ai_provider_svc.AIProvider().GetActive(ctx)
	var providerID int64
	if activeProvider != nil {
		providerID = activeProvider.ID
	}

	conv := &conversation_entity.Conversation{
		Title:      "新对话",
		ProviderID: providerID,
	}
	// 新会话默认沿用激活 Provider 的模型/类型，让「按会话切换模型」有一份初始记录。
	if activeProvider != nil {
		conv.Model = activeProvider.Model
		conv.ProviderType = activeProvider.Type
	}
	if err := conversation_svc.Conversation().Create(ctx, conv); err != nil {
		return nil, err
	}
	a.currentConversationID = conv.ID
	return conv, nil
}

// ListConversations 获取会话列表
func (a *AI) ListConversations() ([]*conversation_entity.Conversation, error) {
	return conversation_svc.Conversation().List(i18n.Ctx(a.ctx, a.lang.Lang()))
}

// UpdateConversationTitle 更新会话标题。
func (a *AI) UpdateConversationTitle(id int64, title string) error {
	ctx := i18n.Ctx(a.ctx, a.lang.Lang())
	err := conversation_svc.Conversation().UpdateTitle(ctx, id, normalizeConversationTitle(title))
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("会话不存在: %w", err)
	}
	return fmt.Errorf("更新会话标题失败: %w", err)
}

// SetConversationProvider 为指定会话切换使用的 Provider（模型）。作用域是**单会话**：
// 只改这条会话，不影响全局激活 Provider，也不影响其它会话（#246）。
//
// 只持久化选择、不动缓存的 runner：下次 SendAIMessage 本就会 LoadAndDelete 旧 entry 并按
// 新 Provider 重建（buildSendConfig 从库里读最新 ProviderID）。因此切换不会打断该会话正在
// 进行的生成——正在流式的回答用旧 Provider 跑完，下一条消息才用新 Provider。
func (a *AI) SetConversationProvider(convID, providerID int64) error {
	if a.systemCfg == nil {
		return fmt.Errorf("请先配置 AI Provider")
	}
	ctx := i18n.Ctx(a.ctx, a.lang.Lang())
	p, err := ai_provider_svc.AIProvider().Get(ctx, providerID)
	if err != nil {
		return fmt.Errorf("provider 不存在: %w", err)
	}
	if err := conversation_svc.Conversation().UpdateProvider(ctx, convID, providerID, p.Model); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("会话不存在: %w", err)
		}
		return fmt.Errorf("切换会话模型失败: %w", err)
	}
	return nil
}

// SwitchConversation 切换到指定会话，返回显示消息
func (a *AI) SwitchConversation(id int64) ([]ConversationDisplayMessage, error) {
	ctx := i18n.Ctx(a.ctx, a.lang.Lang())
	conv, err := conversation_svc.Conversation().Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("会话不存在: %w", err)
	}

	a.switchToConversation(conv)
	return a.loadConversationDisplayMessages(ctx, id)
}

// LoadConversationMessages 只读加载会话消息，不修改 currentConversationID。
func (a *AI) LoadConversationMessages(id int64) ([]ConversationDisplayMessage, error) {
	ctx := i18n.Ctx(a.ctx, a.lang.Lang())
	if _, err := conversation_svc.Conversation().Get(ctx, id); err != nil {
		return nil, fmt.Errorf("会话不存在: %w", err)
	}
	return a.loadConversationDisplayMessages(ctx, id)
}

func (a *AI) loadConversationDisplayMessages(ctx context.Context, id int64) ([]ConversationDisplayMessage, error) {
	msgs, err := conversation_svc.Conversation().LoadMessages(ctx, id)
	if err != nil {
		return nil, err
	}

	var displayMsgs []ConversationDisplayMessage
	for _, msg := range msgs {
		blocks, err := msg.GetBlocks()
		if err != nil {
			logger.Default().Warn("get message blocks", zap.Error(err))
		}
		usage, err := msg.GetTokenUsage()
		if err != nil {
			logger.Default().Warn("get message token usage", zap.Error(err))
		}
		displayMsgs = append(displayMsgs, ConversationDisplayMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			Blocks:     blocks,
			TokenUsage: usage,
		})
	}
	return displayMsgs, nil
}

// switchToConversation 内部切换会话逻辑
func (a *AI) switchToConversation(conv *conversation_entity.Conversation) {
	a.currentConversationID = conv.ID
}

// DeleteConversation 删除会话
func (a *AI) DeleteConversation(id int64) error {
	// 先停止正在运行的生成
	if v, ok := a.runners.LoadAndDelete(id); ok {
		a.stopEntry(v.(*runnerEntry))
	}

	err := conversation_svc.Conversation().Delete(i18n.Ctx(a.ctx, a.lang.Lang()), id)
	if err != nil {
		return err
	}
	a.docGate.Reset(id)
	if a.currentConversationID == id {
		a.currentConversationID = 0
	}
	return nil
}

// SendAIMessage 发送 AI 消息，通过 Wails Events 流式返回
func (a *AI) SendAIMessage(convID int64, messages []runner.Message, aiCtx runner.AIContext) error {
	if a.systemCfg == nil {
		return fmt.Errorf("请先配置 AI Provider")
	}

	ctx := i18n.Ctx(a.ctx, a.lang.Lang())

	// 自动创建会话（首次发消息时）
	if convID == 0 {
		conv, err := a.CreateConversation()
		if err != nil {
			return fmt.Errorf("创建会话失败: %w", err)
		}
		convID = conv.ID
	}

	// 加载会话：既用于标题回填，也用于决定本次发送使用哪个 Provider（按会话切换模型）。
	conv, convErr := conversation_svc.Conversation().Get(ctx, convID)
	if convErr != nil {
		logger.Default().Warn("加载会话失败，将回退全局激活 Provider", zap.Int64("conv_id", convID), zap.Error(convErr))
	}

	// 更新会话标题（如果仍是默认标题"新对话"）
	if conv != nil && conv.Title == "新对话" {
		for _, msg := range messages {
			if msg.Role == runner.RoleUser {
				title := normalizeConversationTitle(string(msg.Content))
				if err := conversation_svc.Conversation().UpdateTitle(ctx, convID, title); err != nil {
					logger.Default().Error("update conversation title", zap.Error(err))
				}
				break
			}
		}
	}

	eventName := fmt.Sprintf("ai:event:%d", convID)

	// 构建动态系统提示
	lang := "en"
	if a.lang.Lang() == "zh-cn" {
		lang = "zh-cn"
	}
	builder := runner.NewPromptBuilder(lang, aiCtx)

	// Inject extension SKILL.md based on connected asset types
	if a.extSvc != nil {
		bridge := a.extSvc.Bridge()
		mds := make(map[string]string)
		seen := make(map[string]bool)
		for _, tab := range aiCtx.OpenTabs {
			if seen[tab.Type] {
				continue
			}
			seen[tab.Type] = true
			if skillMD := bridge.GetSkillMDWithExtension(tab.Type); skillMD.Content != "" {
				mds[skillMD.ExtensionName] = skillMD.Content
			}
		}
		if len(mds) > 0 {
			builder.SetExtensionSkillMDs(mds)
		}
	}

	// Inject the compact per-type skill listing for built-in asset types. This is
	// discovery only — it tells the model that help(asset) exists and which types exec
	// covers. It deliberately does NOT mark anything documented on the doc gate: the only
	// thing that satisfies the gate is an explicit help(asset) call, handled inside
	// handleHelp (internal/ai/tool/tool_handlers_unified.go).
	builder.SetAssetTypeSkills(allBuiltinAssetTypeSkills())

	systemPrompt := builder.Build()

	// 注入审计上下文
	chatCtx := aictx.WithAuditSource(a.ctx, "ai")
	chatCtx = aictx.WithConversationID(chatCtx, convID)
	chatCtx = aictx.WithSessionID(chatCtx, fmt.Sprintf("conv_%d", convID))
	chatCtx = logger.WithContextField(chatCtx, zap.Int64("conv_id", convID))
	if a.pool != nil {
		chatCtx = helper.WithSSHPool(chatCtx, a.pool)
	}

	// 注入 exec 用法门禁：单实例贯穿 AI binder 生命周期，内部按 convID 分片记录，
	// 与 LocalToolGate 的 allow-list 用同一种存储形态（见 ai.go 的字段注释）。
	chatCtx = tool.WithDocGate(chatCtx, a.docGate)

	// 注入本地写门禁：与 local_write 工具走的是**同一个** LocalToolGate 实例，因此
	// "本次会话允许"在两条路上是同一条记录。传输面与 `object get --file=` 在两端都被
	// 策略/grant 自动放行（一个弹框都没有）时靠它守住本地落点——那一档下 spec §6.2
	// 「本地端点不产生审批项」所依赖的"用户批准的那条命令串"并不存在。
	chatCtx = helper.WithLocalWriteGate(chatCtx, a.systemCfg.LocalToolGate)

	// 同一次 Send 内复用连接。
	sshCache := tool.NewSSHClientCache()
	dbCache := helper.NewDatabaseClientCache()
	redisCache := helper.NewRedisClientCache()
	mongoCache := helper.NewMongoDBClientCache()
	chatCtx = tool.WithSSHCache(chatCtx, sshCache)
	chatCtx = helper.WithDatabaseCache(chatCtx, dbCache)
	chatCtx = helper.WithRedisCache(chatCtx, redisCache)
	chatCtx = helper.WithMongoDBCache(chatCtx, mongoCache)
	if a.kafkaSvc != nil {
		chatCtx = helper.WithKafkaService(chatCtx, a.kafkaSvc)
	}
	if a.serialMgr != nil {
		chatCtx = helper.WithSerialManager(chatCtx, a.serialMgr)
	}

	onEvent := func(event runner.StreamEvent) {
		wailsRuntime.EventsEmit(a.ctx, eventName, event)

		// done/stopped 时更新会话时间
		if event.Type == "done" || event.Type == "stopped" {
			if conv, err := conversation_svc.Conversation().Get(a.ctx, convID); err == nil {
				if err := conversation_svc.Conversation().Update(a.ctx, conv); err != nil {
					logger.Default().Warn("update conversation time", zap.Error(err))
				}
			}
		}
	}

	// 注入 policy checker。无条件注入：工具侧的权限检查是 fail-closed 的
	// （permission.RequireChecker），checker 缺失不再等于放行，而是整条 exec 直接失败。
	// 从前这里的 `if a.policyChecker != nil` 暗示它可能为 nil——实际不会（activateProvider
	// 在 systemCfg 之前赋值，而入口守卫 systemCfg == nil），但把这个不变式写成条件分支，
	// 等于把"安全"寄托在读者不会误以为 nil 是合法状态上。
	chatCtx = permission.WithPolicyChecker(chatCtx, a.policyChecker)

	// 旧 entry 若存在，先取消并释放。
	if v, ok := a.runners.LoadAndDelete(convID); ok {
		a.stopEntry(v.(*runnerEntry))
	}

	// 按会话选定的 Provider 组装本次发送配置（#246：可与全局激活 Provider 不同）。
	cfg := a.buildSendConfig(ctx, conv)
	cfg.SystemPrompt = systemPrompt
	sys, err := runner.BuildSystem(chatCtx, cfg)
	if err != nil {
		onEvent(runner.StreamEvent{Type: "error", Error: fmt.Sprintf("build coding system: %s", safeOutwardError(err))})
		return fmt.Errorf("build coding system: %w", err)
	}

	history, lastUserText := runner.SplitForReplay(messages)
	agentConv := agent.LoadConversation(fmt.Sprintf("opskat-conv-%d", convID), runner.ToAgentMessages(history))
	aiRunner := sys.Agent().Runner(agentConv)

	entry := &runnerEntry{
		sys:        sys,
		runner:     aiRunner,
		done:       make(chan struct{}),
		sshCache:   sshCache,
		dbCache:    dbCache,
		redisCache: redisCache,
		mongoCache: mongoCache,
	}
	a.runners.Store(convID, entry)

	events, err := aiRunner.Send(chatCtx, lastUserText)
	if err != nil {
		close(entry.done)
		a.runners.Delete(convID)
		_ = aiRunner.Close()
		_ = sys.Close(context.Background())
		if chatCtx.Err() != nil {
			onEvent(runner.StreamEvent{Type: "stopped"})
			return nil //nolint:nilerr // 取消是用户主动行为，不是错误
		}
		onEvent(runner.StreamEvent{Type: "error", Error: safeOutwardError(err)})
		return fmt.Errorf("send to LLM: %w", err)
	}

	go func() {
		defer close(entry.done)
		translator := runner.NewStreamTranslator()
		for ev := range events {
			translator.Translate(ev, onEvent)
		}
	}()
	return nil
}

// QueueAIMessage 在生成过程中通过 cago Runner.Steer 把用户消息注入当前 turn。
func (a *AI) QueueAIMessage(convID int64, queueID string, content string) error {
	v, ok := a.runners.Load(convID)
	if !ok {
		return fmt.Errorf("会话 %d 没有正在运行的生成", convID)
	}
	entry := v.(*runnerEntry)
	if entry.runner == nil {
		return fmt.Errorf("会话 %d 没有正在运行的生成", convID)
	}
	err := entry.runner.Steer(context.Background(), content, agent.WithSteerID(queueID), agent.WithSteerDisplay(content))
	if err != nil && !errors.Is(err, agent.ErrSteerNoActiveTurn) {
		logger.Default().Warn("cago Steer failed", zap.Error(err))
		return err
	}
	return nil
}

// RemoveQueuedAIMessage 尝试从 cago Runner 尚未消费的 Steer 队列里删除一条消息。
func (a *AI) RemoveQueuedAIMessage(convID int64, queueID string) bool {
	v, ok := a.runners.Load(convID)
	if !ok || queueID == "" {
		return false
	}
	entry := v.(*runnerEntry)
	if entry.runner == nil {
		return false
	}
	return entry.runner.RemovePendingSteer(queueID)
}

// ClearQueuedAIMessages 清空 cago Runner 尚未消费的 Steer 队列。
func (a *AI) ClearQueuedAIMessages(convID int64) []string {
	v, ok := a.runners.Load(convID)
	if !ok {
		return []string{}
	}
	entry := v.(*runnerEntry)
	if entry.runner == nil {
		return []string{}
	}
	ids := entry.runner.ClearPendingSteers()
	if ids == nil {
		return []string{}
	}
	return ids
}

// StopAIGeneration 调用 cago Runner.Cancel 触发取消。
func (a *AI) StopAIGeneration(convID int64) error {
	v, ok := a.runners.LoadAndDelete(convID)
	if !ok {
		return nil
	}
	a.stopEntry(v.(*runnerEntry))
	return nil
}

// SaveConversationMessages 前端调用，保存显示消息到数据库。
func (a *AI) SaveConversationMessages(convID int64, displayMsgs []ConversationDisplayMessage) error {
	if convID == 0 {
		return nil
	}
	ctx := i18n.Ctx(a.ctx, a.lang.Lang())
	var msgs []*conversation_entity.Message
	for i, dm := range displayMsgs {
		msg := &conversation_entity.Message{
			ConversationID: convID,
			Role:           dm.Role,
			Content:        dm.Content,
			SortOrder:      i,
			Createtime:     time.Now().Unix(),
		}
		if err := msg.SetBlocks(dm.Blocks); err != nil {
			logger.Default().Error("set message blocks", zap.Error(err))
		}
		if err := msg.SetTokenUsage(dm.TokenUsage); err != nil {
			logger.Default().Error("set message token usage", zap.Error(err))
		}
		msgs = append(msgs, msg)
	}
	return conversation_svc.Conversation().SaveMessages(ctx, convID, msgs)
}

// GetCurrentConversationID 获取当前会话ID
func (a *AI) GetCurrentConversationID() int64 {
	return a.currentConversationID
}

// subscribeAIFlushAck 在 Startup 中注册：前端完成会话落盘后会 EventsEmit("ai:flush-done")。
func (a *AI) subscribeAIFlushAck() {
	wailsRuntime.EventsOn(a.ctx, "ai:flush-done", func(_ ...any) {
		select {
		case a.flushAckCh <- struct{}{}:
		default:
		}
	})
}

// RespondPermission 前端响应权限确认请求
func (a *AI) RespondPermission(behavior, message string) {
	resp := runner.PermissionResponse{Behavior: behavior, Message: message}
	select {
	case a.permissionChan <- resp:
	default:
	}
}
