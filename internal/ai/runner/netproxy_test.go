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
	"github.com/opskat/opskat/internal/pkg/netproxy"
	"github.com/opskat/opskat/internal/pkg/netproxy/netproxytest"
)

const (
	openAIChatBody    = `{"id":"1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	anthropicChatBody = `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	modelsBody        = `{"data":[{"id":"m1"}]}`

	// 非回环主机名：回环地址永远不经代理（x/net httpproxy），用它测不出路由。
	providerHost = "ai-provider.example.test"
)

// setupProxyEnv 把进程级代理设置切到 mode（custom 指向录制代理），
// 并让 providerHost 经录制代理转发到返回 body 的本地 upstream。结束时恢复原设置。
func setupProxyEnv(t *testing.T, mode netproxy.Mode, body string) *netproxytest.Proxy {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)

	rec := netproxytest.NewRecordingProxy(t)
	rec.Forward(providerHost, upstream.Listener.Addr().String())

	prev := netproxy.Get()
	t.Cleanup(func() { netproxy.Set(prev) })
	if mode == netproxy.ModeCustom {
		netproxy.Set(rec.Settings(netproxy.SchemeHTTP))
	} else {
		netproxy.Set(netproxy.Settings{Mode: mode})
	}
	return rec
}

func chatOnce(e *ai_provider_entity.AIProvider, model string) error {
	p, err := BuildProvider(ProviderOptions{Entity: e, APIKey: "sk-test", SessionID: "s1"})
	if err != nil {
		return err
	}
	_, err = p.ChatCompletion(context.Background(), &provider.CompletionRequest{
		Model:    model,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	return err
}

func TestAIOutbound_ThroughNetproxy(t *testing.T) {
	base := "http://" + providerHost
	cases := []struct {
		name string
		body string
		run  func(t *testing.T) error
	}{
		{"OpenAI 对话 无自定义头", openAIChatBody, func(t *testing.T) error {
			return chatOnce(&ai_provider_entity.AIProvider{Type: "openai", APIBase: base + "/v1"}, "gpt-4o")
		}},
		{"OpenAI 对话 有自定义头", openAIChatBody, func(t *testing.T) error {
			e := &ai_provider_entity.AIProvider{Type: "openai", APIBase: base + "/v1"}
			require.NoError(t, e.SetExtraHeaders([]ai_provider_entity.ExtraHeader{{Name: "X-Trace", Value: "t-9"}}))
			return chatOnce(e, "gpt-4o")
		}},
		{"Anthropic 对话", anthropicChatBody, func(t *testing.T) error {
			return chatOnce(&ai_provider_entity.AIProvider{Type: "anthropic", APIBase: base}, "claude-sonnet-4-5")
		}},
		{"拉模型 OpenAI", modelsBody, func(t *testing.T) error {
			_, err := FetchModels(FetchModelsOptions{ProviderType: "openai", APIBase: base + "/v1", APIKey: "sk-test"})
			return err
		}},
		{"拉模型 Anthropic", modelsBody, func(t *testing.T) error {
			_, err := FetchModels(FetchModelsOptions{ProviderType: "anthropic", APIBase: base, APIKey: "sk-test"})
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name+"/自定义代理下经过代理", func(t *testing.T) {
			rec := setupProxyEnv(t, netproxy.ModeCustom, c.body)
			require.NoError(t, c.run(t))
			reqs := rec.Requests()
			require.NotEmpty(t, reqs, "请求必须到达录制代理")
			assert.Contains(t, reqs[0].Target, providerHost)
		})
		t.Run(c.name+"/直连下不经过代理", func(t *testing.T) {
			rec := setupProxyEnv(t, netproxy.ModeDirect, c.body)
			// 直连下测试主机名无法解析，请求会失败；这里只关心录制代理没有看到任何请求。
			_ = c.run(t)
			assert.Empty(t, rec.Requests())
		})
	}
}
