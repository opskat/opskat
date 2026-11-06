package netproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ProbeTimeout 是 Probe 一次测试的总时限；调用方 ctx 的截止时间更早时以它为准。
const ProbeTimeout = 15 * time.Second

// ViaDirect 是 ProbeResult.Via 在不经代理时的取值。
const ViaDirect = "direct"

// ProbeReason 是测试失败的原因分类，取值稳定，供界面映射文案。
type ProbeReason string

const (
	// ReasonProxyUnreachable 连不上代理（拒绝连接、代理主机名无法解析等）。
	ReasonProxyUnreachable ProbeReason = "proxy_unreachable"
	// ReasonProxyAuth 代理要求认证或认证失败（HTTP 407 / SOCKS5 认证被拒）。
	ReasonProxyAuth ProbeReason = "proxy_auth"
	// ReasonDNS 目标域名解析失败。
	ReasonDNS ProbeReason = "dns"
	// ReasonTLS TLS 握手失败（证书不受信任、对端不是 TLS 等）。
	ReasonTLS ProbeReason = "tls"
	// ReasonTimeout 超时。
	ReasonTimeout ProbeReason = "timeout"
	// ReasonOther 其他错误，见 Detail。
	ReasonOther ProbeReason = "other"
)

// ProbeResult 是 Probe 的结果。
type ProbeResult struct {
	// OK 表示收到了目标的 HTTP 响应（任何状态码）。
	OK bool
	// Via 为所经代理的 scheme://host:port（不含凭据），直连时为 ViaDirect。
	Via string
	// Host 为目标的 host[:port]。
	Host string
	// Status 为收到的 HTTP 状态码；没有收到响应时为 0。
	Status int
	// Elapsed 为从发起到收到响应头或失败的耗时。
	Elapsed time.Duration
	// Reason 为失败原因；OK 时为空。
	Reason ProbeReason
	// Detail 为失败时的原始错误文本；OK 时为空。
	Detail string
}

// SOCKS5 认证被拒时 net/http 内置 SOCKS 客户端返回的错误文本：它没有导出可判别的错误类型，
// 只能比对文本；Go 升级若改了文本，TestProbeProxyAuth 的 SOCKS5 用例会失败。
var socksAuthErrors = []string{"no acceptable authentication methods", "username/password authentication failed"}

// Probe 按 s 对 target（http/https URL，调用方已校验）发一个不带凭据、不跟随重定向的 GET，
// 判断经 s 选出的代理（或直连）能否连通。使用按 s 新建的一次性传输，不影响 Transport，也不保存 s。
// 收到目标的任何 HTTP 响应即为成功；明文目标经代理时收到的 407 是代理自己的响应，归为 ReasonProxyAuth。
func Probe(ctx context.Context, s Settings, target *url.URL) ProbeResult {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	u := *target
	u.User = nil // 不带任何凭据：URL 里的 user:pass 会被 net/http 转成 Basic 认证头
	res := ProbeResult{Via: ViaDirect, Host: u.Host}
	proxyURL, err := s.ProxyFunc()(&u)
	if err != nil {
		res.Reason, res.Detail = ReasonOther, err.Error()
		return res
	}
	if proxyURL != nil {
		res.Via = (&url.URL{Scheme: proxyURL.Scheme, Host: proxyURL.Host}).String()
	}

	var sig probeSignals
	tr := NewTransport(s)
	defer tr.CloseIdleConnections()
	dial := tr.DialContext
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil && proxyURL != nil {
			// 经代理时传输只拨代理的地址。
			sig.set(func() { sig.proxyDialErr = true })
		}
		return conn, err
	}
	tr.OnProxyConnectResponse = func(_ context.Context, _ *url.URL, _ *http.Request, resp *http.Response) error {
		sig.set(func() { sig.connectStatus = resp.StatusCode })
		return nil
	}
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err != nil {
				sig.set(func() { sig.tlsErr = true })
			}
		},
	})
	client := &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		res.Reason, res.Detail = ReasonOther, err.Error()
		return res
	}
	start := time.Now()
	resp, err := client.Do(req)
	res.Elapsed = time.Since(start)
	if err != nil {
		res.Reason, res.Detail = sig.classify(err), err.Error()
		return res
	}
	_ = resp.Body.Close()
	res.Status = resp.StatusCode
	if proxyURL != nil && u.Scheme == "http" && resp.StatusCode == http.StatusProxyAuthRequired {
		res.Reason, res.Detail = ReasonProxyAuth, resp.Status
		return res
	}
	res.OK = true
	return res
}

// probeSignals 记录传输内部各阶段的失败点；钩子在传输的拨号 goroutine 中调用，取消时可能晚于 client.Do 返回，故加锁。
type probeSignals struct {
	mu            sync.Mutex
	proxyDialErr  bool
	connectStatus int
	tlsErr        bool
}

func (p *probeSignals) set(f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f()
}

func (p *probeSignals) classify(err error) ProbeReason {
	p.mu.Lock()
	defer p.mu.Unlock()
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return ReasonTimeout
	case p.proxyDialErr:
		return ReasonProxyUnreachable
	case p.connectStatus == http.StatusProxyAuthRequired || isSOCKSAuthError(err):
		return ReasonProxyAuth
	case p.tlsErr:
		return ReasonTLS
	case errors.As(err, &dnsErr):
		return ReasonDNS
	default:
		return ReasonOther
	}
}

func isSOCKSAuthError(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) || !strings.HasPrefix(opErr.Op, "socks") || opErr.Err == nil {
		return false
	}
	for _, text := range socksAuthErrors {
		if opErr.Err.Error() == text {
			return true
		}
	}
	return false
}
