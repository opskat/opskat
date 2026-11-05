package runner

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// EventTranslator 把 cago agent.Event 翻译成前端 StreamEvent 契约。
//
// 翻译保持 type 字段名和事件字段稳定，供前端 ai:event:{convID} 监听者消费。
//
// 翻译是有状态的：thinking → content / tool_start 之间的转移需要插入一条
// "thinking_done"，保持前端既有 stream contract。EventTranslator 实例每个对话每轮 1 个。
//
// 审批事件（approval_request / approval_result）由 app_ai.go 的 confirmFunc 直接
// emit，不走 cago event 流，因此不在本适配器范围。
type EventTranslator struct {
	ctx context.Context

	// mu 串行化同步 emit 与定时器 flush，并保护下面所有字段。
	mu         sync.Mutex
	inThinking bool
	closed     bool
	// pending 是尚未发出的合并 delta（pendingType 为 "content" / "thinking"）。
	pending     strings.Builder
	pendingType string
	timer       *time.Timer
	// timerGen 每启动一个定时器 +1：已触发、正等 mu 的旧定时器回调据此认出自己已被取代，
	// 不去清掉新定时器、也不提前冲刷新攒的 delta。
	timerGen uint64
	// emit 是最近一次 Translate 的回调，供定时器与 Close flush 使用。
	emit func(StreamEvent)
}

// deltaCoalesceWindow 是 content / thinking delta 的合并窗口。
const deltaCoalesceWindow = 40 * time.Millisecond

// NewStreamTranslator 创建一个不携带调用方 correlation 的事件翻译器（测试和独立使用）。
func NewStreamTranslator() *EventTranslator {
	return NewStreamTranslatorWithContext(context.Background())
}

// NewStreamTranslatorWithContext 创建一个继承 runner 调用上下文的事件翻译器，使重试
// 等运维日志保留 conv_id 等安全 correlation 字段，而不是退化到 logger.Default()。
func NewStreamTranslatorWithContext(ctx context.Context) *EventTranslator {
	return &EventTranslator{ctx: ctx}
}

