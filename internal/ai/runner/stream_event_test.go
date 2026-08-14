package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/logger"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// drain 把 emit 串变成切片，便于断言。
func drain(t *EventTranslator, evs ...agent.Event) []StreamEvent {
	var out []StreamEvent
	emit := func(e StreamEvent) { out = append(out, e) }
	for _, ev := range evs {
		t.Translate(ev, emit)
	}
	return out
}

func TestEventTranslator_TextDelta(t *testing.T) {
	Convey("EventTextDelta → content", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind:  agent.EventTextDelta,
			Delta: "hi",
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "content")
		So(out[0].Content, ShouldEqual, "hi")
	})
}

func TestEventTranslator_ThinkingThenContent(t *testing.T) {
	Convey("thinking 后跟 content 时插入 thinking_done", t, func() {
		out := drain(NewStreamTranslator(),
			agent.Event{Kind: agent.EventThinkingDelta, Delta: "let me think"},
			agent.Event{Kind: agent.EventTextDelta, Delta: "answer"},
		)
		So(out, ShouldHaveLength, 3)
		So(out[0].Type, ShouldEqual, "thinking")
		So(out[0].Content, ShouldEqual, "let me think")
		So(out[1].Type, ShouldEqual, "thinking_done")
		So(out[2].Type, ShouldEqual, "content")
		So(out[2].Content, ShouldEqual, "answer")
	})
}

func TestEventTranslator_ThinkingThenToolStart(t *testing.T) {
	Convey("thinking 后直接调工具时也插入 thinking_done", t, func() {
		out := drain(NewStreamTranslator(),
			agent.Event{Kind: agent.EventThinkingDelta, Delta: "..."},
			agent.Event{Kind: agent.EventPreToolUse, Tool: &agent.ToolEvent{
				ToolUseID: "tu_1",
				Name:      "exec",
				Input:     map[string]any{"asset_id": float64(1), "command": "uptime"},
			}},
		)
		So(out, ShouldHaveLength, 3)
		So(out[1].Type, ShouldEqual, "thinking_done")
		So(out[2].Type, ShouldEqual, "tool_start")
		So(out[2].ToolName, ShouldEqual, "exec")
		So(out[2].ToolCallID, ShouldEqual, "tu_1")
		So(out[2].ToolInput, ShouldContainSubstring, `"command":"uptime"`)
	})
}

func TestEventTranslator_ToolStartPassesRawInput(t *testing.T) {
	Convey("EventPreToolUse 的 tool_input 按原始 JSON 逐字外发，不做任何脱敏", t, func() {
		secret := "live-" + "credential-sentinel" // 运行时拼接避免 gosec G101
		out := drain(NewStreamTranslator(), agent.Event{
			Kind: agent.EventPreToolUse,
			Tool: &agent.ToolEvent{
				ToolUseID: "tu_secret",
				Name:      "put_asset",
				Input: map[string]any{
					"name": "prod",
					"config": map[string]any{
						"host":        "db.internal",
						"password":    secret,
						"private_key": map[string]any{"path": "/Users/x/.ssh/id_rsa", "passphrase": secret},
					},
				},
			}})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "tool_start")
		So(out[0].ToolName, ShouldEqual, "put_asset")
		So(out[0].ToolCallID, ShouldEqual, "tu_secret")
		// Go json.Marshal 按键名排序；逐字断言完整输入，凭据材料原样保留。
		So(out[0].ToolInput, ShouldEqual, `{"config":{"host":"db.internal","password":"`+secret+`","private_key":{"passphrase":"`+secret+`","path":"/Users/x/.ssh/id_rsa"}},"name":"prod"}`)
	})
}

func TestEventTranslator_ToolStartMarshalFailureKeepsRawErrorText(t *testing.T) {
	Convey("EventPreToolUse 输入无法序列化时外发原始序列化错误文本，不注入 redaction 字面量", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind: agent.EventPreToolUse,
			Tool: &agent.ToolEvent{
				ToolUseID: "tu_bad",
				Name:      "broken",
				Input:     map[string]any{"unsupported": func() {}},
			},
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].ToolInput, ShouldNotEqual, "<redacted>")
		So(out[0].ToolInput, ShouldContainSubstring, "json:")
	})
}

func TestEventTranslator_ToolResultPassesRawContent(t *testing.T) {
	Convey("EventPostToolUse 的 JSON 结果逐字外发，不做任何脱敏", t, func() {
		secret := "result-" + "credential-sentinel" // 运行时拼接避免 gosec G101
		out := drain(NewStreamTranslator(), agent.Event{
			Kind: agent.EventPostToolUse,
			Tool: &agent.ToolEvent{
				ToolUseID: "tu_r1",
				Name:      "query",
				Output: &agent.ToolResultBlock{
					Content: []agent.ContentBlock{
						agent.TextBlock{Text: `{"rows":3,"password":"` + secret + `"}`},
					},
				},
			},
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "tool_result")
		So(out[0].Content, ShouldEqual, `{"rows":3,"password":"`+secret+`"}`)
	})

	Convey("普通文本结果逐字外发，凭据形式文本不被改写", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind: agent.EventPostToolUse,
			Tool: &agent.ToolEvent{
				ToolUseID: "tu_r2",
				Name:      "exec",
				Output: &agent.ToolResultBlock{
					Content: []agent.ContentBlock{
						agent.TextBlock{Text: "ok\n--password hidden-cli-secret"},
					},
				},
			},
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].Content, ShouldEqual, "ok\n--password hidden-cli-secret")
	})
}

