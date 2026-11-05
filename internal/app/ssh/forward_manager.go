package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/opskat/opskat/internal/app/sshadapt"
	"github.com/opskat/opskat/internal/model/entity/forward_entity"
	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/internal/repository/forward_repo"
	"github.com/opskat/opskat/internal/service/forward_svc"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// SSHDialer SSH 连接接口，供 ForwardManager 使用
type SSHDialer interface {
	DialAsset(ctx context.Context, assetID int64) (*ssh.Client, []io.Closer, error)
}

// ForwardManager 管理端口转发的运行时状态
type ForwardManager struct {
	dialer  SSHDialer
	mu      sync.Mutex
	clients map[int64]*forwardClient  // assetID → SSH 连接
	running map[int64]*runningForward // ruleID → 运行状态
	dialing map[int64]*dialCall       // assetID → 进行中的拨号（同资产并发只拨一次）
	// starting 按配置串行化 StartConfig / StopConfig：同一配置并发启动不互相覆盖，
	// 启动途中（拨号、Listen）到达的停止排在启动之后执行，不会被启动覆盖。
	// 锁序：starting 里的锁在 m.mu 之前取。
	starting map[int64]*sync.Mutex
}

// dialCall 一次进行中的 SSH 拨号，等待者共享结果。字段在 m.mu 下读写，done 关闭后只读。
type dialCall struct {
	done chan struct{}
	fc   *forwardClient
	err  error
	// abandoned：拨号用的是发起方的 ctx，失败是因为它结束了；等待者 ctx 仍有效时重新拨号。
	abandoned bool
	// closed：拨号期间 CloseAsset / StopAll 要求关掉这个资产的连接，拨出的连接不再入表。
	closed bool
}

// errForwardClientClosed 连接在拨号或启动规则期间被 CloseAsset / StopAll 关闭。
var errForwardClientClosed = errors.New("SSH 连接已被关闭")

// forwardClient 每个资产对应的 SSH 连接
type forwardClient struct {
	client  *ssh.Client
	closers []io.Closer
	refs    int32
}

// close 关闭 SSH 连接及其中间连接。
func (fc *forwardClient) close(assetID int64) {
	if err := fc.client.Close(); err != nil {
		logger.Default().Warn("close SSH client", zap.Int64("assetID", assetID), zap.Error(err))
	}
	for _, c := range fc.closers {
		if err := c.Close(); err != nil {
			logger.Default().Warn("close closer", zap.Int64("assetID", assetID), zap.Error(err))
		}
	}
}

// runningForward 单条转发规则的运行状态
type runningForward struct {
	configID int64
	assetID  int64
	cancel   context.CancelFunc
	errMsg   string // 非空表示失败
}

// RuleStatus 返回给前端的规则运行状态
type RuleStatus = forward_svc.RuleStatus

// ForwardConfigWithStatus 配置 + 规则 + 运行状态
type ForwardConfigWithStatus struct {
	forward_entity.ForwardConfig
	AssetName string           `json:"assetName"`
	Rules     []RuleWithStatus `json:"rules"`
	Status    string           `json:"status"`
}

