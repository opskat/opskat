// Package netproxytest 提供测试用的录制代理：同一端口同时接受 HTTP 代理（CONNECT 隧道与绝对 URI 请求）
// 与 SOCKS5（类似 Clash 的混合端口），记录收到的每个请求，供出站 client 的代理路由测试断言。
//
// 代理不做真实 DNS：目标主机只有经 Forward 登记过才会被转发到对应的本地地址，未登记的目标
// 照样记录，但以 502（HTTP）或 host unreachable（SOCKS5）拒绝。测试目标须用非回环主机名——
// 回环地址与 localhost 永远不经代理（见 golang.org/x/net/http/httpproxy）。
package netproxytest

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/pkg/netproxy"
)

// Protocol 是客户端与录制代理之间使用的代理协议。
type Protocol string

const (
	ProtocolHTTP   Protocol = "http"
	ProtocolSOCKS5 Protocol = "socks5"
)

// Request 是录制代理收到的一次代理请求。
type Request struct {
	Protocol Protocol
	// Method 为 HTTP 代理的请求方法（CONNECT 或 GET 等）；SOCKS5 为 "CONNECT"。
	Method string
	// Target 为目标 host:port，主机部分是客户端交给代理的原样内容（主机名不会被代理解析掉）。
	Target string
	// URL 为 HTTP 代理收到的绝对 URI；CONNECT 与 SOCKS5 为空。
	URL string
	// Username / Password 为客户端向代理出示的凭据；未认证时为空。
	Username string
	Password string
}

// Proxy 是运行中的录制代理，测试结束时自动关闭。
type Proxy struct {
	ln net.Listener

	mu       sync.Mutex
	requests []Request
	routes   map[string]string
	user     string
	pass     string
}

// NewRecordingProxy 在 127.0.0.1 的随机端口上启动录制代理。
func NewRecordingProxy(t testing.TB) *Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen recording proxy: %v", err)
	}
	p := &Proxy{ln: ln, routes: map[string]string{}}
	t.Cleanup(func() { _ = ln.Close() })
	go p.serve()
	return p
}

// Addr 返回代理监听的 host:port。
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// Port 返回代理监听的端口。
func (p *Proxy) Port() int { return p.ln.Addr().(*net.TCPAddr).Port }

// RequireAuth 要求客户端出示用户名与密码：HTTP 代理用 Basic 认证（不符回 407），SOCKS5 用 RFC 1929。
func (p *Proxy) RequireAuth(user, pass string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.user, p.pass = user, pass
}

// Forward 把发往 host（任意端口）的代理请求转发到本地地址 addr。
func (p *Proxy) Forward(host, addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes[host] = addr
}

// Requests 返回目前收到的所有请求（含被拒绝的）。
func (p *Proxy) Requests() []Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Request(nil), p.requests...)
}

// Settings 返回指向本代理的「自定义」设置；调用过 RequireAuth 时带上对应凭据。
// NoProxy 为空，便于测试任意目标都经代理。
func (p *Proxy) Settings(scheme netproxy.Scheme) netproxy.Settings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return netproxy.Settings{
		Mode:     netproxy.ModeCustom,
		Scheme:   scheme,
		Host:     "127.0.0.1",
		Port:     p.Port(),
		Username: p.user,
		Password: p.pass,
	}
}

func (p *Proxy) serve() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(conn)
	}
}

func (p *Proxy) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	if first[0] == 0x05 {
		p.handleSOCKS5(conn, br)
		return
	}
	p.handleHTTP(conn, br)
}

// record 记录请求并返回凭据是否满足 RequireAuth 与目标的转发地址。
func (p *Proxy) record(r Request) (authorized bool, upstream string, routed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, r)
	authorized = p.user == "" || (r.Username == p.user && r.Password == p.pass)
	host, _, err := net.SplitHostPort(r.Target)
	if err != nil {
		return authorized, "", false
	}
	upstream, routed = p.routes[host]
	return authorized, upstream, routed
}

