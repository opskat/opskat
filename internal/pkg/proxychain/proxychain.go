package proxychain

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/pkg/netdial"
)

const (
	LayerSSH        = "ssh"
	LayerSOCKS5     = "socks5"
	LayerHTTPTunnel = "http_tunnel"
)

type DialFunc func(ctx context.Context, addr string) (net.Conn, error)

type Chain struct {
	Layers []Layer
	Direct DialFunc
}

type Layer struct {
	Type           string
	Name           string
	Host           string
	Port           int
	Username       string
	Password       string
	URL            string
	Token          string
	TimeoutSeconds int
	// EstablishTimeout 是建立这一层时单个步骤的时限：SSH 层的握手认证、经它打开到下一
	// 地址的通道，SOCKS5 层的握手。0 表示只受调用方 ctx 约束。SSH 握手里等人回答 MFA
	// 的时间不计（WithDialTimeout / AwaitAnswer）。
	EstablishTimeout time.Duration
	// Handshake 完成该 SSH 层的握手认证（SSH 层必填；由 ssh_svc.ProxyChainHandshake 构造，
	// 口令 / 密钥 / Agent 认证与跳板 MFA 都在里面）。失败时 conn 由本包关闭。
	Handshake func(ctx context.Context, conn net.Conn, addr string) (*ssh.Client, error)
}

func (c Chain) Dial(ctx context.Context, targetAddr string) (net.Conn, error) {
	dial := c.Direct
	if dial == nil {
		dial = func(ctx context.Context, addr string) (net.Conn, error) {
			return (&netdial.Dialer{}).DialContext(ctx, "tcp", addr)
		}
	}
	layers := normalizeLayers(c.Layers)
	var closers []io.Closer
	for i, layer := range layers {
		prior := dial
		switch layer.Type {
		case LayerSSH:
			layer := layer
			dial = func(ctx context.Context, addr string) (net.Conn, error) {
				client, err := dialSSHLayer(ctx, prior, layer)
				if err != nil {
					return nil, err
				}
				// client.Dial 不看 ctx：跳板机连不上下一地址时要等它那边的 TCP 超时才返回。
				openCtx, cancel := stepContext(ctx, layer.EstablishTimeout)
				defer cancel()
				conn, err := client.DialContext(openCtx, "tcp", addr)
				if err != nil {
					_ = client.Close()
					return nil, fmt.Errorf("经 SSH 代理层 %s 连接 %s 失败: %w", layerAddr(layer), addr, err)
				}
				return &connWithClosers{Conn: deadlineIgnoredConn{Conn: conn}, closers: []io.Closer{client}}, nil
			}
		case LayerSOCKS5:
			layer := layer
			dial = func(ctx context.Context, addr string) (net.Conn, error) {
				proxyAddr := layerAddr(layer)
				conn, err := prior(ctx, proxyAddr)
				if err != nil {
					return nil, err
				}
				// socks5Connect 是阻塞读写，靠关闭连接来响应 ctx 结束。
				hsCtx, cancel := stepContext(ctx, layer.EstablishTimeout)
				defer cancel()
				stop := context.AfterFunc(hsCtx, func() { _ = conn.Close() })
				err = socks5Connect(conn, addr, layer.Username, layer.Password)
				if !stop() {
					return nil, fmt.Errorf("SOCKS5 代理层 %s 握手未完成: %w", proxyAddr, hsCtx.Err())
				}
				if err != nil {
					_ = conn.Close()
					return nil, err
				}
				return conn, nil
			}
		case LayerHTTPTunnel:
			if i != 0 {
				return nil, fmt.Errorf("HTTP 隧道必须是代理链第一层")
			}
			layer := layer
			dial = func(ctx context.Context, addr string) (net.Conn, error) {
				return dialHTTPTunnel(ctx, layer, addr)
			}
		default:
			return nil, fmt.Errorf("不支持的代理链层类型: %s", layer.Type)
		}
		_ = closers
	}
	conn, err := dial(ctx, targetAddr)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// stepContext 给建立某一层的一个步骤套上该层的时限。
func stepContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func layerAddr(layer Layer) string {
	return net.JoinHostPort(layer.Host, strconv.Itoa(layer.Port))
}

func normalizeLayers(layers []Layer) []Layer {
	out := make([]Layer, 0, len(layers))
	for _, layer := range layers {
		if strings.TrimSpace(layer.Type) != "" {
			out = append(out, layer)
		}
	}
	return out
}

func dialSSHLayer(ctx context.Context, dial DialFunc, layer Layer) (*ssh.Client, error) {
	if layer.Handshake == nil {
		return nil, fmt.Errorf("SSH 代理层缺少认证配置")
	}
	addr := layerAddr(layer)
	conn, err := dial(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("连接 SSH 代理层失败: %w", err)
	}
	// ssh.NewClientConn 自己没有握手时限（ClientConfig.Timeout 只管 ssh.Dial 的 TCP 连接），
	// 对端连上后不再应答就会一直等下去。握手里等人回答这台跳板的 MFA 时（AwaitAnswer）
	// 不计时。
	hsCtx, cancel := WithDialTimeout(ctx, layer.EstablishTimeout)
	defer cancel()
	done := make(chan struct{})
	var client *ssh.Client
	var hsErr error
	go func() {
		defer close(done)
		client, hsErr = layer.Handshake(hsCtx, conn, addr)
	}()
	select {
	case <-hsCtx.Done():
		_ = conn.Close()
		return nil, fmt.Errorf("SSH 代理层 %s 握手未完成: %w", addr, hsCtx.Err())
	case <-done:
	}
	if hsErr != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SSH 代理层握手失败: %w", hsErr)
	}
	return client, nil
}