// RuleWithStatus 规则 + 运行状态
type RuleWithStatus struct {
	forward_entity.ForwardRule
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// poolDialer 通过 sshadapt 复用同一份 credential_resolver 拨号逻辑。
type poolDialer = sshadapt.PoolDialer

func NewForwardManager(dialer SSHDialer) *ForwardManager {
	return &ForwardManager{
		dialer:   dialer,
		clients:  make(map[int64]*forwardClient),
		running:  make(map[int64]*runningForward),
		dialing:  make(map[int64]*dialCall),
		starting: make(map[int64]*sync.Mutex),
	}
}

// StartConfig 启动一个转发配置的所有规则
func (m *ForwardManager) StartConfig(ctx context.Context, configID int64) error {
	config, err := forward_repo.Forward().FindConfig(ctx, configID)
	if err != nil {
		return fmt.Errorf("配置不存在: %w", err)
	}
	rules, err := forward_repo.Forward().ListRulesByConfigID(ctx, configID)
	if err != nil {
		return fmt.Errorf("读取规则失败: %w", err)
	}
	if len(rules) == 0 {
		return fmt.Errorf("配置中没有转发规则")
	}

	startMu := m.configStartLock(configID)
	startMu.Lock()
	defer startMu.Unlock()

	// 先停掉已有的（同一配置重新启动）
	m.stopConfig(configID)

	// 获取或创建 SSH 连接；拨号期间不持有 m.mu。
	// 引用计数在拿到连接的同一把锁内一次性加上，避免其它配置的 Stop 在
	// 我们注册规则前把连接引用归零而关掉。
	fc, err := m.acquireClient(ctx, config.AssetID, int32(len(rules)))
	if err != nil {
		return fmt.Errorf("SSH 连接失败: %w", err)
	}

	// 先注册运行状态、再在锁外启动（Listen / 远端 listen）：启动途中到达的 CloseAsset /
	// StopAll 能看到并取消这些规则，规则的 ctx 一取消监听随之关闭。注册与"连接仍是这个
	// 资产的连接"的确认在同一把锁内，连接已被关掉就不再注册。
	m.mu.Lock()
	if m.clients[config.AssetID] != fc {
		m.mu.Unlock()
		return fmt.Errorf("SSH 连接失败: %w", errForwardClientClosed)
	}
	type pendingRule struct {
		rule *forward_entity.ForwardRule
		rf   *runningForward
		ctx  context.Context
	}
	pending := make([]pendingRule, 0, len(rules))
	for _, rule := range rules {
		rctx, cancel := context.WithCancel(context.Background())
		rf := &runningForward{configID: configID, assetID: config.AssetID, cancel: cancel}
		m.running[rule.ID] = rf
		pending = append(pending, pendingRule{rule: rule, rf: rf, ctx: rctx})
	}
	m.mu.Unlock()

	for _, p := range pending {
		if startErr := startForwardRule(p.ctx, fc.client, p.rule); startErr != nil {
			p.rf.cancel()
			logger.Default().Error("rule failed", zap.Int64("ruleID", p.rule.ID), zap.Error(startErr))
			m.mu.Lock()
			p.rf.errMsg = startErr.Error()
			m.mu.Unlock()
		}
	}

	return nil
}

func (m *ForwardManager) configStartLock(configID int64) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.starting[configID]
	if !ok {
		l = &sync.Mutex{}
		m.starting[configID] = l
	}
	return l
}

// StopConfig 停止一个转发配置的所有规则。该配置正在启动时，等启动完成后再停。
func (m *ForwardManager) StopConfig(configID int64) {
	startMu := m.configStartLock(configID)
	startMu.Lock()
	defer startMu.Unlock()
	m.stopConfig(configID)
}

// stopConfig 是 StopConfig 去掉启动锁的部分，调用方持有该配置的启动锁。
func (m *ForwardManager) stopConfig(configID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopConfigLocked(configID)
}

func (m *ForwardManager) stopConfigLocked(configID int64) {
	for ruleID, rf := range m.running {
		if rf.configID == configID {
			rf.cancel()
			m.releaseClientLocked(rf.assetID)
			delete(m.running, ruleID)
		}
	}
}

// CloseAsset 停掉指定资产的全部转发规则并关闭它的 SSH 连接。
// 不走 releaseClientLocked：那是按引用计数递减，而资产已经被删除，
// 这条连接不会再有新的使用者，必须直接关掉，否则本地监听端口会一直挂着。
func (m *ForwardManager) CloseAsset(assetID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ruleID, rf := range m.running {
		if rf.assetID == assetID {
			rf.cancel()
			delete(m.running, ruleID)
		}
	}
	if call, ok := m.dialing[assetID]; ok {
		call.closed = true
	}
	fc, ok := m.clients[assetID]
	if !ok {
		return
	}
	delete(m.clients, assetID)
	fc.close(assetID)
}

