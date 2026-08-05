package ssh_svc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/dirsync"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/pkg/socksdial"
	"github.com/opskat/opskat/internal/pkg/sshkeepalive"
	"github.com/opskat/opskat/internal/pkg/sshtuning"
	"github.com/opskat/opskat/internal/service/sessionid"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// sharedClient 封装 SSH 连接，支持引用计数共享
type sharedClient struct {
	client        *ssh.Client
	mu            sync.Mutex
	refCount      int
	closers       []io.Closer // 跳板机 client 等额外资源
	closed        bool
	stopKeepalive func()
}

func newSharedClient(client *ssh.Client, closers []io.Closer, keepAliveSeconds int) *sharedClient {
	sc := &sharedClient{
		client:   client,
		refCount: 1,
		closers:  closers,
	}
	sc.stopKeepalive = sshkeepalive.Start(client, sshtuning.ResolveKeepAlive(keepAliveSeconds))
	return sc
}

func (sc *sharedClient) acquire() {
	sc.mu.Lock()
	sc.refCount++
	sc.mu.Unlock()
}

func (sc *sharedClient) release() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.refCount--
	if sc.refCount <= 0 && !sc.closed {
		sc.closed = true
		if sc.stopKeepalive != nil {
			sc.stopKeepalive()
		}
		if err := sc.client.Close(); err != nil {
			logger.Default().Warn("close client", zap.Error(err))
		}
		for _, c := range sc.closers {
			if err := c.Close(); err != nil {
				logger.Default().Warn("close jump host resource", zap.Error(err))
			}
		}
	}
}

// Session 表示一个活跃的 SSH 终端会话
type Session struct {
	ID       string
	AssetID  int64
	shared   *sharedClient
	session  *ssh.Session
	stdin    io.WriteCloser
	stdout   io.Reader
	mu       sync.Mutex
	closed   bool
	onData   func(data []byte)      // 终端输出回调
	onClosed func(sessionID string) // 会话关闭回调
	onSync   func(sessionID string, state DirectorySyncState)

	// shellPath / shellType are detected lazily by EnableSync. Empty means no
	// sync attempt has needed shell detection yet; "unsupported" means the
	// remote shell cannot host the directory-sync prompt hook.
	shellPath string
	shellType string

	syncEnableMu       sync.Mutex
	syncMu             sync.Mutex
	outputFilterMu     sync.Mutex
	syncState          DirectorySyncState
	pendingDirChange   chan error
	pendingDirNonce    string
	pendingDirTarget   string
	pendingDirExpected string
	parserRemainder    []byte
	echoSuppressions   [][]byte
	echoSuppressionIdx int
	echoWrapPending    bool // a terminal-autowrap CR ended a chunk; skip the re-emitted char next
	syncToken          string
	promptNonce        string
	promptPendingNonce string
	shellPID           int
	syncDirty          bool
	syncBootstrapCh    chan struct{} // closed when EnableSync receives init:pid; nil when not bootstrapping
	syncProbeActive    bool
	probeShellStateFn  func(int) (shellProbeResult, error)
}

// Write 向终端写入数据（用户输入）
func (s *Session) Write(data []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session is closed")
	}
	hasNewline := bytes.ContainsAny(data, "\r\n")
	s.markUserInput(data)
	_, err := s.stdin.Write(data)
	s.mu.Unlock()
	if err == nil && hasNewline {
		s.ensureSyncProbe()
	}
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
	s.failPendingDirectoryChange(dirsync.Error(dirSyncErrSessionClosed))
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.session != nil {
		if err := s.session.Close(); err != nil {
			logger.Default().Warn("close session", zap.String("sessionID", s.ID), zap.Error(err))
		}
	}
	s.shared.release()
	if s.onClosed != nil {
		go s.onClosed(s.ID)
	}
}

// Client 返回底层 SSH Client（用于 SFTP 等）
func (s *Session) Client() *ssh.Client {
	return s.shared.client
}

// IsClosed 检查是否已关闭
func (s *Session) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Session) writeInternal(data []byte) error {
	pattern := s.queueInternalEchoSuppression(data)
	if err := s.writeStdin(data); err != nil {
		// 部分写入/关闭都要清掉队列项：截断的远端回显匹配不到完整 pattern，
		// 留在队列里会误吞后续无关输出。
		s.removeQueuedEchoSuppression(pattern)
		return err
	}
	return nil
}

// writeStdin writes raw bytes to the shell stdin without queueing echo
// suppression. Callers that need to track the suppression pattern themselves
// (EnableSync, so it can drop the pattern on a failed enable) use this plus
// queueInternalEchoSuppression directly.
func (s *Session) writeStdin(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session is closed")
	}
	_, err := s.stdin.Write(data)
	return err
}