type connWithClosers struct {
	net.Conn
	closers []io.Closer
	once    sync.Once
}

func (c *connWithClosers) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		for _, closer := range c.closers {
			if closeErr := closer.Close(); err == nil {
				err = closeErr
			}
		}
	})
	return err
}

// deadlineIgnoredConn wraps SSH direct-tcpip channels for database drivers.
// golang.org/x/crypto/ssh currently returns a tcpChan whose SetDeadline
// methods return "deadline not supported"; MongoDB and other drivers treat
// that as a hard connection failure even though reads/writes work fine.
type deadlineIgnoredConn struct {
	net.Conn
}

func (c deadlineIgnoredConn) SetDeadline(time.Time) error      { return nil }
func (c deadlineIgnoredConn) SetReadDeadline(time.Time) error  { return nil }
func (c deadlineIgnoredConn) SetWriteDeadline(time.Time) error { return nil }

func socks5Connect(conn net.Conn, targetAddr, username, password string) error {
	host, portText, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("目标端口无效: %s", portText)
	}
	methods := []byte{0x00}
	if username != "" {
		methods = append(methods, 0x02)
	}
	if _, err := conn.Write(append([]byte{0x05, byte(len(methods))}, methods...)); err != nil {
		return err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return err
	}
	if resp[0] != 0x05 || resp[1] == 0xff {
		return fmt.Errorf("SOCKS5 代理不接受认证方式")
	}
	if resp[1] == 0x02 {
		if len(username) > 255 || len(password) > 255 {
			return fmt.Errorf("SOCKS5 用户名或密码过长")
		}
		auth := make([]byte, 0, 2+len(username)+1+len(password))
		auth = append(auth, 0x01, byte(len(username)))
		auth = append(auth, username...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, password...)
		if _, err := conn.Write(auth); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, resp); err != nil {
			return err
		}
		if resp[1] != 0x00 {
			return fmt.Errorf("SOCKS5 代理认证失败")
		}
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("SOCKS5 目标主机名过长")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	var portBytes [2]byte
	binary.BigEndian.PutUint16(portBytes[:], uint16(port))
	req = append(req, portBytes[:]...)
	if _, err := conn.Write(req); err != nil {
		return err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[1] != 0x00 {
		return fmt.Errorf("SOCKS5 代理连接失败，响应码: %d", header[1])
	}
	var skip int
	switch header[3] {
	case 0x01:
		skip = 4
	case 0x03:
		lenBuf := []byte{0}
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return err
		}
		skip = int(lenBuf[0])
	case 0x04:
		skip = 16
	default:
		return fmt.Errorf("SOCKS5 地址类型无效")
	}
	if skip > 0 {
		if _, err := io.CopyN(io.Discard, conn, int64(skip)); err != nil {
			return err
		}
	}
	if _, err := io.CopyN(io.Discard, conn, 2); err != nil {
		return err
	}
	return nil
}