// StopAll 停止所有转发
func (m *ForwardManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ruleID, rf := range m.running {
		rf.cancel()
		delete(m.running, ruleID)
	}
	for _, call := range m.dialing {
		call.closed = true
	}
	for assetID, fc := range m.clients {
		fc.close(assetID)
		delete(m.clients, assetID)
	}
}

// GetConfigStatus 获取指定配置的运行状态
func (m *ForwardManager) GetConfigStatus(configID int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	running, errored, total := 0, 0, 0
	for _, rf := range m.running {
		if rf.configID == configID {
			total++
			if rf.errMsg != "" {
				errored++
			} else {
				running++
			}
		}
	}
	if total == 0 {
		return "stopped"
	}
	if errored == total {
		return "error"
	}
	if running == total {
		return "running"
	}
	return "partial"
}

// GetRuleStatus 获取单条规则的运行状态
func (m *ForwardManager) GetRuleStatus(ruleID int64) RuleStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	rf, ok := m.running[ruleID]
	if !ok {
		return RuleStatus{RuleID: ruleID, Status: "stopped"}
	}
	if rf.errMsg != "" {
		return RuleStatus{RuleID: ruleID, Status: "error", Error: rf.errMsg}
	}
	return RuleStatus{RuleID: ruleID, Status: "running"}
}

// IsConfigRunning 检查配置是否有运行中的规则
func (m *ForwardManager) IsConfigRunning(configID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rf := range m.running {
		if rf.configID == configID {
			return true
		}
	}
	return false
}

// --- SSH 连接管理 ---

// acquireClient 返回资产的 SSH 连接并给它加 refs 个引用。拨号在锁外进行，
// 同一资产的并发调用共享同一次拨号。
func (m *ForwardManager) acquireClient(ctx context.Context, assetID int64, refs int32) (*forwardClient, error) {
	for {
		m.mu.Lock()
		if fc, ok := m.clients[assetID]; ok {
			atomic.AddInt32(&fc.refs, refs)
			m.mu.Unlock()
			return fc, nil
		}
		if call, ok := m.dialing[assetID]; ok {
			m.mu.Unlock()
			select {
			case <-call.done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if call.abandoned {
				continue // 发起方的 ctx 结束了，我们的还在：重新拨号
			}
			if call.err != nil {
				return nil, call.err
			}
			continue // 重新取锁，按普通路径拿到连接并加引用
		}
		call := &dialCall{done: make(chan struct{})}
		m.dialing[assetID] = call
		m.mu.Unlock()

		client, closers, err := m.dialer.DialAsset(ctx, assetID)

		m.mu.Lock()
		delete(m.dialing, assetID)
		var orphan *forwardClient
		switch {
		case err != nil:
			call.err = err
			call.abandoned = ctx.Err() != nil
		case call.closed:
			orphan = &forwardClient{client: client, closers: closers}
			call.err = errForwardClientClosed
		default:
			call.fc = &forwardClient{client: client, closers: closers}
			m.clients[assetID] = call.fc
			atomic.AddInt32(&call.fc.refs, refs)
		}
		m.mu.Unlock()
		close(call.done)
		if orphan != nil {
			orphan.close(assetID)
		}
		return call.fc, call.err
	}
}

func (m *ForwardManager) releaseClientLocked(assetID int64) {
	fc, ok := m.clients[assetID]
	if !ok {
		return
	}
	if atomic.AddInt32(&fc.refs, -1) <= 0 {
		fc.close(assetID)
		delete(m.clients, assetID)
	}
}

// --- 端口转发启动 ---

func startForwardRule(ctx context.Context, client *ssh.Client, rule *forward_entity.ForwardRule) error {
	switch rule.Type {
	case "local":
		return startLocalForward(ctx, client, rule)
	case "remote":
		return startRemoteForward(ctx, client, rule)
	case "dynamic":
		return startDynamicForward(ctx, client, rule)
	default:
		return fmt.Errorf("unsupported type: %s", rule.Type)
	}
}

func startLocalForward(ctx context.Context, client *ssh.Client, rule *forward_entity.ForwardRule) error {
	addr := fmt.Sprintf("%s:%d", rule.LocalHost, rule.LocalPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	go func() {
		<-ctx.Done()
		if err := listener.Close(); err != nil {
			logger.Default().Warn("close listener", zap.Error(err))
		}
	}()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				remote := fmt.Sprintf("%s:%d", rule.RemoteHost, rule.RemotePort)
				rconn, err := client.Dial("tcp", remote)
				if err != nil {
					if closeErr := conn.Close(); closeErr != nil {
						logger.Default().Warn("close conn", zap.Error(closeErr))
					}
					return
				}
				pipeConns(conn, rconn)
			}()
		}
	}()

	return nil
}