// Manager 管理所有 SSH 会话
type Manager struct {
	sessions sync.Map // map[string]*Session
	idgen    *sessionid.Generator
}

// NewManager 创建会话管理器
func NewManager() *Manager {
	return &Manager{idgen: sessionid.NewGenerator("ssh")}
}

// nextSessionID 生成进程内唯一、且跨重启不会与持久化旧会话 ID 冲突的会话 ID（issue #141）。
func (m *Manager) nextSessionID() string {
	return m.idgen.Next()
}

// ConnectConfig SSH 连接配置
type ConnectConfig struct {
	Host     string
	Port     int
	Username string
	AuthType string // password | key | keyboard-interactive | agent
	Password string
	// Ctx 供握手 / 取消使用（Agent 列举、签名、MFA 等待与握手取消）。nil 时用 background。
	Ctx context.Context
	// Agent 非 nil 时该层经 Agent 认证工厂（agent_factory.go）完成握手：精确签名器
	// + MFA 适配 + 主机密钥契约。与 Password/Key 等常规材料互斥。
	Agent         *AgentConfig
	Key           string   // PEM 格式私钥（直接传入）
	KeyPassphrase string   // 私钥密码（用于加密的私钥）
	PrivateKeys   []string // 私钥文件路径列表
	AssetID       int64
	Cols          int
	Rows          int
	OnData        func(sessionID string, data []byte) // 终端输出回调
	OnClosed      func(sessionID string)              // 关闭回调
	OnSync        func(sessionID string, state DirectorySyncState)

	// 进度回调（异步连接用），step: resolve/connect/auth/shell
	OnProgress func(step, message string)
	// 键盘交互认证回调
	OnAuthChallenge func(prompts []string, echo []bool) ([]string, error)

	// 跳板机: 已解析的链式连接配置（从叶子到根）
	JumpHosts []JumpHostEntry
	// 代理
	Proxy *asset_entity.ProxyConfig
	// 代理链：非空时优先于旧 JumpHosts/Proxy。
	ProxyChain []proxychain.Layer

	// 主机密钥校验回调（nil 则跳过校验）
	HostKeyVerifyFunc HostKeyVerifyFunc

	// KeepAliveIntervalSeconds 覆盖此连接的 SSH 空闲保活心跳间隔（秒）。
	// 0 = 跟随全局默认（sshtuning）。
	KeepAliveIntervalSeconds int

	// RestoreCwdOnReconnect 为该资产开启「重连恢复上次目录」时为真：连接建立后
	// 自动启用目录同步以持续追踪 cwd，并在 InitialWorkdir 非空时 cd 回上次目录。
	RestoreCwdOnReconnect bool
	// InitialWorkdir 仅在重连路径由前端携带上次已知 cwd；首次连接为空。
	// 实际是否恢复由 RestoreCwdOnReconnect 权威闸门决定。
	InitialWorkdir string
}

// JumpHostEntry 跳板机连接信息
type JumpHostEntry struct {
	Host       string
	Port       int
	Username   string
	AuthType   string
	Password   string
	Key        string
	Passphrase string
	// Agent 非 nil 时该跳板层经 Agent 认证工厂完成握手（旧 jump-host 字段同样使用
	// 同一个工厂，没有单独的回退实现）。
	Agent *AgentConfig
}

// emitProgress 安全调用进度回调
func emitProgress(cfg *ConnectConfig, step, message string) {
	if cfg.OnProgress != nil {
		cfg.OnProgress(step, message)
	}
}

