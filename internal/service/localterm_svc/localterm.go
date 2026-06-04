package localterm_svc

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/service/sessionid"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

var (
	errSessionClosed   = errors.New("session is closed")
	errSessionNotFound = errors.New("local session not found")
)

// callbackSetupGracePeriod 是会话挂回调前的宽限期,超时未挂则回收 PTY 防泄漏。
// 用 var 而非 const,与 startPTYFn 同理:测试可缩短它以快速覆盖宽限路径。
var callbackSetupGracePeriod = 5 * time.Second

// ConnectConfig 本地终端连接配置。
type ConnectConfig struct {
	AssetID int64
	Shell   string
	Args    []string
	Cwd     string
	Cols    int
	Rows    int
}

// Session 表示一个活跃的本地终端会话。
type Session struct {
	ID      string
	AssetID int64
	proc    ptyProcess

	// 启动配置,构造后不可变(与 proc/ID/AssetID 同):本地分屏(SplitFrom)据此
	// 再起一个同 shell 的 PTY —— 本地无连接可复用,分屏即新开一个同配置 shell。
	shell string
	args  []string
	cwd   string

	writeMu sync.Mutex
	mu      sync.Mutex

	closed        bool
	readerStarted bool
	closedCh      chan struct{}
	readerReadyCh chan struct{}

	onData   func(data []byte)
	onClosed func(sessionID string)
}

// Write 向 PTY 写入用户输入。
func (s *Session) Write(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errSessionClosed
	}
	proc := s.proc
	s.mu.Unlock()
	_, err := proc.Write(data)
	return err
}

// Resize 调整 PTY 窗口尺寸。
func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errSessionClosed
	}
	proc := s.proc
	s.mu.Unlock()
	return proc.Resize(cols, rows)
}

func (s *Session) ensureClosedChLocked() chan struct{} {
	if s.closedCh == nil {
		s.closedCh = make(chan struct{})
	}
	return s.closedCh
}

func (s *Session) ensureReaderReadyChLocked() chan struct{} {
	if s.readerReadyCh == nil {
		s.readerReadyCh = make(chan struct{})
	}
	return s.readerReadyCh
}

func (s *Session) closeLocked() (ptyProcess, func(string), string, bool) {
	if s.closed {
		return nil, nil, "", false
	}
	close(s.ensureClosedChLocked())
	s.closed = true
	return s.proc, s.onClosed, s.ID, true
}

// Close 关闭会话(关 PTY + 回调)。
func (s *Session) Close() {
	s.mu.Lock()
	proc, onClosed, sessionID, ok := s.closeLocked()
	s.mu.Unlock()
	if !ok {
		return
	}
	if err := proc.Close(); err != nil {
		logger.Default().Warn("close local pty", zap.String("sessionID", sessionID), zap.Error(err))
	}
	if onClosed != nil {
		go onClosed(sessionID)
	}
}

// IsClosed 是否已关闭。
func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Manager 管理所有本地终端会话。
type Manager struct {
	sessions sync.Map // map[string]*Session
	idgen    *sessionid.Generator
}

// NewManager 创建本地终端会话管理器。
func NewManager() *Manager { return &Manager{idgen: sessionid.NewGenerator("local")} }

// nextSessionID 生成进程内唯一、且跨重启不会与持久化旧会话 ID 冲突的会话 ID（issue #141）。
func (m *Manager) nextSessionID() string {
	return m.idgen.Next()
}

// Connect 启动一个本地 shell,返回 sessionID。调用方随后用 SetCallbacks 挂回调。
func (m *Manager) Connect(cfg ConnectConfig) (string, error) {
	proc, err := startPTYFn(ptySpec{
		Shell: cfg.Shell, Args: cfg.Args, Cwd: cfg.Cwd, Cols: cfg.Cols, Rows: cfg.Rows,
	})
	if err != nil {
		return "", fmt.Errorf("start local pty: %w", err)
	}

	sessionID := m.nextSessionID()

	sess := &Session{
		ID: sessionID, AssetID: cfg.AssetID, proc: proc,
		shell: cfg.Shell, args: cfg.Args, cwd: cfg.Cwd,
	}
	m.sessions.Store(sessionID, sess)
	m.watchCallbackSetup(sess, callbackSetupGracePeriod)

	logger.Default().Info("local terminal started",
		zap.String("sessionID", sessionID), zap.Int64("assetID", cfg.AssetID), zap.String("shell", cfg.Shell))
	return sessionID, nil
}

