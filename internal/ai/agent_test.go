package ai

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

// mockProvider 模拟 AI provider，返回预设的响应
type mockProvider struct {
	responses [][]StreamEvent // 每轮对话的响应事件序列
	round     int
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Chat(_ context.Context, _ []Message, _ []Tool) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		if m.round < len(m.responses) {
			for _, event := range m.responses[m.round] {
				ch <- event
			}
		}
		m.round++
	}()
	return ch, nil
}

// mockExecutor 模拟工具执行
type mockExecutor struct {
	calls []struct {
		Name string
		Args string
	}
	results map[string]string
}

func (m *mockExecutor) Execute(_ context.Context, name string, args string) (string, error) {
	m.calls = append(m.calls, struct {
		Name string
		Args string
	}{name, args})
	if result, ok := m.results[name]; ok {
		return result, nil
	}
	return `{"ok":true}`, nil
}

func TestAgent_SimpleChat(t *testing.T) {
	convey.Convey("Agent 简单对话（无 tool 调用）", t, func() {
		provider := &mockProvider{
			responses: [][]StreamEvent{
				{
					{Type: "content", Content: "你好"},
					{Type: "content", Content: "！"},
					{Type: "done"},
				},
			},
		}
		executor := &mockExecutor{}
		agent := NewAgent(provider, executor)

		var events []StreamEvent
		err := agent.Chat(context.Background(), []Message{
			{Role: RoleUser, Content: "你好"},
		}, func(e StreamEvent) {
			events = append(events, e)
		})

		assert.NoError(t, err)
		assert.Len(t, executor.calls, 0) // 没有 tool 调用

		// 应有 content + done 事件
		contentEvents := 0
		doneEvents := 0
		for _, e := range events {
			if e.Type == "content" {
				contentEvents++
			}
			if e.Type == "done" {
				doneEvents++
			}
		}
		assert.Equal(t, 2, contentEvents)
		assert.Equal(t, 1, doneEvents)
	})
}

func TestAgent_ToolCallLoop(t *testing.T) {
	convey.Convey("Agent tool 调用循环", t, func() {
		provider := &mockProvider{
			responses: [][]StreamEvent{
				// 第一轮：LLM 返回 tool 调用
				{
					{Type: "tool_call", ToolCalls: []ToolCall{
						{ID: "call_1", Function: struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						}{Name: "list_assets", Arguments: `{"asset_type":"ssh"}`}},
					}},
					{Type: "done"},
				},
				// 第二轮：LLM 返回最终回复
				{
					{Type: "content", Content: "找到了2台服务器"},
					{Type: "done"},
				},
			},
		}
		executor := &mockExecutor{
			results: map[string]string{
				"list_assets": `[{"ID":1,"Name":"web-01"},{"ID":2,"Name":"web-02"}]`,
			},
		}
		agent := NewAgent(provider, executor)

		var events []StreamEvent
		err := agent.Chat(context.Background(), []Message{
			{Role: RoleUser, Content: "列出所有SSH服务器"},
		}, func(e StreamEvent) {
			events = append(events, e)
		})

		assert.NoError(t, err)
		// executor 应被调用一次
		assert.Len(t, executor.calls, 1)
		assert.Equal(t, "list_assets", executor.calls[0].Name)

		// 最终应有 content 事件
		hasContent := false
		for _, e := range events {
			if e.Type == "content" {
				hasContent = true
			}
		}
		assert.True(t, hasContent)
	})
}