// Dial 仅建立 SSH 连接（不创建 PTY/Session），用于连接池等场景
func (m *Manager) Dial(cfg ConnectConfig) (*ssh.Client, []io.Closer, error) {
	sshConfig, err := buildClientConfig(cfg)
	if err != nil {
		return nil, nil, err
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, closers, err := m.dial(cfg, sshConfig, addr)
	if err != nil {
		return nil, nil, err
	}
	// 非终端连接（连接池 / 端口转发 / AI）的 keepalive 由本函数负责：把 stop 作为
	// 首个 closer 交回调用方，调用方关闭 closers 时即停止心跳，无需各池自管。
	stop := sshkeepalive.Start(client, sshtuning.ResolveKeepAlive(cfg.KeepAliveIntervalSeconds))
	closers = append([]io.Closer{closerFunc(stop)}, closers...)
	return client, closers, nil
}

// closerFunc 把一个无返回值的停止函数适配成 io.Closer，便于随 closers 统一关闭。
type closerFunc func()

func (f closerFunc) Close() error {
	f()
	return nil
}

// Connect 建立 SSH 连接并启动 PTY 会话
func (m *Manager) Connect(cfg ConnectConfig) (string, error) {
	// 构建目标认证方式（Agent 模式走认证工厂）
	sshConfig, err := buildClientConfig(cfg)
	if err != nil {
		return "", err
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	emitProgress(&cfg, "connect", fmt.Sprintf("正在连接 %s...", addr))

	// 建立连接（可能经过代理和跳板机链）
	client, extraClosers, err := m.dial(cfg, sshConfig, addr)
	if err != nil {
		return "", err
	}

	shared := newSharedClient(client, extraClosers, cfg.KeepAliveIntervalSeconds)

	emitProgress(&cfg, "shell", "正在启动终端...")

	sessionID, err := m.createSession(shared, cfg.AssetID, cfg.Cols, cfg.Rows, cfg.OnData, cfg.OnClosed, cfg.OnSync)
	if err != nil {
		shared.release()
		return "", err
	}

	if cfg.RestoreCwdOnReconnect {
		if sess, ok := m.GetSession(sessionID); ok {
			// 恢复：重连时 cd 回上次目录（首次连接 InitialWorkdir 为空 → no-op）。
			if err := sess.RestoreWorkingDirectory(cfg.InitialWorkdir); err != nil {
				logger.Default().Warn("restore cwd on reconnect failed",
					zap.String("sessionID", sessionID), zap.String("cwd", cfg.InitialWorkdir), zap.Error(err))
			}
			// 捕获：延迟后自动启用目录同步，持续追踪 cwd 供下次重连。
			go m.autoEnableDirectorySync(sess)
		}
	}

	return sessionID, nil
}

// ConnectClient 建立仅用于 SFTP/端口转发等非终端用途的 SSH 会话 ID。
// 它不创建 PTY 和 shell，但会注册到 Manager sessions，供 SFTP 服务按 sessionID 复用。
func (m *Manager) ConnectClient(cfg ConnectConfig) (string, error) {
	sshConfig, err := buildClientConfig(cfg)
	if err != nil {
		return "", err
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, extraClosers, err := m.dial(cfg, sshConfig, addr)
	if err != nil {
		return "", err
	}
	shared := newSharedClient(client, extraClosers, cfg.KeepAliveIntervalSeconds)
	sessionID := m.nextSessionID()
	m.sessions.Store(sessionID, &Session{
		ID:       sessionID,
		AssetID:  cfg.AssetID,
		shared:   shared,
		onClosed: cfg.OnClosed,
	})
	return sessionID, nil
}

// autoSyncSettleDelay 是自动启用目录同步前的等待，让 sshd 的 motd/首个 prompt 先落地，
// 避免注入的 hook 脚本与其交错。设为变量便于测试缩短。
var autoSyncSettleDelay = 1 * time.Second

// autoEnableDirectorySync 在会话稳定后自动启用目录同步（用于「重连恢复上次目录」的 cwd 捕获）。
// best-effort：不支持的 shell / 超时只记 Warn，不影响会话本身。
func (m *Manager) autoEnableDirectorySync(sess *Session) {
	time.Sleep(autoSyncSettleDelay)
	if sess.IsClosed() {
		return
	}
	logger.Default().Info("auto-enable directory sync for cwd restore", zap.String("sessionID", sess.ID))
	if err := sess.EnableSync(); err != nil {
		logger.Default().Warn("auto-enable directory sync failed",
			zap.String("sessionID", sess.ID), zap.Error(err))
	}
}

// createSession 在 sharedClient 上创建新的 SSH 会话（PTY + shell）
func (m *Manager) createSession(shared *sharedClient, assetID int64, cols, rows int,
	onData func(string, []byte), onClosed func(string), onSync func(string, DirectorySyncState)) (string, error) {

	session, err := shared.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("创建会话失败: %w", err)
	}

	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	if err := session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		if closeErr := session.Close(); closeErr != nil {
			logger.Default().Warn("close session after PTY request failure", zap.Error(closeErr))
		}
		return "", fmt.Errorf("请求PTY失败: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		if closeErr := session.Close(); closeErr != nil {
			logger.Default().Warn("close session after stdin pipe failure", zap.Error(closeErr))
		}
		return "", fmt.Errorf("获取stdin失败: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		if closeErr := session.Close(); closeErr != nil {
			logger.Default().Warn("close session after stdout pipe failure", zap.Error(closeErr))
		}
		return "", fmt.Errorf("获取stdout失败: %w", err)
	}

	sessionID := m.nextSessionID()

	sess := &Session{
		ID:       sessionID,
		AssetID:  assetID,
		shared:   shared,
		session:  session,
		stdin:    stdin,
		stdout:   stdout,
		onData:   func(data []byte) { onData(sessionID, data) },
		onClosed: onClosed,
	}
	if onSync != nil {
		sess.onSync = func(_ string, state DirectorySyncState) { onSync(sessionID, state) }
	}

	// Start a native interactive shell so sshd emits "Last login" / motd /
	// banner natively. Shell-type detection and sync hooks are deferred to
	// Session.EnableSync — opening a probe SSH channel here would consume the
	// PAM motd output before the user's main session sees it.
	sess.initSyncState("", "", false)

	if err := session.Shell(); err != nil {
		if closeErr := session.Close(); closeErr != nil {
			logger.Default().Warn("close session after shell start failure", zap.Error(closeErr))
		}
		return "", fmt.Errorf("启动shell失败: %w", err)
	}

	m.sessions.Store(sessionID, sess)
	go m.readOutput(sess)

	return sessionID, nil
}

// NewSessionFrom 在已有会话的连接上创建新会话（用于分割窗格）
func (m *Manager) NewSessionFrom(existingSessionID string, cols, rows int,
	onData func(string, []byte), onClosed func(string), onSync func(string, DirectorySyncState)) (string, error) {

	existing, ok := m.GetSession(existingSessionID)
	if !ok {
		return "", fmt.Errorf("会话不存在: %s", existingSessionID)
	}
	if existing.IsClosed() {
		return "", fmt.Errorf("会话已关闭: %s", existingSessionID)
	}

	existing.shared.acquire()

	sessionID, err := m.createSession(existing.shared, existing.AssetID, cols, rows, onData, onClosed, onSync)
	if err != nil {
		existing.shared.release()
		return "", err
	}

	return sessionID, nil
}

// dial 建立到目标的网络连接，支持代理和跳板机链
func (m *Manager) dial(cfg ConnectConfig, sshConfig *ssh.ClientConfig, targetAddr string) (*ssh.Client, []io.Closer, error) {
	ctx := connectCtx(cfg)
	var closers []io.Closer

	if len(cfg.ProxyChain) > 0 {
		emitProgress(&cfg, "connect", "正在通过代理链连接...")
		conn, err := proxychain.Chain{
			Layers: cfg.ProxyChain,
			Direct: dialTCP,
		}.Dial(ctx, targetAddr)
		if err != nil {
			return nil, nil, err
		}
		closers = append(closers, conn)
		emitProgress(&cfg, "auth", "正在认证...")
		client, err := newClientLayer(ctx, cfg, sshConfig, conn, targetAddr)
		if err != nil {
			if closeErr := conn.Close(); closeErr != nil {
				logger.Default().Warn("close proxy chain connection after handshake failure", zap.Error(closeErr))
			}
			return nil, nil, fmt.Errorf("SSH握手失败: %w", err)
		}
		return client, closers, nil
	}

	// 情况1: 有跳板机链
	if len(cfg.JumpHosts) > 0 {
		return m.dialViaJumpHosts(cfg, sshConfig, targetAddr)
	}

	// 情况2: 有代理（无跳板机）
	if cfg.Proxy != nil {
		emitProgress(&cfg, "connect", fmt.Sprintf("正在通过代理 %s:%d 连接...", cfg.Proxy.Host, cfg.Proxy.Port))
		conn, err := socksdial.Dial(ctx, cfg.Proxy, targetAddr)
		if err != nil {
			return nil, nil, err
		}
		closers = append(closers, conn)

		emitProgress(&cfg, "auth", "正在认证...")
		client, err := newClientLayer(ctx, cfg, sshConfig, conn, targetAddr)
		if err != nil {
			if closeErr := conn.Close(); closeErr != nil {
				logger.Default().Warn("close proxy connection after handshake failure", zap.Error(closeErr))
			}
			return nil, nil, fmt.Errorf("SSH握手失败: %w", err)
		}
		return client, closers, nil
	}

	// 情况3: 直连
	emitProgress(&cfg, "auth", "正在认证...")
	conn, err := dialTCP(ctx, targetAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH连接失败: %w", err)
	}
	client, err := newClientLayer(ctx, cfg, sshConfig, conn, targetAddr)
	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			logger.Default().Warn("close connection after handshake failure", zap.Error(closeErr))
		}
		return nil, nil, fmt.Errorf("SSH连接失败: %w", err)
	}
	return client, nil, nil
}