type httpTunnelConn struct {
	session string
	baseURL string
	token   string
	client  *http.Client
	reader  *bytes.Reader
	closed  bool
	mu      sync.Mutex
}

func dialHTTPTunnel(ctx context.Context, layer Layer, targetAddr string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("HTTP 隧道目标端口无效")
	}
	if _, err := url.ParseRequestURI(layer.URL); err != nil {
		return nil, fmt.Errorf("HTTP 隧道 URL 无效: %w", err)
	}
	timeoutSeconds := layer.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 10
	}
	session := fmt.Sprintf("%d%x", time.Now().UnixNano(), rand.Uint64())
	conn := &httpTunnelConn{
		session: session,
		baseURL: strings.TrimSpace(layer.URL),
		token:   strings.TrimSpace(layer.Token),
		client:  &http.Client{Timeout: 15 * time.Second},
		reader:  bytes.NewReader(nil),
	}
	q := map[string]string{
		"dbx_action":          "open",
		"dbx_session":         session,
		"dbx_target_host":     host,
		"dbx_target_port":     strconv.Itoa(port),
		"dbx_connect_timeout": strconv.Itoa(timeoutSeconds),
	}
	if err := conn.do(ctx, q, nil, nil); err != nil {
		return nil, err
	}
	return conn, nil
}

func (c *httpTunnelConn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return 0, net.ErrClosed
		}
		if c.reader.Len() > 0 {
			n, err := c.reader.Read(p)
			c.mu.Unlock()
			return n, err
		}
		c.mu.Unlock()
		var body []byte
		status, err := c.doStatus(context.Background(), map[string]string{
			"dbx_action":  "read",
			"dbx_session": c.session,
			"dbx_wait_ms": "1000",
		}, nil, &body)
		if err != nil {
			return 0, err
		}
		if status == http.StatusNoContent {
			continue
		}
		c.mu.Lock()
		c.reader = bytes.NewReader(body)
		c.mu.Unlock()
	}
}

func (c *httpTunnelConn) Write(p []byte) (int, error) {
	if err := c.do(context.Background(), map[string]string{
		"dbx_action":  "write",
		"dbx_session": c.session,
	}, bytes.NewReader(p), nil); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *httpTunnelConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.do(context.Background(), map[string]string{
		"dbx_action":  "close",
		"dbx_session": c.session,
	}, nil, nil)
}

func (c *httpTunnelConn) LocalAddr() net.Addr              { return dummyAddr("http-tunnel-local") }
func (c *httpTunnelConn) RemoteAddr() net.Addr             { return dummyAddr("http-tunnel-remote") }
func (c *httpTunnelConn) SetDeadline(time.Time) error      { return nil }
func (c *httpTunnelConn) SetReadDeadline(time.Time) error  { return nil }
func (c *httpTunnelConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return "http-tunnel" }
func (a dummyAddr) String() string  { return string(a) }

func (c *httpTunnelConn) do(ctx context.Context, q map[string]string, body io.Reader, out *[]byte) error {
	_, err := c.doStatus(ctx, q, body, out)
	return err
}

func (c *httpTunnelConn) doStatus(ctx context.Context, q map[string]string, body io.Reader, out *[]byte) (int, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return 0, err
	}
	query := u.Query()
	for k, v := range q {
		query.Set(k, v)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return 0, err
	}
	if c.token != "" {
		req.Header.Set("X-DBX-Tunnel-Token", c.token)
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode == http.StatusGone {
		return resp.StatusCode, errors.New("HTTP 隧道会话已关闭")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("HTTP 隧道请求失败: HTTP %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		*out = data
	}
	return resp.StatusCode, nil
}

func DialTLS(ctx context.Context, dial DialFunc, addr string, tlsConfig *tls.Config) (net.Conn, error) {
	conn, err := dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	cfg := tlsConfig.Clone()
	if cfg.ServerName == "" {
		cfg.ServerName = host
	}
	tlsConn := tls.Client(conn, cfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func ReadHTTPResponse(conn net.Conn) (*http.Response, error) {
	return http.ReadResponse(bufio.NewReader(conn), nil)
}
