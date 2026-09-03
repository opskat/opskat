package ssh_svc

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// --- 可控 SSH Agent 转发服务器 ---
//
// agentForwardServer 是 SSH Agent 转发路径的进程内 sshd 替身：接受任意口令认证、
// 服务交互式 "session" 通道（pty-req / shell），对
// auth-agent-req@openssh.com 按配置应答，并可像真实 sshd 收到远端 agent 请求时那样
// 反向打开 auth-agent@openssh.com 通道。
type agentForwardServer struct {
	t             *testing.T
	ln            net.Listener
	allowAgentReq bool

	connCh chan ssh.Conn

	mu        sync.Mutex
	agentReqs int
}

func newAgentForwardServer(t *testing.T, allowAgentReq bool) *agentForwardServer {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil },
	}
	cfg.AddHostKey(newTestHostKey(t))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &agentForwardServer{
		t:             t,
		ln:            ln,
		allowAgentReq: allowAgentReq,
		connCh:        make(chan ssh.Conn, 4),
	}
	go s.serve(cfg)
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *agentForwardServer) serve(cfg *ssh.ServerConfig) {
	for {
		raw, err := s.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
			if err != nil {
				_ = raw.Close()
				return
			}
			defer func() { _ = sc.Close() }()
			go ssh.DiscardRequests(reqs)
			s.connCh <- sc
			for nc := range chans {
				if nc.ChannelType() != "session" {
					_ = nc.Reject(ssh.UnknownChannelType, "unsupported channel")
					continue
				}
				go s.serveSession(nc)
			}
		}()
	}
}

// serveSession 应答一个交互式会话的 pty-req / shell，并按 allowAgentReq 决定是否
// 接受 auth-agent-req@openssh.com（服务器 AllowAgentForwarding no 时回失败）。
func (s *agentForwardServer) serveSession(nc ssh.NewChannel) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		ok := false
		switch req.Type {
		case "auth-agent-req@openssh.com":
			s.mu.Lock()
			s.agentReqs++
			s.mu.Unlock()
			ok = s.allowAgentReq
		case "pty-req", "shell", "env":
			ok = true
		}
		if req.WantReply {
			_ = req.Reply(ok, nil)
		}
	}
}

func (s *agentForwardServer) agentRequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentReqs
}

func (s *agentForwardServer) hostPort() (string, int) {
	host, portStr, err := net.SplitHostPort(s.ln.Addr().String())
	require.NoError(s.t, err)
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	require.NoError(s.t, err)
	return host, port
}

func (s *agentForwardServer) waitConn(d time.Duration) ssh.Conn {
	s.t.Helper()
	select {
	case c := <-s.connCh:
		return c
	case <-time.After(d):
		s.t.Fatalf("timed out waiting for the server side connection")
		return nil
	}
}

// listForwardedKeys 从服务端反向打开一个 auth-agent@openssh.com 通道，并列举被转发
// 的本地 Agent 持有的身份——远端 ssh-add -l 走的就是这条链路。
func listForwardedKeys(conn ssh.Conn) ([]*agent.Key, error) {
	ch, reqs, err := conn.OpenChannel(agentForwardChannelType, nil)
	if err != nil {
		return nil, err
	}
	go ssh.DiscardRequests(reqs)
	defer func() { _ = ch.Close() }()
	return agent.NewClient(ch).List()
}

// connectForwardingSession 走完整产品路径（Manager.Connect）建立一个开启 Agent
// 转发的终端会话。
func connectForwardingSession(t *testing.T, m *Manager, srv *agentForwardServer, sockPath string) string {
	t.Helper()
	host, port := srv.hostPort()
	sessionID, err := m.Connect(ConnectConfig{
		Host:         host,
		Port:         port,
		Username:     "u",
		AuthType:     "password",
		Password:     "p",
		AgentForward: &AgentForwardConfig{Source: agentSource(sockPath)},
		OnData:       func(string, []byte) {},
		OnClosed:     func(string) {},
	})
	require.NoError(t, err)
	t.Cleanup(func() { m.Disconnect(sessionID) })
	return sessionID
}

func TestAgentForwardingServesRemoteChannels(t *testing.T) {
	setupAgentFactoryTest(t)
	priv, pub := newTestKeyPair(t)
	agentSrv, _ := newKeyringAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "forwarded"})

	srv := newAgentForwardServer(t, true)
	m := NewManager()
	connectForwardingSession(t, m, srv, agentSrv.path)

	serverConn := srv.waitConn(5 * time.Second)
	keys, err := listForwardedKeys(serverConn)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.Equal(t, ssh.FingerprintSHA256(pub), ssh.FingerprintSHA256(keys[0]))
	require.Equal(t, 1, srv.agentRequestCount())
}

// 回归（每通道一条本地 Agent 连接）：本地 Agent 重启后，新打开的转发通道必须仍然
// 可用。单条长连接的实现会把整条 SSH 会话的转发永久打死。
func TestAgentForwardingSurvivesLocalAgentRestart(t *testing.T) {
	setupAgentFactoryTest(t)
	privBefore, _ := newTestKeyPair(t)
	privAfter, pubAfter := newTestKeyPair(t)
	sockPath := tempSocketPath(t)
	agentSrv, _ := serveKeyringAgent(t, sockPath, agent.AddedKey{PrivateKey: privBefore, Comment: "before"})

	srv := newAgentForwardServer(t, true)
	m := NewManager()
	connectForwardingSession(t, m, srv, sockPath)

	serverConn := srv.waitConn(5 * time.Second)
	_, err := listForwardedKeys(serverConn)
	require.NoError(t, err, "forwarding must work before the local agent restarts")

	// 本地 Agent 重启：监听与已建立连接全部断开，随后在同一 socket 路径上以另一把
	// 密钥重新提供服务。
	agentSrv.stop()
	_, _ = serveKeyringAgent(t, sockPath, agent.AddedKey{PrivateKey: privAfter, Comment: "after"})

	keys, err := listForwardedKeys(serverConn)
	require.NoError(t, err, "a channel opened after the local agent restarted must reach the new agent")
	require.Len(t, keys, 1)
	require.Equal(t, ssh.FingerprintSHA256(pubAfter), ssh.FingerprintSHA256(keys[0]))
}

// 服务器 AllowAgentForwarding no：auth-agent-req@openssh.com 被拒绝时终端仍要开起来
// （与 ssh -A 一致：告警并继续）。
func TestAgentForwardingRequestDeniedStillOpensTerminal(t *testing.T) {
	setupAgentFactoryTest(t)
	priv, _ := newTestKeyPair(t)
	agentSrv, _ := newKeyringAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})

	srv := newAgentForwardServer(t, false)
	m := NewManager()
	sessionID := connectForwardingSession(t, m, srv, agentSrv.path)

	require.Equal(t, 1, srv.agentRequestCount())
	sess, ok := m.GetSession(sessionID)
	require.True(t, ok)
	require.False(t, sess.IsClosed())
}

// 连接期探测：来源不可用时连接必须响亮失败，而不是连上后每次 agent 请求静默失败。
func TestAgentForwardingUnavailableSourceFailsConnect(t *testing.T) {
	setupAgentFactoryTest(t)
	srv := newAgentForwardServer(t, true)
	host, port := srv.hostPort()

	m := NewManager()
	_, err := m.Connect(ConnectConfig{
		Host:         host,
		Port:         port,
		Username:     "u",
		AuthType:     "password",
		Password:     "p",
		AgentForward: &AgentForwardConfig{Source: agentSource(tempSocketPath(t))},
		OnData:       func(string, []byte) {},
		OnClosed:     func(string) {},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "SSH Agent 转发")
}