// newClientLayer 在已建立的 conn 上完成一个 SSH 层握手。cfg.Agent 非 nil 时经
// Agent 认证工厂（精确签名器 + MFA + 主机密钥契约）；否则用常规 ssh.NewClientConn。
func newClientLayer(ctx context.Context, cfg ConnectConfig, sshConfig *ssh.ClientConfig, conn net.Conn, addr string) (*ssh.Client, error) {
	if cfg.Agent != nil {
		return AgentClientConn(ctx, cfg.Agent, cfg.Username, MakeAgentHostKeyCallback(cfg.Host, cfg.Port, cfg.HostKeyVerifyFunc), conn, addr)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, sshConfig)
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// dialTCP 建立到 addr 的底层 TCP 连接，并按 sshtuning 配置施加 TCP_NODELAY /
// SO_KEEPALIVE / 连接超时。仅对真正的 TCP 套接字生效；经 SOCKS5 代理或跳板机
// 通道的连接不是 *net.TCPConn，TCP 选项无法触达（保活/超时仍通过 SSH 层与
// ClientConfig.Timeout 兜底）。
func dialTCP(ctx context.Context, addr string) (net.Conn, error) {
	s := sshtuning.Get()
	conn, err := s.Dialer().DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if err := s.ApplyTCPOptions(conn); err != nil {
		logger.Default().Warn("apply TCP options", zap.String("addr", addr), zap.Error(err))
	}
	return conn, nil
}

// dialViaJumpHosts 通过跳板机链连接目标
func (m *Manager) dialViaJumpHosts(cfg ConnectConfig, targetConfig *ssh.ClientConfig, targetAddr string) (*ssh.Client, []io.Closer, error) {
	var closers []io.Closer

	// 连接第一个跳板机（可能通过代理）
	ctx := connectCtx(cfg)
	firstJump := cfg.JumpHosts[0]
	firstAddr := fmt.Sprintf("%s:%d", firstJump.Host, firstJump.Port)

	emitProgress(&cfg, "connect", fmt.Sprintf("正在连接跳板机 %s...", firstAddr))

	firstConfig, err := buildJumpHostConfig(cfg, firstJump)
	if err != nil {
		return nil, nil, fmt.Errorf("跳板机认证配置失败: %w", err)
	}

	var currentClient *ssh.Client

	if cfg.Proxy != nil {
		emitProgress(&cfg, "connect", fmt.Sprintf("正在通过代理 %s:%d 连接跳板机...", cfg.Proxy.Host, cfg.Proxy.Port))
		conn, err := socksdial.Dial(ctx, cfg.Proxy, firstAddr)
		if err != nil {
			return nil, nil, fmt.Errorf("通过代理连接跳板机失败: %w", err)
		}
		closers = append(closers, conn)

		client, err := newJumpClientLayer(ctx, cfg, firstJump, firstConfig, conn, firstAddr)
		if err != nil {
			if closeErr := conn.Close(); closeErr != nil {
				logger.Default().Warn("close proxy connection after jump host handshake failure", zap.Error(closeErr))
			}
			return nil, nil, fmt.Errorf("跳板机SSH握手失败: %w", err)
		}
		currentClient = client
	} else {
		conn, err := dialTCP(ctx, firstAddr)
		if err != nil {
			return nil, nil, fmt.Errorf("连接跳板机失败: %w", err)
		}
		client, err := newJumpClientLayer(ctx, cfg, firstJump, firstConfig, conn, firstAddr)
		if err != nil {
			if closeErr := conn.Close(); closeErr != nil {
				logger.Default().Warn("close jump host connection after handshake failure", zap.Error(closeErr))
			}
			return nil, nil, fmt.Errorf("连接跳板机失败: %w", err)
		}
		currentClient = client
	}
	closers = append(closers, currentClient)

	// 连接中间跳板机
	for i := 1; i < len(cfg.JumpHosts); i++ {
		jump := cfg.JumpHosts[i]
		jumpAddr := fmt.Sprintf("%s:%d", jump.Host, jump.Port)

		emitProgress(&cfg, "connect", fmt.Sprintf("正在连接跳板机 %s...", jumpAddr))

		jumpConfig, err := buildJumpHostConfig(cfg, jump)
		if err != nil {
			for _, c := range closers {
				if closeErr := c.Close(); closeErr != nil {
					logger.Default().Warn("close jump host chain resource during auth config cleanup", zap.Error(closeErr))
				}
			}
			return nil, nil, fmt.Errorf("跳板机认证配置失败: %w", err)
		}

		conn, err := currentClient.Dial("tcp", jumpAddr)
		if err != nil {
			for _, c := range closers {
				if closeErr := c.Close(); closeErr != nil {
					logger.Default().Warn("close jump host chain resource during dial cleanup", zap.Error(closeErr))
				}
			}
			return nil, nil, fmt.Errorf("通过跳板机连接下一跳失败: %w", err)
		}

		client, err := newJumpClientLayer(ctx, cfg, jump, jumpConfig, conn, jumpAddr)
		if err != nil {
			if closeErr := conn.Close(); closeErr != nil {
				logger.Default().Warn("close jump host connection after handshake failure", zap.Error(closeErr))
			}
			for _, c := range closers {
				if closeErr := c.Close(); closeErr != nil {
					logger.Default().Warn("close jump host chain resource during handshake cleanup", zap.Error(closeErr))
				}
			}
			return nil, nil, fmt.Errorf("跳板机SSH握手失败: %w", err)
		}
		currentClient = client
		closers = append(closers, currentClient)
	}

	// 通过最后一个跳板机连接目标
	emitProgress(&cfg, "connect", fmt.Sprintf("正在通过跳板机连接目标 %s...", targetAddr))

	conn, err := currentClient.Dial("tcp", targetAddr)
	if err != nil {
		for _, c := range closers {
			if closeErr := c.Close(); closeErr != nil {
				logger.Default().Warn("close jump host chain resource during target dial cleanup", zap.Error(closeErr))
			}
		}
		return nil, nil, fmt.Errorf("通过跳板机连接目标失败: %w", err)
	}

	emitProgress(&cfg, "auth", "正在认证...")

	client, err := newClientLayer(ctx, cfg, targetConfig, conn, targetAddr)
	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			logger.Default().Warn("close target connection after handshake failure", zap.Error(closeErr))
		}
		for _, c := range closers {
			if closeErr := c.Close(); closeErr != nil {
				logger.Default().Warn("close jump host chain resource during target handshake cleanup", zap.Error(closeErr))
			}
		}
		return nil, nil, fmt.Errorf("目标SSH握手失败: %w", err)
	}

	return client, closers, nil
}

