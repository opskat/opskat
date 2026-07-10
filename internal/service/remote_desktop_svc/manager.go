package remote_desktop_svc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/executil"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	rdpclient "github.com/x90skysn3k/grdp/client"
	"go.uber.org/zap"
)

type rdpAuthenticator interface {
	Authenticate(ctx context.Context, address, username, password string) error
}

type grdpAuthenticator struct{}

func (grdpAuthenticator) Authenticate(ctx context.Context, address, username, password string) error {
	client := &rdpclient.RdpClient{}
	defer client.Close()
	if err := client.LoginAuthOnly(ctx, address, username, password); err != nil {
		return fmt.Errorf("RDP 凭据认证失败: %w", err)
	}
	return nil
}

type Manager struct {
	assetRepo        asset_repo.AssetRepo
	resolver         *credential_resolver.Resolver
	rdpAuthenticator rdpAuthenticator

	mu       sync.Mutex
	sessions map[string]*Session
}

type Session struct {
	ID             string `json:"id"`
	AssetID        int64  `json:"assetId"`
	AssetType      string `json:"assetType"`
	AssetName      string `json:"assetName"`
	WebSocketURL   string `json:"webSocketUrl"`
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	FileSSHAssetID int64  `json:"fileSshAssetId"`
	FileEnabled    bool   `json:"fileEnabled"`
	FileStatus     string `json:"fileStatus"`
	Status         string `json:"status"`
	Message        string `json:"message,omitempty"`

	done    chan struct{}
	proxy   *tcpWebSocketProxy
	rdpFile string
	cmd     *exec.Cmd
}

type ConnectOptions struct {
	WebSocketBaseURL string
}

func NewManager(repo asset_repo.AssetRepo) *Manager {
	if repo == nil {
		repo = asset_repo.Asset()
	}
	return &Manager{
		assetRepo:        repo,
		resolver:         credential_resolver.Default(),
		rdpAuthenticator: grdpAuthenticator{},
		sessions:         make(map[string]*Session),
	}
}

func (m *Manager) TestRDPAuthentication(
	ctx context.Context,
	target string,
	layers []proxychain.Layer,
	username string,
	password string,
) error {
	forward, err := startTCPForward(ctx, target, layers)
	if err != nil {
		return fmt.Errorf("启动 RDP 测试代理转发失败: %w", err)
	}
	defer forward.Close()
	return m.rdpAuthenticator.Authenticate(ctx, forward.Addr(), username, password)
}

func (m *Manager) Connect(ctx context.Context, assetID int64, opts ConnectOptions) (*Session, error) {
	asset, err := m.assetRepo.Find(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("读取远程桌面资产失败: %w", err)
	}
	switch asset.Type {
	case asset_entity.AssetTypeVNC:
		return m.connectVNC(ctx, asset)
	case asset_entity.AssetTypeRDP:
		return m.connectRDP(ctx, asset)
	default:
		return nil, fmt.Errorf("资产不是远程桌面类型: %s", asset.Type)
	}
}

func (m *Manager) connectVNC(ctx context.Context, asset *asset_entity.Asset) (*Session, error) {
	cfg, err := asset.GetVNCConfig()
	if err != nil {
		return nil, err
	}
	password, err := m.resolver.ResolvePasswordGeneric(ctx, cfg)
	if err != nil {
		return nil, err
	}
	layers, err := m.resolver.ResolveProxyChain(ctx, cfg.ProxyChain, 5)
	if err != nil {
		return nil, err
	}
	proxy := newTCPWebSocketProxy(net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), layers)
	wsURL, err := proxy.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("启动 VNC 代理失败: %w", err)
	}
	session := &Session{
		ID:             uuid.NewString(),
		AssetID:        asset.ID,
		AssetType:      asset.Type,
		AssetName:      asset.Name,
		WebSocketURL:   wsURL,
		Username:       cfg.Username,
		Password:       password,
		FileSSHAssetID: cfg.FileSSHAssetID,
		FileEnabled:    cfg.FileSSHAssetID > 0,
		FileStatus:     fileStatus(cfg.FileSSHAssetID),
		Status:         "connecting",
		done:           make(chan struct{}),
		proxy:          proxy,
	}
	m.store(session)
	return session, nil
}

