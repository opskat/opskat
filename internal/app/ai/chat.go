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
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
	"github.com/opskat/opskat/internal/model/entity/conversation_entity"
	"github.com/opskat/opskat/internal/service/ai_provider_svc"
	"github.com/opskat/opskat/internal/service/conversation_svc"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/app/coding"
	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

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

	// 更新会话标题（如果仍是默认标题"新对话"）
	if conv, err := conversation_svc.Conversation().Get(ctx, convID); err == nil && conv.Title == "新对话" {
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

	systemPrompt := builder.Build()

	// 注入审计上下文
	chatCtx := aictx.WithAuditSource(a.ctx, "ai")
	chatCtx = aictx.WithConversationID(chatCtx, convID)
	chatCtx = aictx.WithSessionID(chatCtx, fmt.Sprintf("conv_%d", convID))
	chatCtx = logger.WithContextField(chatCtx, zap.Int64("conv_id", convID))
	if a.pool != nil {
		chatCtx = helper.WithSSHPool(chatCtx, a.pool)
	}

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

	// 注入 policy checker
	if a.policyChecker != nil {
		chatCtx = permission.WithPolicyChecker(chatCtx, a.policyChecker)
	}

	// 旧 entry 若存在，先取消并释放。
	if v, ok := a.runners.LoadAndDelete(convID); ok {
		a.stopEntry(v.(*runnerEntry))
	}

	cfg := *a.systemCfg
	cfg.SystemPrompt = systemPrompt
	if cfg.ProviderEntity != nil {
		cfg.Model = cfg.ProviderEntity.Model
	}
	sys, err := runner.BuildSystem(chatCtx, cfg)
	if err != nil {
		onEvent(runner.StreamEvent{Type: "error", Error: fmt.Sprintf("build coding system: %s", err.Error())})
		return fmt.Errorf("build coding system: %w", err)
	}

	history, lastUserText := runner.SplitForReplay(messages)
	conv := agent.LoadConversation(fmt.Sprintf("opskat-conv-%d", convID), runner.ToAgentMessages(history))
	aiRunner := sys.Agent().Runner(conv)

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
		onEvent(runner.StreamEvent{Type: "error", Error: err.Error()})
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
