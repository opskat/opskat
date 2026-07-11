package remote_desktop_svc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"go.uber.org/zap"
)

type Manager struct {
	assetRepo asset_repo.AssetRepo
	resolver  *credential_resolver.Resolver

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

	done  chan struct{}
	proxy *tcpWebSocketProxy
}

type ConnectOptions struct {
	WebSocketBaseURL string
}

func NewManager(repo asset_repo.AssetRepo) *Manager {
	if repo == nil {
		repo = asset_repo.Asset()
	}
	return &Manager{
		assetRepo: repo,
		resolver:  credential_resolver.Default(),
		sessions:  make(map[string]*Session),
	}
}

func (m *Manager) Connect(ctx context.Context, assetID int64, opts ConnectOptions) (*Session, error) {
	asset, err := m.assetRepo.Find(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("读取远程桌面资产失败: %w", err)
	}
	if asset.Type != asset_entity.AssetTypeVNC {
		return nil, fmt.Errorf("资产不是远程桌面类型: %s", asset.Type)
	}
	return m.connectVNC(ctx, asset)
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
}

func fileStatus(id int64) string {
	if id > 0 {
		return "已启用 SSH/SFTP 文件通道"
	}
	return "未配置 SSH/SFTP 文件通道，文件上传下载不可用"
}

type tcpWebSocketProxy struct {
	target string
	layers []proxychain.Layer
	server *http.Server
	done   chan struct{}
	once   sync.Once
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
	})
}