func startRemoteForward(ctx context.Context, client *ssh.Client, rule *forward_entity.ForwardRule) error {
	addr := fmt.Sprintf("%s:%d", rule.RemoteHost, rule.RemotePort)
	listener, err := client.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("remote listen %s: %w", addr, err)
	}

	go func() {
		<-ctx.Done()
		if err := listener.Close(); err != nil {
			logger.Default().Warn("close listener", zap.Error(err))
		}
	}()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				local := net.JoinHostPort(rule.LocalHost, fmt.Sprintf("%d", rule.LocalPort))
				lconn, err := (&netdial.Dialer{}).DialContext(ctx, "tcp", local)
				if err != nil {
					if closeErr := conn.Close(); closeErr != nil {
						logger.Default().Warn("close conn", zap.Error(closeErr))
					}
					return
				}
				pipeConns(conn, lconn)
			}()
		}
	}()

	return nil
}

// startDynamicForward SOCKS5 代理：本地监听，通过 SSH 隧道代理所有连接
func startDynamicForward(ctx context.Context, client *ssh.Client, rule *forward_entity.ForwardRule) error {
	addr := fmt.Sprintf("%s:%d", rule.LocalHost, rule.LocalPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	go func() {
		<-ctx.Done()
		if err := listener.Close(); err != nil {
			logger.Default().Warn("close listener", zap.Error(err))
		}
	}()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5(conn, client)
		}
	}()

	return nil
}

// handleSOCKS5 处理一个 SOCKS5 CONNECT 请求
func handleSOCKS5(conn net.Conn, client *ssh.Client) {
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close SOCKS5 conn", zap.Error(err))
		}
	}()

	// 1. 握手
	buf := make([]byte, 258)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 { // SOCKS5
		return
	}
	nMethods := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:nMethods]); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		logger.Default().Warn("SOCKS5 auth reply", zap.Error(err))
		return
	}

	// 2. 读取连接请求
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return
	}
	if buf[1] != 0x01 { // 只支持 CONNECT
		if _, err := conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
			logger.Default().Warn("SOCKS5 write command-not-supported", zap.Error(err))
		}
		return
	}

	var host string
	switch buf[3] { // address type
	case 0x01: // IPv4
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // Domain
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		domainLen := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:domainLen]); err != nil {
			return
		}
		host = string(buf[:domainLen])
	case 0x04: // IPv6
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		if _, err := conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
			logger.Default().Warn("SOCKS5 write addr-type-not-supported", zap.Error(err))
		}
		return
	}

	// 读端口
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	port := int(buf[0])<<8 | int(buf[1])
	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// 3. 通过 SSH 隧道连接目标
	rconn, err := client.Dial("tcp", target)
	if err != nil {
		if _, err := conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
			logger.Default().Warn("SOCKS5 write connection-refused", zap.Error(err))
		}
		return
	}

	// 4. 回复成功
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		logger.Default().Warn("SOCKS5 write success reply", zap.Error(err))
		return
	}

	// 5. 双向转发
	pipeConns(conn, rconn)
}

func pipeConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		if _, err := io.Copy(dst, src); err != nil {
			logger.Default().Warn("pipe copy", zap.Error(err))
		}
		if err := dst.Close(); err != nil {
			logger.Default().Warn("pipe close", zap.Error(err))
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}