func (p *Proxy) handleHTTP(conn net.Conn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	r := Request{Protocol: ProtocolHTTP, Method: req.Method}
	if req.Method == http.MethodConnect {
		r.Target = req.Host
	} else {
		r.URL = req.URL.String()
		r.Target = req.URL.Host
		if req.URL.Port() == "" {
			r.Target = net.JoinHostPort(req.URL.Hostname(), "80")
		}
	}
	if u, pw, ok := (&http.Request{Header: http.Header{"Authorization": req.Header.Values("Proxy-Authorization")}}).BasicAuth(); ok {
		r.Username, r.Password = u, pw
	}
	authorized, upstream, routed := p.record(r)
	switch {
	case !authorized:
		_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"netproxytest\"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	case !routed:
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	target, err := net.Dial("tcp", upstream)
	if err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	defer func() { _ = target.Close() }()
	if req.Method == http.MethodConnect {
		if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
			return
		}
		pipe(conn, br, target)
		return
	}
	// 绝对 URI 请求：去掉代理专用头后以 origin-form 转给上游，单次请求后关闭连接。
	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	req.Close = true
	if err := req.Write(target); err != nil {
		return
	}
	_, _ = io.Copy(conn, target)
}

func (p *Proxy) handleSOCKS5(conn net.Conn, br *bufio.Reader) {
	// 协商：VER NMETHODS METHODS...
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	p.mu.Lock()
	user, pass := p.user, p.pass
	p.mu.Unlock()
	r := Request{Protocol: ProtocolSOCKS5, Method: http.MethodConnect}
	if user != "" {
		if !containsByte(methods, 0x02) {
			_, _ = conn.Write([]byte{5, 0xff})
			return
		}
		if _, err := conn.Write([]byte{5, 2}); err != nil {
			return
		}
		// RFC 1929：VER ULEN UNAME PLEN PASSWD
		ver := make([]byte, 2)
		if _, err := io.ReadFull(br, ver); err != nil {
			return
		}
		uname := make([]byte, ver[1])
		if _, err := io.ReadFull(br, uname); err != nil {
			return
		}
		plen := make([]byte, 1)
		if _, err := io.ReadFull(br, plen); err != nil {
			return
		}
		passwd := make([]byte, plen[0])
		if _, err := io.ReadFull(br, passwd); err != nil {
			return
		}
		r.Username, r.Password = string(uname), string(passwd)
		if r.Username != user || r.Password != pass {
			// 认证在 CONNECT 请求之前完成，被拒绝的尝试没有目标地址。
			p.record(r)
			_, _ = conn.Write([]byte{1, 1})
			return
		}
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			return
		}
	} else if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	// 请求：VER CMD RSV ATYP DST.ADDR DST.PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1, 4: // IPv4 / IPv6
		size := net.IPv4len
		if req[3] == 4 {
			size = net.IPv6len
		}
		b := make([]byte, size)
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3: // 域名
		l := make([]byte, 1)
		if _, err := io.ReadFull(br, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(br, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(br, portBuf); err != nil {
		return
	}
	r.Target = net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBuf))))
	_, upstream, routed := p.record(r)
	if !routed {
		_, _ = conn.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0}) // host unreachable
		return
	}
	target, err := net.Dial("tcp", upstream)
	if err != nil {
		_, _ = conn.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = target.Close() }()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	pipe(conn, br, target)
}

// pipe 在客户端（含 br 中已缓冲的字节）与上游之间双向转发，直到上游一侧结束。
func pipe(conn net.Conn, br *bufio.Reader, target net.Conn) {
	go func() { _, _ = io.Copy(target, br) }()
	_, _ = io.Copy(conn, target)
}

func containsByte(b []byte, v byte) bool {
	for _, x := range b {
		if x == v {
			return true
		}
	}
	return false
}
