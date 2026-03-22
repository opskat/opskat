package ssh_svc

import (
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Session 表示一个活跃的 SSH 终端会话
type Session struct {
	ID       string
	AssetID  int64
	client   *ssh.Client
	session  *ssh.Session
	stdin    io.WriteCloser
	stdout   io.Reader
	mu       sync.Mutex
	closed   bool
	onData   func(data []byte)   // 终端输出回调
	onClosed func(sessionID string) // 会话关闭回调
}

// Write 向终端写入数据（用户输入）
func (s *Session) Write(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session is closed")
	}
	_, err := s.stdin.Write(data)
	return err
}

// Resize 调整终端尺寸
func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session is closed")
	}
	return s.session.WindowChange(rows, cols)
}

// Close 关闭会话
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.session.Close()
	s.client.Close()
	if s.onClosed != nil {
		go s.onClosed(s.ID)
	}
}

// IsClosed 检查是否已关闭
func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Manager 管理所有 SSH 会话
type Manager struct {
	sessions sync.Map // map[string]*Session
	counter  int64
	mu       sync.Mutex
}

// NewManager 创建会话管理器
func NewManager() *Manager {
	return &Manager{}
}

// ConnectConfig SSH 连接配置
type ConnectConfig struct {
	Host     string
	Port     int
	Username string
	AuthType string // password | key
	Password string
	Key      string // PEM 格式私钥
	AssetID  int64
	Cols     int
	Rows     int
	OnData   func(sessionID string, data []byte) // 终端输出回调
	OnClosed func(sessionID string)               // 关闭回调
}

// Connect 建立 SSH 连接并启动 PTY 会话
func (m *Manager) Connect(cfg ConnectConfig) (string, error) {
	// 构建 SSH 认证方式
	var authMethods []ssh.AuthMethod
	switch cfg.AuthType {
	case "password":
		authMethods = []ssh.AuthMethod{ssh.Password(cfg.Password)}
	case "key":
		signer, err := ssh.ParsePrivateKey([]byte(cfg.Key))
		if err != nil {
			return "", fmt.Errorf("解析密钥失败: %w", err)
		}
		authMethods = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	default:
		return "", fmt.Errorf("不支持的认证方式: %s", cfg.AuthType)
	}

	// 连接 SSH
	sshConfig := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return "", fmt.Errorf("SSH连接失败: %w", err)
	}

	// 创建会话
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return "", fmt.Errorf("创建会话失败: %w", err)
	}

	// 请求 PTY
	cols := cfg.Cols
	if cols <= 0 {
		cols = 80
	}
	rows := cfg.Rows
	if rows <= 0 {
		rows = 24
	}
	if err := session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("请求PTY失败: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("获取stdin失败: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("获取stdout失败: %w", err)
	}

	// 启动 shell
	if err := session.Shell(); err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("启动shell失败: %w", err)
	}

	// 生成会话 ID
	m.mu.Lock()
	m.counter++
	sessionID := fmt.Sprintf("ssh-%d", m.counter)
	m.mu.Unlock()

	sess := &Session{
		ID:       sessionID,
		AssetID:  cfg.AssetID,
		client:   client,
		session:  session,
		stdin:    stdin,
		stdout:   stdout,
		onData:   func(data []byte) { cfg.OnData(sessionID, data) },
		onClosed: cfg.OnClosed,
	}

	m.sessions.Store(sessionID, sess)

	// 启动输出读取 goroutine
	go m.readOutput(sess)

	return sessionID, nil
}

// readOutput 持续读取终端输出并回调
func (m *Manager) readOutput(sess *Session) {
	buf := make([]byte, 8192)
	for {
		n, err := sess.stdout.Read(buf)
		if n > 0 && sess.onData != nil {
			data := make([]byte, n)
			copy(data, buf[:n])
			sess.onData(data)
		}
		if err != nil {
			break
		}
	}
	sess.Close()
	m.sessions.Delete(sess.ID)
}

// GetSession 获取会话
func (m *Manager) GetSession(id string) (*Session, bool) {
	v, ok := m.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

// Disconnect 断开指定会话
func (m *Manager) Disconnect(id string) {
	if sess, ok := m.GetSession(id); ok {
		sess.Close()
		m.sessions.Delete(id)
	}
}

// DisconnectAll 断开所有会话
func (m *Manager) DisconnectAll() {
	m.sessions.Range(func(key, value any) bool {
		value.(*Session).Close()
		m.sessions.Delete(key)
		return true
	})
}

// ActiveSessions 返回活跃会话数
func (m *Manager) ActiveSessions() int {
	count := 0
	m.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