func (m *Manager) connectRDP(ctx context.Context, asset *asset_entity.Asset) (*Session, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("RDP 当前使用 Windows 系统远程桌面客户端，仅支持 Windows")
	}
	cfg, err := asset.GetRDPConfig()
	if err != nil {
		return nil, err
	}
	password, err := m.resolver.ResolvePasswordGeneric(ctx, cfg)
	if err != nil {
		return nil, err
	}
	layers, err := m.resolver.ResolveProxyChain(ctx, cfg.ProxyChain, 5)
	if err != nil {
		return nil, err
	}
	targetAddr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	forward, err := startTCPForward(ctx, targetAddr, layers)
	if err != nil {
		return nil, fmt.Errorf("启动 RDP 代理转发失败: %w", err)
	}
	host, port, err := net.SplitHostPort(forward.Addr())
	if err != nil {
		forward.Close()
		return nil, err
	}
	if err := storeRDPCredentials(ctx, host, cfg.Username, password); err != nil {
		forward.Close()
		return nil, err
	}
	rdpFile, err := writeRDPFile(asset.ID, host, port, cfg)
	if err != nil {
		forward.Close()
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "mstsc.exe", rdpFile) //nolint:gosec // The executable is fixed and the RDP file is created in the application temp directory.
	executil.HideConsoleWindow(cmd)
	if err := cmd.Start(); err != nil {
		forward.Close()
		_ = os.Remove(rdpFile)
		return nil, fmt.Errorf("启动 mstsc 失败: %w", err)
	}
	session := &Session{
		ID:             uuid.NewString(),
		AssetID:        asset.ID,
		AssetType:      asset.Type,
		AssetName:      asset.Name,
		FileSSHAssetID: cfg.FileSSHAssetID,
		FileEnabled:    cfg.FileSSHAssetID > 0,
		FileStatus:     fileStatus(cfg.FileSSHAssetID),
		Status:         "external",
		Message:        "已通过 Windows 远程桌面客户端打开 RDP 会话",
		done:           make(chan struct{}),
		proxy:          &tcpWebSocketProxy{forward: forward},
		rdpFile:        rdpFile,
		cmd:            cmd,
	}
	m.store(session)
	go func() {
		_ = cmd.Wait()
		m.Disconnect(session.ID)
	}()
	return session, nil
}

func (m *Manager) store(session *Session) {
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()
}

func (m *Manager) Disconnect(sessionID string) {
	m.mu.Lock()
	session := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if session != nil {
		session.close()
	}
}

func (m *Manager) Cleanup() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Disconnect(id)
	}
}

func (s *Session) close() {
	select {
	case <-s.done:
		return
	default:
		close(s.done)
	}
	if s.proxy != nil {
		s.proxy.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.rdpFile != "" {
		_ = os.Remove(s.rdpFile)
	}
}

func fileStatus(id int64) string {
	if id > 0 {
		return "已启用 SSH/SFTP 文件通道"
	}
	return "未配置 SSH/SFTP 文件通道，文件上传下载不可用"
}

type tcpWebSocketProxy struct {
	target  string
	layers  []proxychain.Layer
	server  *http.Server
	forward *tcpForward
	done    chan struct{}
	once    sync.Once
}

func newTCPWebSocketProxy(target string, layers []proxychain.Layer) *tcpWebSocketProxy {
	return &tcpWebSocketProxy{target: target, layers: layers, done: make(chan struct{})}
}

func (p *tcpWebSocketProxy) Start(ctx context.Context) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p.handleWebSocket(ctx, w, r)
	})
	p.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := p.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			zap.L().Warn("remote desktop websocket tcp proxy failed", zap.Error(err))
		}
	}()
	return "ws://" + ln.Addr().String(), nil
}