// buildJumpHostConfig 构建一个跳板层的 ClientConfig。j.Agent 非 nil 时该跳板层经
// Agent 认证工厂；否则沿用常规认证方式。
func buildJumpHostConfig(cfg ConnectConfig, j JumpHostEntry) (*ssh.ClientConfig, error) {
	if j.Agent != nil {
		return &ssh.ClientConfig{
			User:    j.Username,
			Timeout: sshtuning.Get().DialTimeoutOrDefault(),
		}, nil
	}
	auth, err := buildAuthMethods(j.AuthType, j.Password, j.Key, j.Passphrase, nil, nil)
	if err != nil {
		return nil, err
	}
	return &ssh.ClientConfig{
		User:            j.Username,
		Auth:            auth,
		HostKeyCallback: MakeHostKeyCallback(j.Host, j.Port, cfg.HostKeyVerifyFunc),
		Timeout:         sshtuning.Get().DialTimeoutOrDefault(),
	}, nil
}

// newJumpClientLayer 在已建立的 conn 上完成一个跳板层握手。j.Agent 非 nil 时经
// Agent 认证工厂；否则用常规 ssh.NewClientConn。
func newJumpClientLayer(ctx context.Context, cfg ConnectConfig, j JumpHostEntry, config *ssh.ClientConfig, conn net.Conn, addr string) (*ssh.Client, error) {
	if j.Agent != nil {
		return AgentClientConn(ctx, j.Agent, j.Username, MakeAgentHostKeyCallback(j.Host, j.Port, cfg.HostKeyVerifyFunc), conn, addr)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// buildClientConfig 构建目标层的 ClientConfig。Agent 模式走认证工厂（host key 回调
// 在握手内组装，这里只带 User/Timeout）；常规模式沿用 buildAuthMethods。
func buildClientConfig(cfg ConnectConfig) (*ssh.ClientConfig, error) {
	if cfg.Agent != nil {
		return &ssh.ClientConfig{
			User:    cfg.Username,
			Timeout: sshtuning.Get().DialTimeoutOrDefault(),
		}, nil
	}
	authMethods, err := buildAuthMethods(cfg.AuthType, cfg.Password, cfg.Key, cfg.KeyPassphrase, cfg.PrivateKeys, cfg.OnAuthChallenge)
	if err != nil {
		return nil, err
	}
	return &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            authMethods,
		HostKeyCallback: MakeHostKeyCallback(cfg.Host, cfg.Port, cfg.HostKeyVerifyFunc),
		Timeout:         sshtuning.Get().DialTimeoutOrDefault(),
	}, nil
}

// buildAuthMethods 构建 SSH 认证方式
func buildAuthMethods(authType, password, key, keyPassphrase string, privateKeyPaths []string,
	onAuthChallenge func(prompts []string, echo []bool) ([]string, error)) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	// keyboard-interactive 认证回调（用于 OTP/动态密码等场景）
	kbInteractive := func() ssh.AuthMethod {
		return ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			// 如果没有问题，返回空
			if len(questions) == 0 {
				return nil, nil
			}
			// 如果有回调，使用回调获取用户输入
			if onAuthChallenge != nil {
				return onAuthChallenge(questions, echos)
			}
			// 没有回调但有密码，尝试用密码回答第一个问题
			if password != "" {
				answers := make([]string, len(questions))
				answers[0] = password
				return answers, nil
			}
			return nil, fmt.Errorf("keyboard-interactive 认证需要用户输入")
		})
	}

	switch authType {
	case "password":
		methods = append(methods, ssh.Password(password))
		// 追加 keyboard-interactive 作为 fallback（许多服务器用 keyboard-interactive 替代 password）
		methods = append(methods, kbInteractive())
	case "key":
		// 优先使用直接传入的 key
		if key != "" {
			signer, err := parsePrivateKey([]byte(key), keyPassphrase)
			if err != nil {
				return nil, fmt.Errorf("解析密钥失败: %w", err)
			}
			methods = append(methods, ssh.PublicKeys(signer))
		}
		// 从文件路径读取私钥
		for _, path := range privateKeyPaths {
			data, err := os.ReadFile(path) //nolint:gosec // file path from user config
			if err != nil {
				return nil, fmt.Errorf("读取私钥文件 %s 失败: %w", path, err)
			}
			signer, err := parsePrivateKey(data, keyPassphrase)
			if err != nil {
				return nil, fmt.Errorf("解析私钥文件 %s 失败: %w", path, err)
			}
			methods = append(methods, ssh.PublicKeys(signer))
		}
		if len(methods) == 0 {
			return nil, fmt.Errorf("密钥认证方式需要提供私钥")
		}
		// 追加 keyboard-interactive 以支持 publickey + MFA/OTP 链路（JumpServer / 堡垒机场景，issue #77）
		methods = append(methods, kbInteractive())
	case "keyboard-interactive":
		methods = append(methods, kbInteractive())
	default:
		return nil, fmt.Errorf("不支持的认证方式: %s", authType)
	}

	return methods, nil
}