func TestEventTranslator_RetryPassesRawCause(t *testing.T) {
	Convey("EventRetry 的 Cause 在事件里逐字外发，Attempt/Delay 原样保留", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind: agent.EventRetry,
			Retry: &agent.RetryEvent{
				Attempt: 3,
				Delay:   2 * time.Second,
				Cause:   errors.New("auth failed: --api-key retry-secret-token"),
			},
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "retry")
		So(out[0].Error, ShouldEqual, "auth failed: --api-key retry-secret-token")
		So(out[0].Content, ShouldEqual, "3")
		So(out[0].RetryDelayMs, ShouldEqual, 2000)
	})
}

func TestEventTranslator_RetryLogRedactsCauseKeepsCorrelation(t *testing.T) {
	Convey("EventRetry 的运维日志 cause 脱敏，并保留 attempt/delay_ms/conv_id correlation", t, func() {
		core, logs := observer.New(zap.DebugLevel)
		orig := logger.Default()
		logger.SetLogger(zap.New(core))
		t.Cleanup(func() { logger.SetLogger(orig) })
		ctx := logger.WithContextField(context.Background(), zap.Int64("conv_id", 42))

		secret := "log-" + "retry-secret-token"
		drain(NewStreamTranslatorWithContext(ctx), agent.Event{
			Kind: agent.EventRetry,
			Retry: &agent.RetryEvent{
				Attempt: 4,
				Delay:   5 * time.Second,
				Cause:   errors.New("provider error: --api-key " + secret),
			},
		})

		var found bool
		for _, le := range logs.All() {
			if le.Message != "AI provider retry" {
				continue
			}
			found = true
			cm := le.ContextMap()
			So(cm["cause"].(string), ShouldNotContainSubstring, secret)
			So(cm["attempt"], ShouldEqual, int64(4))
			So(cm["delay_ms"], ShouldEqual, int64(5000))
			So(cm["conv_id"], ShouldEqual, int64(42))
		}
		So(found, ShouldBeTrue)
	})
}

func TestEventTranslator_ErrorPassesRawMessage(t *testing.T) {
	Convey("EventError 的消息逐字外发，不做任何脱敏", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind:  agent.EventError,
			Error: errors.New("connection failed: --password error-secret-pass"),
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "error")
		So(out[0].Error, ShouldEqual, "connection failed: --password error-secret-pass")
	})
}

func TestEventTranslator_PostToolUse(t *testing.T) {
	Convey("EventPostToolUse → tool_result（拼出 TextBlock 内容）", t, func() {
		out := drain(NewStreamTranslator(),
			agent.Event{Kind: agent.EventPostToolUse, Tool: &agent.ToolEvent{
				ToolUseID: "tu_2",
				Name:      "list_assets",
				Output: &agent.ToolResultBlock{
					Content: []agent.ContentBlock{
						&agent.TextBlock{Text: "row1\n"},
						&agent.TextBlock{Text: "row2\n"},
					},
				},
			}},
		)
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "tool_result")
		So(out[0].ToolName, ShouldEqual, "list_assets")
		So(out[0].ToolCallID, ShouldEqual, "tu_2")
		So(out[0].Content, ShouldEqual, "row1\nrow2\n")
	})
}

func TestEventTranslator_PostToolUsePreservesErrorState(t *testing.T) {
	Convey("EventPostToolUse preserves ToolResultBlock.IsError", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind: agent.EventPostToolUse,
			Tool: &agent.ToolEvent{
				ToolUseID: "tu_denied",
				Name:      "upload_file",
				Output: &agent.ToolResultBlock{
					IsError: true,
					Content: []agent.ContentBlock{
						agent.TextBlock{Text: "USER DENIED: transfer rejected"},
					},
				},
			},
		})

		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "tool_result")
		So(out[0].IsError, ShouldBeTrue)
	})
}

func TestEventTranslator_TurnEndUsage(t *testing.T) {
	Convey("EventTurnEnd（带 Usage）→ usage", t, func() {
		out := drain(NewStreamTranslator(),
			agent.Event{Kind: agent.EventTurnEnd, Usage: &provider.Usage{
				PromptTokens:        100,
				CompletionTokens:    50,
				CachedTokens:        20,
				CacheCreationTokens: 10,
				TotalTokens:         180,
			}},
		)
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "usage")
		So(out[0].Usage, ShouldNotBeNil)
		So(out[0].Usage.InputTokens, ShouldEqual, 100)
		So(out[0].Usage.OutputTokens, ShouldEqual, 50)
		So(out[0].Usage.CacheReadTokens, ShouldEqual, 20)
		So(out[0].Usage.CacheCreationTokens, ShouldEqual, 10)
	})

	Convey("OpenAI 风格 usage：prompt_tokens 已包含 cached_tokens 时归一化为非缓存输入", t, func() {
		out := drain(NewStreamTranslator(),
			agent.Event{Kind: agent.EventTurnEnd, Usage: &provider.Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				CachedTokens:     20,
				TotalTokens:      150,
			}},
		)
		So(out, ShouldHaveLength, 1)
		So(out[0].Usage, ShouldNotBeNil)
		So(out[0].Usage.InputTokens, ShouldEqual, 80)
		So(out[0].Usage.OutputTokens, ShouldEqual, 50)
		So(out[0].Usage.CacheReadTokens, ShouldEqual, 20)
	})
}

