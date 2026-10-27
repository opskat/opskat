package netproxy

import (
	"net/http"
	"net/url"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/pkg/netdial"
	"go.uber.org/zap"
)

// applied 是当前生效的设置与按它构建的传输，整体替换，保证读到的两者一致。
type applied struct {
	settings  Settings
	transport *http.Transport
}

var (
	mu      sync.RWMutex
	current = &applied{settings: Default(), transport: NewTransport(Default())}
)

// Set 让此后经 Transport 发出的请求按 s 选择代理，无需重启。已在进行的请求（如流式 AI 回复）沿原连接完成；
// 旧设置下的空闲连接随即关闭。s 应已通过 Validate。
func Set(s Settings) {
	next := &applied{settings: s, transport: NewTransport(s)}
	mu.Lock()
	prev := current
	current = next
	mu.Unlock()
	prev.transport.CloseIdleConnections()
	logger.Default().Info("network proxy settings applied",
		zap.String("mode", string(s.Mode)),
		zap.String("scheme", string(s.Scheme)),
		zap.String("host", s.Host),
		zap.Int("port", s.Port),
		zap.Bool("auth", s.Username != ""))
}

// Get 返回当前生效的设置；未调用过 Set 时为 Default()。
func Get() Settings {
	mu.RLock()
	defer mu.RUnlock()
	return current.settings
}

// Transport 返回进程内共用的外网出站传输：每个请求交给最近一次 Set 构建的传输处理。
// 供 AI、检查更新、扩展商店、备份等外网 client 使用；资产连接不要用它。
func Transport() http.RoundTripper { return liveTransport{} }

type liveTransport struct{}

func (liveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	mu.RLock()
	tr := current.transport
	mu.RUnlock()
	return tr.RoundTrip(req)
}

// NewTransport 按 s 构建独立的传输（http.DefaultTransport 的克隆，拨号走 netdial），
// 供用未保存的设置做一次性请求（如测试连接）；不影响 Transport 与 http.DefaultTransport。
func NewTransport(s Settings) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	proxy := s.ProxyFunc()
	tr.Proxy = func(req *http.Request) (*url.URL, error) { return proxy(req.URL) }
	tr.DialContext = netdial.Default().DialContext
	return tr
}