// Translate 处理一条 cago agent.Event，调用 emit 0..N 次发出对应 StreamEvent。
//
// 多发场景：thinking → content 转移会先发 "thinking_done" 再发 "content"。
// 不映射的 EventKind（如 EventMessageEnd / EventToolDelta）直接静默——
// 前者由 EventTextDelta / EventTurnEnd 隐含，后者是 cago 流式工具参数增量，
// 前端 StreamEvent 契约没有对应字段，可丢弃。
//
// content / thinking delta 按 deltaCoalesceWindow 合并后再发；任何其它会发事件的
// 种类先 flush 已攒的 delta，保证事件顺序不变。结束时调用 Close 冲刷尾部。
func (t *EventTranslator) Translate(ev agent.Event, emit func(StreamEvent)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.emit = emit

	switch ev.Kind {
	case agent.EventTextDelta:
		t.flushPending(emit, "content")
		t.flushThinking(emit)
		t.appendDelta("content", ev.Delta)
		return

	case agent.EventThinkingDelta:
		t.flushPending(emit, "thinking")
		t.inThinking = true
		t.appendDelta("thinking", ev.Delta)
		return

	case agent.EventMessageEnd, agent.EventToolDelta:
		// 不映射，且不打断正在合并的 delta。
		return
	}

	t.flushPending(emit, "")
	switch ev.Kind {

	case agent.EventPreToolUse:
		t.flushThinking(emit)
		if ev.Tool == nil {
			return
		}
		// 原值语义：tool 参数按原始 JSON 序列化后逐字外发。
		// 真实 cago 工具参数总是由 JSON 解码而来，必然可序列化；万一序列化失败
		// （合成/不存在的输入）就外发原始错误文本，绝不注入任何占位字面量。
		input := ""
		if ev.Tool.Input != nil {
			b, err := json.Marshal(ev.Tool.Input)
			if err != nil {
				b = []byte(err.Error())
			}
			input = string(b)
		}
		emit(StreamEvent{
			Type:       "tool_start",
			ToolName:   ev.Tool.Name,
			ToolInput:  input,
			ToolCallID: ev.Tool.ToolUseID,
		})

	case agent.EventPostToolUse:
		if ev.Tool == nil {
			return
		}
		emit(StreamEvent{
			Type:       "tool_result",
			ToolName:   ev.Tool.Name,
			ToolCallID: ev.Tool.ToolUseID,
			// 原值语义：工具结果原文透传。
			Content: extractToolResultText(ev.Tool.Output),
			IsError: ev.Tool.Output != nil && ev.Tool.Output.IsError,
		})

	case agent.EventTurnEnd:
		t.flushThinking(emit)
		if ev.Usage != nil {
			emit(StreamEvent{Type: "usage", Usage: convertUsage(ev.Usage)})
		}

	case agent.EventRetry:
		// 透传 Attempt / Delay / Cause —— 前端用 RetryDelayMs 做倒计时同步、Content 显示第几次。
		// 事件里的 Cause 原值外发；运维日志只记 attempt/delay_ms 与 ctx 里的 conv_id 等
		// correlation 字段，不复制 cause payload（spec：结构化日志不记录 provider 响应正文）。
		msg := ""
		attempt := 0
		delayMs := 0
		if ev.Retry != nil {
			attempt = ev.Retry.Attempt
			delayMs = int(ev.Retry.Delay / time.Millisecond)
			if ev.Retry.Cause != nil {
				msg = ev.Retry.Cause.Error()
			}
		} else if ev.Error != nil {
			msg = ev.Error.Error()
		}
		// 落运维日志：用户线上反馈"看不到 RetryBanner"时，先查后端日志确认 cago
		// 真的触发了 retry。如果日志没有，说明 cago shouldRetry 没识别错误（多半
		// 是 provider 没把 *APIError 包成 *provider.ProviderError），与前端无关。
		// 不记录 cause：它可能携带 provider 回显的用户输入/远端输出。
		logger.Ctx(t.ctx).Info("AI provider retry",
			zap.Int("attempt", attempt),
			zap.Int("delay_ms", delayMs),
		)
		emit(StreamEvent{
			Type:         "retry",
			Error:        msg,
			Content:      strconv.Itoa(attempt),
			RetryDelayMs: delayMs,
		})

	case agent.EventCancelled:
		emit(StreamEvent{Type: "stopped"})

	case agent.EventError:
		msg := ""
		if ev.Error != nil {
			msg = ev.Error.Error()
		}
		// 原值语义：面向用户的错误正文原文透传。
		emit(StreamEvent{Type: "error", Error: msg})

	case agent.EventCompacted:
		// 新事件类型——前端尚未识别就当未知 type 忽略，不会破坏渲染。
		emit(StreamEvent{Type: "compacted"})

	case agent.EventSteerConsumed:
		// cago 把一条 Steer 队列消息追加到 conv —— 翻译为前端契约里的
		// queue_consumed：UI 据此收尾当前 assistant 气泡、插 user 气泡、开新流，
		// 并从 pendingQueue 弹出首条。Delta 是 displayText（含 @mention 原样），
		// 没有 display 时回落 LLM 文本，前端把它当 user 消息内容渲染。
		t.flushThinking(emit)
		emit(StreamEvent{Type: "queue_consumed", Content: ev.Delta, QueueID: ev.SteerID})

	case agent.EventDone:
		t.flushThinking(emit)
		emit(StreamEvent{Type: "done"})

	}
}

// Close 冲刷尚未发出的 delta 并停止定时器；之后 Translate 与定时器都不再 emit。
// 幂等。事件流结束（通道关闭）时调用，覆盖没有 done / error / stopped 收尾的情形。
func (t *EventTranslator) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	if t.emit != nil {
		t.flushPending(t.emit, "")
	}
	t.closed = true
}