// SplitFrom 以现有会话的 shell 配置(shell/args/cwd/assetID)新开一个 PTY,返回新
// sessionID。本地"分屏"不复用 PTY(没有连接可复用),而是再起一个同配置的 shell ——
// 与 iTerm/tmux 的分屏语义一致。调用方随后用 SetCallbacks 挂回调,与 Connect 一致。
func (m *Manager) SplitFrom(existingSessionID string, cols, rows int) (string, error) {
	src, ok := m.GetSession(existingSessionID)
	if !ok {
		return "", fmt.Errorf("%w: %s", errSessionNotFound, existingSessionID)
	}
	// shell/args/cwd 构造后不可变,读取无需持锁(同 readOutput 读 proc)。
	return m.Connect(ConnectConfig{
		AssetID: src.AssetID,
		Shell:   src.shell,
		Args:    src.args,
		Cwd:     src.cwd,
		Cols:    cols,
		Rows:    rows,
	})
}

// SetCallbacks 挂数据/关闭回调,回调就绪后才启动 reader,避免首屏输出丢失。
func (m *Manager) SetCallbacks(sessionID string, onData func([]byte), onClosed func(string)) {
	sess, ok := m.GetSession(sessionID)
	if !ok {
		return
	}
	startReader := false
	sess.mu.Lock()
	sess.onData = onData
	sess.onClosed = onClosed
	if !sess.readerStarted && !sess.closed {
		close(sess.ensureReaderReadyChLocked())
		sess.readerStarted = true
		startReader = true
	}
	sess.mu.Unlock()
	if startReader {
		go m.readOutput(sess)
	}
}

func (m *Manager) watchCallbackSetup(sess *Session, timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	sess.mu.Lock()
	if sess.closed || sess.readerStarted {
		sess.mu.Unlock()
		return
	}
	readyCh := sess.ensureReaderReadyChLocked()
	closedCh := sess.ensureClosedChLocked()
	sessionID := sess.ID
	sess.mu.Unlock()

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-readyCh:
			return
		case <-closedCh:
			return
		case <-timer.C:
			if m.closeSessionWithoutCallbacks(sessionID) {
				logger.Default().Warn("close local session without callbacks",
					zap.String("sessionID", sessionID), zap.Duration("timeout", timeout))
			}
		}
	}()
}

// closeSessionWithoutCallbacks 仅在会话确实从未挂回调时回收它,返回是否真正回收。
// 在 mu 下复检 closed/readerStarted:若 SetCallbacks 恰好在宽限边界抢先(readyCh 与
// timer.C 同时就绪、select 随机选中 timer.C),这里会跳过,既不误报 warning 也不误杀
// 刚启动 reader 的会话。
func (m *Manager) closeSessionWithoutCallbacks(sessionID string) bool {
	v, ok := m.sessions.Load(sessionID)
	if !ok {
		return false
	}
	sess := v.(*Session)
	sess.mu.Lock()
	if sess.closed || sess.readerStarted {
		sess.mu.Unlock()
		return false
	}
	sess.mu.Unlock()
	m.closeSession(sessionID)
	return true
}

// readOutput 持续读 PTY 输出并回调。一次 Read 最多 32KB,天然合并突发输出。
func (m *Manager) readOutput(sess *Session) {
	defer func() {
		m.sessions.Delete(sess.ID)
		sess.Close()
	}()

	buf := make([]byte, 32*1024)
	for {
		// proc 在构造后不可变,故读取它无需持 mu(阻塞 Read 也不该在锁内)。
		n, err := sess.proc.Read(buf)
		if n > 0 {
			sess.mu.Lock()
			handler := sess.onData
			sess.mu.Unlock()
			if handler != nil {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				handler(chunk)
			}
		}
		if err != nil {
			return // EOF(shell 退出)或读错误 → 关闭会话
		}
	}
}

// GetSession 获取活跃会话。
func (m *Manager) GetSession(sessionID string) (*Session, bool) {
	v, ok := m.sessions.Load(sessionID)
	if !ok {
		return nil, false
	}
	sess := v.(*Session)
	if sess.IsClosed() {
		m.sessions.Delete(sessionID)
		return nil, false
	}
	return sess, true
}

// Disconnect 断开会话。
func (m *Manager) Disconnect(sessionID string) { m.closeSession(sessionID) }

// CloseAll 关闭所有会话。
func (m *Manager) CloseAll() {
	var ids []string
	m.sessions.Range(func(k, _ any) bool { ids = append(ids, k.(string)); return true })
	for _, id := range ids {
		m.closeSession(id)
	}
}

func (m *Manager) closeSession(sessionID string) {
	v, ok := m.sessions.LoadAndDelete(sessionID)
	if !ok {
		return
	}
	v.(*Session).Close()
}
