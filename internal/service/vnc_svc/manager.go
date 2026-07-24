package vnc_svc

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
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
	Username       string `json:"username,omitempty"`
	Password       string `json:"password,omitempty"`
	FileSSHAssetID int64  `json:"fileSshAssetId"`

	// assetID 是这个会话所属的 VNC 资产（区别于上面用于文件传输的 FileSSHAssetID）。
	// 不导出：前端不需要它，它只用于资产被删除时按资产断开会话。
	assetID   int64
	conn      net.Conn
	onData    func([]byte)
	onClose   func()
	onEnd     func()
	startOnce sync.Once
	closeOnce sync.Once
}

func NewManager(repo asset_repo.AssetRepo) *Manager {
	return &Manager{
		assetRepo: repo,
		resolver:  credential_resolver.Default(),
		sessions:  make(map[string]*Session),
	}
}

func (m *Manager) Connect(ctx context.Context, assetID int64) (*Session, error) {
	logger.Ctx(ctx).Info("VNC connect start", zap.Int64("assetID", assetID))
	session, err := m.connect(ctx, assetID)
	if err != nil {
		logger.Ctx(ctx).Error("VNC connect failed", zap.Int64("assetID", assetID), zap.Error(err))
		return nil, err
	}
	logger.Ctx(ctx).Info("VNC connected",
		zap.Int64("assetID", assetID), zap.String("sessionID", session.ID))
	return session, nil
}

func (m *Manager) connect(ctx context.Context, assetID int64) (*Session, error) {
	asset, err := m.assetRepo.Find(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("读取 VNC 资产失败: %w", err)
	}
	if asset.Type != asset_entity.AssetTypeVNC {
		return nil, fmt.Errorf("资产不是 VNC 类型: %s", asset.Type)
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
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := (proxychain.Chain{Layers: layers}).Dial(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("连接 VNC 目标失败: %w", err)
	}
	session := &Session{
		ID:             uuid.NewString(),
		Username:       cfg.Username,
		Password:       password,
		FileSSHAssetID: cfg.FileSSHAssetID,
		assetID:        asset.ID,
		conn:           conn,
	}
	m.store(session)
	return session, nil
}

func (m *Manager) store(session *Session) {
	m.mu.Lock()
	m.sessions[session.ID] = session
	m.mu.Unlock()
}

// SetCallbacks 挂上 Go→FE 的数据/关闭回调,并启动读 pump(幂等)。sessionID 不存在返回错误。
func (m *Manager) SetCallbacks(sessionID string, onData func([]byte), onClose func()) error {
	m.mu.Lock()
	session := m.sessions[sessionID]
	m.mu.Unlock()
	if session == nil {
		return fmt.Errorf("VNC 会话不存在: %s", sessionID)
	}
	session.start(onData, onClose, func() { m.retire(sessionID, session) })
	return nil
}

// Write 把前端(noVNC)发来的字节写入目标连接。
func (m *Manager) Write(sessionID string, data []byte) error {
	m.mu.Lock()
	session := m.sessions[sessionID]
	m.mu.Unlock()
	if session == nil {
		return fmt.Errorf("VNC 会话不存在: %s", sessionID)
	}
	return session.write(data)
}

func (m *Manager) Disconnect(sessionID string) {
	m.mu.Lock()
	session := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if session != nil {
		session.close()
		// Disconnect 从 Wails 绑定调用,无 ctx,用默认 logger 记录会话关闭。
		logger.Default().Info("VNC session closed", zap.String("sessionID", sessionID))
	}
}

func (m *Manager) retire(sessionID string, session *Session) {
	m.mu.Lock()
	if m.sessions[sessionID] == session {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()
}

// CloseAsset 断开指定资产的全部 VNC 会话。
func (m *Manager) CloseAsset(assetID int64) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id, session := range m.sessions {
		if session.assetID == assetID {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Disconnect(id)
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

func (s *Session) start(onData func([]byte), onClose func(), onEnd func()) {
	s.startOnce.Do(func() {
		s.onData = onData
		s.onClose = onClose
		s.onEnd = onEnd
		go s.readPump()
	})
}

func (s *Session) readPump() {
	buf := make([]byte, 32768)
	for {
		n, err := s.conn.Read(buf)
		if n > 0 && s.onData != nil {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.onData(chunk)
		}
		if err != nil {
			break
		}
	}
	s.close()
	if s.onEnd != nil {
		s.onEnd()
	}
	if s.onClose != nil {
		s.onClose()
	}
}

func (s *Session) write(data []byte) error {
	if s.conn == nil {
		return fmt.Errorf("VNC 会话未建立连接")
	}
	_, err := s.conn.Write(data)
	return err
}

func (s *Session) close() {
	s.closeOnce.Do(func() {
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}
