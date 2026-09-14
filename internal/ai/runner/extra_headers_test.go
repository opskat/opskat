package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cago-frame/agents/provider"
	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
)

func TestExpandSessionPlaceholder(t *testing.T) {
	t.Run("展开为给定的会话标识", func(t *testing.T) {
		got := expandSessionPlaceholder("{{session}}", "sess-abc")
		assert.Equal(t, "sess-abc", got)
	})

	t.Run("同一个值里出现多次都展开", func(t *testing.T) {
		got := expandSessionPlaceholder("a-{{session}}-b-{{session}}", "s1")
		assert.Equal(t, "a-s1-b-s1", got)
	})

	t.Run("没有占位符的值原样发送，不做转义", func(t *testing.T) {
		got := expandSessionPlaceholder("Bearer {literal} $x %s", "s1")
		assert.Equal(t, "Bearer {literal} $x %s", got)
	})
}

func TestBuildProvider_OpenAISendsExtraHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p, err := BuildProvider(ProviderOptions{
		Entity: &ai_provider_entity.AIProvider{Type: "openai", APIBase: srv.URL},
		APIKey: "sk-test",
		ExtraHeaders: []ai_provider_entity.ExtraHeader{
			{Name: "x-opencode-session", Value: "{{session}}"},
			{Name: "X-Trace", Value: "t-9"},
		},
		SessionID: "sess-abc",
	})
	require.NoError(t, err)

	_, err = p.ChatCompletion(context.Background(), &provider.CompletionRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	require.NoError(t, err)

	assert.Equal(t, "sess-abc", got.Get("X-Opencode-Session"), "{{session}} 必须展开为本次会话的标识")
	assert.Equal(t, "t-9", got.Get("X-Trace"))
	assert.Equal(t, "Bearer sk-test", got.Get("Authorization"), "自定义头不得挤掉鉴权头")
}

func TestBuildProvider_NoExtraHeadersLeavesRequestUntouched(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	p, err := BuildProvider(ProviderOptions{
		Entity: &ai_provider_entity.AIProvider{Type: "openai", APIBase: srv.URL},
		APIKey: "sk-test",
	})
	require.NoError(t, err)

	_, err = p.ChatCompletion(context.Background(), &provider.CompletionRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	require.NoError(t, err)

	assert.Empty(t, got.Get("X-Opencode-Session"))
	assert.Equal(t, "Bearer sk-test", got.Get("Authorization"))
}

func TestFetchModels_SendsExtraHeadersWithOneOffSession(t *testing.T) {
	seen := make([]string, 0, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Opencode-Session"))
		assert.Equal(t, "t-9", r.Header.Get("X-Trace"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o"}]}`)
	}))
	defer srv.Close()

	headers := []ai_provider_entity.ExtraHeader{
		{Name: "x-opencode-session", Value: "{{session}}"},
		{Name: "X-Trace", Value: "t-9"},
	}
	for range 2 {
		models, err := FetchModels(FetchModelsOptions{APIBase: srv.URL, APIKey: "sk-test", ExtraHeaders: headers})
		require.NoError(t, err)
		require.Len(t, models, 1)
	}

	require.Len(t, seen, 2)
	assert.NotEmpty(t, seen[0], "要求会话头的网关也得能拉到模型列表")
	assert.NotEqual(t, seen[0], seen[1], "拉列表用一次性随机值，不留可跨次跟踪的固定标识")
}