func BuildAuthMethodsForProxyChain(authType, password, key, keyPassphrase string, privateKeyPaths []string) ([]ssh.AuthMethod, error) {
	return buildAuthMethods(authType, password, key, keyPassphrase, privateKeyPaths, nil)
}

// parsePrivateKey 解析私钥，支持 passphrase
func parsePrivateKey(data []byte, passphrase string) (ssh.Signer, error) {
	// 先尝试无 passphrase 解析
	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return signer, nil
	}
	// 如果失败且提供了 passphrase，尝试带 passphrase 解析
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
		if err != nil {
			return nil, fmt.Errorf("解析加密私钥失败（可能 passphrase 不正确）: %w", err)
		}
		return signer, nil
	}
	return nil, err
}

// readOutput 持续读取终端输出并回调
// 使用 timer 合并输出，减少高频 EventsEmit 调用导致前端事件队列阻塞
func (m *Manager) readOutput(sess *Session) {
	defer func() {
		if r := recover(); r != nil {
			logger.Default().Error("readOutput panic recovered",
				zap.String("sessionID", sess.ID),
				zap.Any("panic", r))
		}
		sess.Close()
		m.sessions.Delete(sess.ID)
	}()

	var pending bytes.Buffer
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	flush := func() {
		if pending.Len() > 0 && sess.onData != nil {
			data := make([]byte, pending.Len())
			copy(data, pending.Bytes())
			pending.Reset()
			sess.onData(data)
		}
	}

	type readResult struct {
		data []byte
		err  error
	}
	readCh := make(chan readResult, 4)

	go func() {
		buf := make([]byte, 32768)
		for {
			n, err := sess.stdout.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				readCh <- readResult{data: data}
			}
			if err != nil {
				readCh <- readResult{err: err}
				return
			}
		}
	}()

	for {
		select {
		case r := <-readCh:
			if r.err != nil {
				if len(sess.parserRemainder) > 0 {
					pending.Write(sess.parserRemainder)
					sess.parserRemainder = nil
				}
				flush()
				// Log the disconnect reason. io.EOF is the remote closing the
				// channel cleanly (user `exit`, or a server-side idle logout
				// such as sshd ClientAlive*/shell TMOUT, which client keepalive
				// cannot prevent). Anything else (reset/timeout) is an abnormal
				// transport drop. This is the one place that knows *why* a
				// session ended — without it diagnosis is blind.
				if errors.Is(r.err, io.EOF) {
					logger.Default().Info("ssh session ended: remote closed channel",
						zap.String("sessionID", sess.ID), zap.Int64("assetID", sess.AssetID))
				} else {
					logger.Default().Warn("ssh session dropped: transport read error",
						zap.String("sessionID", sess.ID), zap.Int64("assetID", sess.AssetID), zap.Error(r.err))
				}
				return
			}
			filtered := sess.filterOutput(r.data)
			if len(filtered) > 0 {
				pending.Write(filtered)
			}
			if pending.Len() >= 32*1024 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// GetSession 获取会话
func (m *Manager) GetSession(id string) (*Session, bool) {
	v, ok := m.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

// ListActiveSessionIDsByAsset 返回指定资产当前仍处于活跃态的 SSH 会话 ID。
// external edit 只允许在“同一资产且候选唯一”时做受限重绑，因此这里刻意只暴露最小会话列表，
// 不把终端层的更多运行态细节泄漏到上层业务。
func (m *Manager) ListActiveSessionIDsByAsset(assetID int64) []string {
	if assetID <= 0 {
		return nil
	}

	ids := make([]string, 0, 4)
	m.sessions.Range(func(key, value any) bool {
		sess, ok := value.(*Session)
		if !ok || sess == nil || sess.AssetID != assetID || sess.IsClosed() {
			return true
		}
		if id, ok := key.(string); ok && id != "" {
			ids = append(ids, id)
		}
		return true
	})
	sort.Strings(ids)
	return ids
}

// GetSessionSyncState 获取会话目录同步状态。
func (m *Manager) GetSessionSyncState(id string) (DirectorySyncState, error) {
	sess, ok := m.GetSession(id)
	if !ok {
		return DirectorySyncState{}, dirsync.Error(dirsync.CodeSessionNotFound)
	}
	return sess.GetSyncState(), nil
}

// Disconnect 断开指定会话
func (m *Manager) Disconnect(id string) {
	if sess, ok := m.GetSession(id); ok {
		sess.Close()
		m.sessions.Delete(id)
	}
}

// CloseAsset 断开指定资产的全部会话，返回被断开的会话 ID。
// 资产被删除时由 assetconn 广播过来，调用方据此清理挂在这些会话上的资源（如 SFTP 客户端）。
func (m *Manager) CloseAsset(assetID int64) []string {
	ids := m.ListActiveSessionIDsByAsset(assetID)
	for _, id := range ids {
		m.Disconnect(id)
	}
	return ids
}

// DisconnectAll 断开所有会话
func (m *Manager) DisconnectAll() {
	m.sessions.Range(func(key, value any) bool {
		value.(*Session).Close()
		m.sessions.Delete(key)
		return true
	})
}

// TestConnection 测试 SSH 连接（仅验证连通性，不创建会话）。
// ctx 取消时函数立即返回 ctx.Err()，后台 dial 仍会跑到 10s 兜底超时并自行清理。
func (m *Manager) TestConnection(ctx context.Context, cfg ConnectConfig) error {
	sshConfig, err := buildClientConfig(cfg)
	if err != nil {
		return err
	}
	// Agent 列举、签名、MFA 等待与握手取消都使用本调用方 ctx。
	cfg.Ctx = ctx

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	type dialResult struct {
		client  *ssh.Client
		closers []io.Closer
		err     error
	}
	done := make(chan dialResult, 1)
	go func() {
		c, cl, e := m.dial(cfg, sshConfig, addr)
		done <- dialResult{c, cl, e}
	}()

	select {
	case <-ctx.Done():
		// dial 还在跑：用单独的 goroutine 等它完成，结果到达后立刻关闭资源，避免泄漏。
		go func() {
			r := <-done
			if r.err == nil && r.client != nil {
				if cerr := r.client.Close(); cerr != nil {
					logger.Default().Warn("close orphaned test connection client", zap.Error(cerr))
				}
			}
			for _, c := range r.closers {
				if cerr := c.Close(); cerr != nil {
					logger.Default().Warn("close orphaned test connection resource", zap.Error(cerr))
				}
			}
		}()
		return ctx.Err()
	case r := <-done:
		if r.err != nil {
			return r.err
		}
		if err := r.client.Close(); err != nil {
			logger.Default().Warn("close test connection client", zap.Error(err))
		}
		for _, c := range r.closers {
			if err := c.Close(); err != nil {
				logger.Default().Warn("close test connection resource", zap.Error(err))
			}
		}
		return nil
	}
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