// appendDelta 把 delta 攒进缓冲，并在需要时启动合并定时器。持有 t.mu。
func (t *EventTranslator) appendDelta(typ, delta string) {
	t.pendingType = typ
	t.pending.WriteString(delta)
	if t.timer == nil {
		t.timerGen++
		gen := t.timerGen
		t.timer = time.AfterFunc(deltaCoalesceWindow, func() { t.onTimer(gen) })
	}
}

func (t *EventTranslator) onTimer(gen uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer == nil || t.timerGen != gen {
		return // 触发前已被 flushPending 停掉并取代
	}
	t.timer = nil
	if t.closed || t.emit == nil {
		return
	}
	t.flushPending(t.emit, "")
}

// flushPending 发出已攒的 delta。keepType 非空且等于缓冲类型时不 flush（继续合并同类）。
// 持有 t.mu。
func (t *EventTranslator) flushPending(emit func(StreamEvent), keepType string) {
	if t.pendingType == "" || t.pendingType == keepType {
		return
	}
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	typ, content := t.pendingType, t.pending.String()
	t.pendingType = ""
	t.pending.Reset()
	emit(StreamEvent{Type: typ, Content: content})
}

// flushThinking 若之前发了 thinking 现在要发非 thinking 内容，先发 "thinking_done" 收尾。
func (t *EventTranslator) flushThinking(emit func(StreamEvent)) {
	if t.inThinking {
		emit(StreamEvent{Type: "thinking_done"})
		t.inThinking = false
	}
}

// extractToolResultText 把 cago ToolResultBlock 的 Content（异构 ContentBlock 切片）
// 拼成一段文本——OpsKat 前端 tool_result 当前只渲染纯文本。
// 非文本 block（image / 嵌套 tool_result 等）目前 OpsKat 没有 tool 真在用，
// 遇到就跳过；如未来需要透传图像/结构化结果再扩展。
func extractToolResultText(blk *agent.ToolResultBlock) string {
	if blk == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range blk.Content {
		if tb, ok := c.(*agent.TextBlock); ok {
			sb.WriteString(tb.Text)
			continue
		}
		// 兼容值类型 TextBlock，blocks.TextBlock 的方法接收器是值
		if tb, ok := c.(agent.TextBlock); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}

// convertUsage 把 cago provider.Usage 折算成 OpsKat Usage。
//
// 映射规则：
//   - InputTokens         ← normalized PromptTokens (uncached input)
//   - OutputTokens        ← CompletionTokens（ReasoningTokens 已合并进 CompletionTokens 由 provider 上送）
//   - CacheCreationTokens ← CacheCreationTokens
//   - CacheReadTokens     ← CachedTokens
func convertUsage(u *provider.Usage) *Usage {
	if u == nil {
		return nil
	}
	return &Usage{
		InputTokens:         normalizeInputTokens(u),
		OutputTokens:        u.CompletionTokens,
		CacheCreationTokens: u.CacheCreationTokens,
		CacheReadTokens:     u.CachedTokens,
	}
}

func normalizeInputTokens(u *provider.Usage) int {
	input := u.PromptTokens
	// OpenAI reports prompt_tokens with cached_tokens included, while Anthropic reports
	// fresh input separately from cache read/write. Use TotalTokens to detect the former
	// and keep OpsKat's InputTokens meaning consistent: uncached prompt input only.
	if u.CachedTokens > 0 && u.TotalTokens > 0 {
		cacheIncludedTotal := u.PromptTokens + u.CompletionTokens
		cacheSeparatedTotal := u.PromptTokens + u.CacheCreationTokens + u.CachedTokens + u.CompletionTokens
		if u.TotalTokens == cacheIncludedTotal && cacheSeparatedTotal != cacheIncludedTotal {
			input -= u.CachedTokens
		}
	}
	if input < 0 {
		return 0
	}
	return input
}
