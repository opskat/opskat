package runner

import (
	"crypto/rand"
	"net/http"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/ai_provider_entity"
)

// sessionPlaceholder 在自定义请求头的值里展开为本次请求所属会话的标识。
// 对端网关（OpenCode 的 x-opencode-session 是典型）用它做后端亲和与 prompt cache 路由，
// 填死常量能消掉 400，但会把所有会话压到同一条路由上。
const sessionPlaceholder = "{{session}}"

// expandSessionPlaceholder 只认 sessionPlaceholder 一个占位符，其余内容按字面量发送。
func expandSessionPlaceholder(value, sessionID string) string {
	return strings.ReplaceAll(value, sessionPlaceholder, sessionID)
}

// resolveExtraHeaders 把配置好的请求头展开成可以直接写到请求上的键值对。
func resolveExtraHeaders(headers []ai_provider_entity.ExtraHeader, sessionID string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for _, h := range headers {
		out[h.Name] = expandSessionPlaceholder(h.Value, sessionID)
	}
	return out
}

// oneOffSessionID 生成一个只用一次的会话标识，供不属于任何会话的请求（拉模型列表）使用。
// 不复用一个安装级的固定值：那会给第三方网关一个可以跨会话跟踪设备的标识。
func oneOffSessionID() string {
	return rand.Text()
}

// headerInjectingTransport 给每个出站请求补上自定义头。
// 只在请求副本上写，不改调用方持有的 *http.Request（RoundTripper 的契约）。
type headerInjectingTransport struct {
	headers map[string]string
}

func (t *headerInjectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for name, value := range t.headers {
		clone.Header.Set(name, value)
	}
	return http.DefaultTransport.RoundTrip(clone)
}