func (p *tcpWebSocketProxy) handleWebSocket(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
		Subprotocols:   []string{"binary"},
	})
	if err != nil {
		return
	}
	defer func() { _ = ws.Close(websocket.StatusNormalClosure, "") }()
	tcp, err := (proxychain.Chain{Layers: p.layers}).Dial(ctx, p.target)
	if err != nil {
		zap.L().Warn("remote desktop tcp target dial failed", zap.String("target", p.target), zap.Error(err))
		return
	}
	defer func() { _ = tcp.Close() }()
	errCh := make(chan error, 2)
	go func() {
		for {
			typ, data, err := ws.Read(r.Context())
			if err != nil {
				errCh <- err
				return
			}
			if typ == websocket.MessageBinary {
				_, err = tcp.Write(data)
				if err != nil {
					errCh <- err
					return
				}
			}
		}
	}()
	go func() {
		buf := make([]byte, 32768)
		for {
			n, err := tcp.Read(buf)
			if err != nil {
				errCh <- err
				return
			}
			if err := ws.Write(r.Context(), websocket.MessageBinary, buf[:n]); err != nil {
				errCh <- err
				return
			}
		}
	}()
	<-errCh
}

func (p *tcpWebSocketProxy) Close() {
	p.once.Do(func() {
		if p.done != nil {
			close(p.done)
		}
		if p.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = p.server.Shutdown(ctx)
			cancel()
		}
		if p.forward != nil {
			p.forward.Close()
		}
	})
}

type tcpForward struct {
	listener net.Listener
	target   string
	layers   []proxychain.Layer
	done     chan struct{}
}

func startTCPForward(ctx context.Context, target string, layers []proxychain.Layer) (*tcpForward, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f := &tcpForward{listener: ln, target: target, layers: layers, done: make(chan struct{})}
	go f.acceptLoop(ctx)
	return f, nil
}

func (f *tcpForward) Addr() string { return f.listener.Addr().String() }

func (f *tcpForward) Close() {
	select {
	case <-f.done:
		return
	default:
		close(f.done)
	}
	_ = f.listener.Close()
}

func (f *tcpForward) acceptLoop(ctx context.Context) {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(ctx, conn)
	}
}

func (f *tcpForward) handle(ctx context.Context, inbound net.Conn) {
	defer func() { _ = inbound.Close() }()
	outbound, err := (proxychain.Chain{Layers: f.layers}).Dial(ctx, f.target)
	if err != nil {
		zap.L().Warn("remote desktop target dial failed", zap.String("target", f.target), zap.Error(err))
		return
	}
	defer func() { _ = outbound.Close() }()
	errCh := make(chan error, 2)
	go func() { _, err := io.Copy(outbound, inbound); errCh <- err }()
	go func() { _, err := io.Copy(inbound, outbound); errCh <- err }()
	<-errCh
}

func storeRDPCredentials(ctx context.Context, host, username, password string) error {
	if strings.TrimSpace(password) == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "cmdkey.exe", "/generic:TERMSRV/"+host, "/user:"+username, "/pass:"+password) //nolint:gosec // cmdkey.exe is fixed and receives validated RDP credentials.
	executil.HideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("保存 RDP 凭据失败: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeRDPFile(assetID int64, host, port string, cfg *asset_entity.RDPConfig) (string, error) {
	width := cfg.ScreenWidth
	if width <= 0 {
		width = 1280
	}
	height := cfg.ScreenHeight
	if height <= 0 {
		height = 720
	}
	lines := []string{
		"screen mode id:i:1",
		"use multimon:i:0",
		"desktopwidth:i:" + strconv.Itoa(width),
		"desktopheight:i:" + strconv.Itoa(height),
		"session bpp:i:" + strconv.Itoa(cfg.ColorDepth),
		"full address:s:" + net.JoinHostPort(host, port),
		"prompt for credentials:i:0",
		"authentication level:i:0",
		"enablecredsspsupport:i:1",
	}
	if cfg.Username != "" {
		lines = append(lines, "username:s:"+cfg.Username)
	}
	if cfg.Domain != "" {
		lines = append(lines, "domain:s:"+cfg.Domain)
	}
	if cfg.IgnoreCert {
		lines = append(lines, "authentication level:i:0")
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("opskat-rdp-%d-%d.rdp", assetID, time.Now().UnixNano()))
	return path, os.WriteFile(path, []byte(strings.Join(lines, "\r\n")+"\r\n"), 0600)
}