func TestEventTranslator_TurnEndNoUsage(t *testing.T) {
	Convey("EventTurnEnd（无 Usage）静默", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{Kind: agent.EventTurnEnd})
		So(out, ShouldBeEmpty)
	})
}

func TestEventTranslator_Retry(t *testing.T) {
	Convey("EventRetry → retry，Attempt/Delay/Cause 全部透传", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind: agent.EventRetry,
			Retry: &agent.RetryEvent{
				Attempt: 2,
				Delay:   3 * time.Second,
				Cause:   errors.New("timeout"),
			},
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "retry")
		So(out[0].Error, ShouldEqual, "timeout")
		// Attempt 放在 Content 字段，前端 parseInt(event.content) 解析。
		So(out[0].Content, ShouldEqual, "2")
		So(out[0].RetryDelayMs, ShouldEqual, 3000)
	})

	Convey("EventRetry 无 Retry 字段时退化到 ev.Error，不带 Attempt/Delay", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind:  agent.EventRetry,
			Error: errors.New("fallback"),
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "retry")
		So(out[0].Error, ShouldEqual, "fallback")
		So(out[0].Content, ShouldEqual, "0")
		So(out[0].RetryDelayMs, ShouldEqual, 0)
	})
}

func TestEventTranslator_Canceled(t *testing.T) {
	Convey("EventCancelled → stopped", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{Kind: agent.EventCancelled})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "stopped")
	})
}

func TestEventTranslator_Error(t *testing.T) {
	Convey("EventError → error 携带消息", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{
			Kind:  agent.EventError,
			Error: errors.New("boom"),
		})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "error")
		So(out[0].Error, ShouldEqual, "boom")
	})
}

func TestEventTranslator_Compacted(t *testing.T) {
	Convey("EventCompacted → compacted（新 type）", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{Kind: agent.EventCompacted})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "compacted")
	})
}

func TestEventTranslator_SteerConsumed(t *testing.T) {
	Convey("EventSteerConsumed → queue_consumed，Content / QueueID 透传", t, func() {
		out := drain(NewStreamTranslator(), agent.Event{Kind: agent.EventSteerConsumed, Delta: "排队的内容", SteerID: "q1"})
		So(out, ShouldHaveLength, 1)
		So(out[0].Type, ShouldEqual, "queue_consumed")
		So(out[0].Content, ShouldEqual, "排队的内容")
		So(out[0].QueueID, ShouldEqual, "q1")
	})

	Convey("仍处于 thinking 时先发 thinking_done", t, func() {
		out := drain(NewStreamTranslator(),
			agent.Event{Kind: agent.EventThinkingDelta, Delta: "..."},
			agent.Event{Kind: agent.EventSteerConsumed, Delta: "x"},
		)
		So(out, ShouldHaveLength, 3)
		So(out[1].Type, ShouldEqual, "thinking_done")
		So(out[2].Type, ShouldEqual, "queue_consumed")
	})
}

func TestEventTranslator_Done_FlushesThinking(t *testing.T) {
	Convey("EventDone 收尾，仍处于 thinking 时先发 thinking_done", t, func() {
		out := drain(NewStreamTranslator(),
			agent.Event{Kind: agent.EventThinkingDelta, Delta: "..."},
			agent.Event{Kind: agent.EventDone},
		)
		So(out, ShouldHaveLength, 3)
		So(out[1].Type, ShouldEqual, "thinking_done")
		So(out[2].Type, ShouldEqual, "done")
	})
}

func TestEventTranslator_IgnoredKinds(t *testing.T) {
	Convey("EventMessageEnd / EventToolDelta 不映射", t, func() {
		out := drain(NewStreamTranslator(),
			agent.Event{Kind: agent.EventMessageEnd},
			agent.Event{Kind: agent.EventToolDelta},
		)
		So(out, ShouldBeEmpty)
	})
}

func TestExtractToolResultText_SkipsNonText(t *testing.T) {
	Convey("extractToolResultText 跳过非文本 block", t, func() {
		text := extractToolResultText(&agent.ToolResultBlock{
			Content: []agent.ContentBlock{
				&agent.TextBlock{Text: "before"},
				&agent.ImageBlock{}, // 非文本，应被跳过
				&agent.TextBlock{Text: "after"},
			},
		})
		So(text, ShouldEqual, "beforeafter")
	})
}
