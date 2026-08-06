package ssh_svc

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/opskat/opskat/internal/repository/host_key_repo"
	"github.com/opskat/opskat/internal/sshagent"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"gorm.io/gorm"
)

// --- 可控 SSH 服务器（记录提供的公钥，供认证状态机断言） ---

type controllableSSHServer struct {
	t    *testing.T
	ln   net.Listener
	addr string
	cfg  *ssh.ServerConfig

	mu      sync.Mutex
	offered []string
}

func (s *controllableSSHServer) offeredFingerprints() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.offered...)
}

func (s *controllableSSHServer) record(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.offered {
		if f == fp {
			return
		}
	}
	s.offered = append(s.offered, fp)
}

// newControllableSSHServer 在 127.0.0.1:0 上运行一个 SSH 服务器，记录每次用户认证
// 提供的公钥。返回的 channel 在连接被任一方关闭时触发。
func newControllableSSHServer(t *testing.T, cfg *ssh.ServerConfig) (*controllableSSHServer, chan struct{}) {
	t.Helper()
	s := &controllableSSHServer{t: t}
	inner := cfg.PublicKeyCallback
	cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		s.record(sshagent.FingerprintSHA256(key))
		if inner != nil {
			return inner(c, key)
		}
		return nil, nil
	}
	cfg.AddHostKey(newTestHostKey(t))
	s.cfg = cfg

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.ln = ln
	s.addr = ln.Addr().String()

	connClosed := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer close(connClosed)
				_, _, _, err := ssh.NewServerConn(conn, s.cfg)
				if err != nil {
					return
				}
				var b [1]byte
				_, _ = conn.Read(b[:])
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s, connClosed
}

func (s *controllableSSHServer) hostPort() (string, int) {
	host, portStr, err := net.SplitHostPort(s.addr)
	if err != nil {
		s.t.Fatalf("split addr: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		s.t.Fatalf("parse port: %v", err)
	}
	return host, port
}

func newTestHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new host signer: %v", err)
	}
	return s
}

func newTestKeyPair(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	_ = pub
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return priv, s.PublicKey()
}

// --- 内存 keyring agent（unix socket） ---

type testAgentServer struct {
	ln   net.Listener
	path string
}

func newKeyringAgent(t *testing.T, keys ...agent.AddedKey) (*testAgentServer, chan struct{}) {
	t.Helper()
	kr := agent.NewKeyring()
	for _, k := range keys {
		if err := kr.Add(k); err != nil {
			t.Fatalf("add key: %v", err)
		}
	}
	path := filepath.Join(t.TempDir(), "a")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	connClosed := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer close(connClosed)
				defer func() { _ = conn.Close() }()
				_ = agent.ServeAgent(kr, conn)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return &testAgentServer{ln: ln, path: path}, connClosed
}

func waitClose(ch chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// jumpForwardServer 是支持 direct-tcpip 转发（跳板机链路）的 SSH 服务器。
// NewServerConn 后的 channel/request 由它完整服务，因此客户端可以经它转发到目标。
type jumpForwardServer struct {
	t    *testing.T
	ln   net.Listener
	addr string
}

func newJumpForwardServer(t *testing.T, cfg *ssh.ServerConfig) *jumpForwardServer {
	t.Helper()
	if cfg.PublicKeyCallback == nil {
		cfg.PublicKeyCallback = func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, nil
		}
	}
	cfg.AddHostKey(newTestHostKey(t))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &jumpForwardServer{t: t, ln: ln, addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				serverConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer func() { _ = serverConn.Close() }()
				go ssh.DiscardRequests(reqs)
				for newChan := range chans {
					if newChan.ChannelType() != "direct-tcpip" {
						_ = newChan.Reject(ssh.UnknownChannelType, "unsupported channel")
						continue
					}
					go handleDirectTCPIP(newChan)
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *jumpForwardServer) hostPort() (string, int) {
	host, portStr, err := net.SplitHostPort(s.addr)
	if err != nil {
		s.t.Fatalf("split addr: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		s.t.Fatalf("parse port: %v", err)
	}
	return host, port
}

// handleDirectTCPIP 桥接一个 direct-tcpip 通道到其目标地址。
func handleDirectTCPIP(newChan ssh.NewChannel) {
	type directTCPIPData struct {
		DestAddr string
		DestPort uint32
		SrcAddr  string
		SrcPort  uint32
	}
	var d directTCPIPData
	if err := ssh.Unmarshal(newChan.ExtraData(), &d); err != nil {
		_ = newChan.Reject(ssh.ConnectionFailed, "bad direct-tcpip payload")
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(d.DestAddr, fmt.Sprintf("%d", d.DestPort)))
	if err != nil {
		_ = newChan.Reject(ssh.ConnectionFailed, "cannot dial target")
		return
	}
	ch, reqs, err := newChan.Accept()
	if err != nil {
		_ = target.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		defer func() { _ = ch.Close() }()
		defer func() { _ = target.Close() }()
		_, _ = io.Copy(ch, target)
	}()
	go func() {
		defer func() { _ = ch.Close() }()
		defer func() { _ = target.Close() }()
		_, _ = io.Copy(target, ch)
	}()
}

func agentSource(path string) func(context.Context) (sshagent.Source, error) {
	return func(context.Context) (sshagent.Source, error) {
		return sshagent.Source{Type: sshagent.EndpointTypeUnixSocket, Value: path}, nil
	}
}

// agentCode 返回 sshagent 类型化错误码。
func agentCode(err error) string {
	code, _ := sshagent.CodeOf(err)
	return code
}

// setupAgentFactoryTest 注册主机密钥仓库（in-memory SQLite），供 Agent 主机密钥
// 契约查询/保存使用。
func setupAgentFactoryTest(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&host_key_entity.HostKey{}))
	db.SetDefault(gdb)
	host_key_repo.RegisterHostKey(host_key_repo.NewHostKey())
	return context.Background()
}

func TestManagerDialAgentAuth(t *testing.T) {
	setupAgentFactoryTest(t)
	convey.Convey("Manager.Dial 经 Agent 认证工厂建立连接", t, func() {
		convey.Convey("只提供所选密钥、签名器被使用、握手后关闭 Agent 传输", func() {
			privA, pubA := newTestKeyPair(t)
			privB, pubB := newTestKeyPair(t)
			agentSrv, agentClosed := newKeyringAgent(t,
				agent.AddedKey{PrivateKey: privA, Comment: "key a"},
				agent.AddedKey{PrivateKey: privB, Comment: "key b"},
			)
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{})
			host, port := sshSrv.hostPort()

			m := NewManager()
			client, closers, err := m.Dial(ConnectConfig{
				Host:     host,
				Port:     port,
				Username: "alice",
				AuthType: "agent",
				Agent: &AgentConfig{
					Source:      agentSource(agentSrv.path),
					Fingerprint: sshagent.FingerprintSHA256(pubB),
				},
				HostKeyVerifyFunc: AutoTrustFirstRejectChangeVerifyFunc(),
			})
			convey.So(err, convey.ShouldBeNil)
			convey.So(client, convey.ShouldNotBeNil)
			defer func() { _ = client.Close() }()
			for _, c := range closers {
				defer func(c io.Closer) { _ = c.Close() }(c)
			}

			// 只向服务器提供了所选公钥，绝无第二个密钥。
			convey.So(sshSrv.offeredFingerprints(), convey.ShouldResemble,
				[]string{sshagent.FingerprintSHA256(pubB)})
			convey.So(sshSrv.offeredFingerprints(), convey.ShouldNotContain,
				sshagent.FingerprintSHA256(pubA))
			// 执行握手的组件拥有 Agent 传输：客户端返回前已关闭。
			convey.So(waitClose(agentClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("缺省 verifier 时 Agent 模式关闭失败（不回退 InsecureIgnoreHostKey）", func() {
			priv, _ := newTestKeyPair(t)
			agentSrv, _ := newKeyringAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{})
			host, port := sshSrv.hostPort()

			m := NewManager()
			_, _, err := m.Dial(ConnectConfig{
				Host:     host,
				Port:     port,
				Username: "alice",
				AuthType: "agent",
				Agent: &AgentConfig{
					Source:      agentSource(agentSrv.path),
					Fingerprint: sshagent.FingerprintSHA256(mustPub(t, priv)),
				},
				// HostKeyVerifyFunc 缺失：Agent 主机密钥契约必须关闭失败。
			})
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(agentCode(err), convey.ShouldEqual, sshagent.CodeHostKeyVerifierMissing)
		})

		convey.Convey("非交互新建连接需要 MFA 时返回 ssh_agent_mfa_required", func() {
			priv, pub := newTestKeyPair(t)
			agentSrv, _ := newKeyringAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			// 服务器：精确公钥部分成功后要求 keyboard-interactive（必须发出挑战，
			// 否则客户端非交互方无需应答即可成功）。
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
					return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
						KeyboardInteractiveCallback: func(c ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
							_, err := ch("Verification", "Enter code", []string{"Code: "}, []bool{false})
							return nil, err
						},
					}}
				},
			})
			host, port := sshSrv.hostPort()

			m := NewManager()
			_, _, err := m.Dial(ConnectConfig{
				Host:     host,
				Port:     port,
				Username: "alice",
				AuthType: "agent",
				Agent: &AgentConfig{
					Source:      agentSource(agentSrv.path),
					Fingerprint: sshagent.FingerprintSHA256(pub),
					// MFA nil = 非交互。
				},
				HostKeyVerifyFunc: AutoTrustFirstRejectChangeVerifyFunc(),
			})
			convey.So(err, convey.ShouldNotBeNil)
			convey.So(agentCode(err), convey.ShouldEqual, sshagent.CodeMFARequired)
		})

		convey.Convey("交互式调用方经结构化挑战完成 MFA 后连接成功", func() {
			priv, pub := newTestKeyPair(t)
			agentSrv, agentClosed := newKeyringAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
					return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
						KeyboardInteractiveCallback: func(c ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
							answers, err := ch("Verification", "Enter code", []string{"Code: "}, []bool{false})
							if err != nil {
								return nil, err
							}
							if len(answers) != 1 || answers[0] != "123456" {
								return nil, fmt.Errorf("bad answers")
							}
							return nil, nil
						},
					}}
				},
			})
			host, port := sshSrv.hostPort()

			caller := &recordingAgentCaller{answers: []string{"123456"}}
			m := NewManager()
			client, closers, err := m.Dial(ConnectConfig{
				Host:     host,
				Port:     port,
				Username: "alice",
				AuthType: "agent",
				Agent: &AgentConfig{
					Source:      agentSource(agentSrv.path),
					Fingerprint: sshagent.FingerprintSHA256(pub),
					MFA:         caller,
				},
				HostKeyVerifyFunc: AutoTrustFirstRejectChangeVerifyFunc(),
			})
			convey.So(err, convey.ShouldBeNil)
			convey.So(client, convey.ShouldNotBeNil)
			defer func() { _ = client.Close() }()
			for _, c := range closers {
				defer func(c io.Closer) { _ = c.Close() }(c)
			}

			convey.So(caller.challengeCount(), convey.ShouldEqual, 1)
			ch := caller.first()
			convey.So(ch.Name, convey.ShouldEqual, "Verification")
			convey.So(ch.Prompts, convey.ShouldResemble, []string{"Code: "})
			convey.So(ch.Echo, convey.ShouldResemble, []bool{false})
			convey.So(waitClose(agentClosed, time.Second), convey.ShouldBeTrue)
		})
		convey.Convey("跳板机层同样经 Agent 认证工厂（旧 jump-host 字段无单独回退）", func() {
			privA, pubA := newTestKeyPair(t)
			privB, pubB := newTestKeyPair(t)
			// 跳板机与目标都用 Agent 认证，两个来源分别持有各自密钥。
			jumpAgent, _ := newKeyringAgent(t, agent.AddedKey{PrivateKey: privA, Comment: "jump"})
			targetAgent, targetAgentClosed := newKeyringAgent(t, agent.AddedKey{PrivateKey: privB, Comment: "target"})

			jumpSrv := newJumpForwardServer(t, &ssh.ServerConfig{})
			targetSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{})
			targetHost, targetPort := targetSrv.hostPort()
			jumpHost, jumpPort := jumpSrv.hostPort()

			m := NewManager()
			client, closers, err := m.Dial(ConnectConfig{
				Host:     targetHost,
				Port:     targetPort,
				Username: "alice",
				AuthType: "agent",
				Agent: &AgentConfig{
					Source:      agentSource(targetAgent.path),
					Fingerprint: sshagent.FingerprintSHA256(pubB),
				},
				JumpHosts: []JumpHostEntry{{
					Host:     jumpHost,
					Port:     jumpPort,
					Username: "bob",
					AuthType: "agent",
					Agent: &AgentConfig{
						Source:      agentSource(jumpAgent.path),
						Fingerprint: sshagent.FingerprintSHA256(pubA),
					},
				}},
				HostKeyVerifyFunc: AutoTrustFirstRejectChangeVerifyFunc(),
			})
			convey.So(err, convey.ShouldBeNil)
			convey.So(client, convey.ShouldNotBeNil)
			defer func() { _ = client.Close() }()
			for _, c := range closers {
				defer func(c io.Closer) { _ = c.Close() }(c)
			}

			// 两个 SSH 层都只向各自服务器提供了所选密钥。
			convey.So(targetSrv.offeredFingerprints(), convey.ShouldResemble,
				[]string{sshagent.FingerprintSHA256(pubB)})
			convey.So(waitClose(targetAgentClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("取消握手时关闭 Agent 传输并停止等待", func() {
			priv, pub := newTestKeyPair(t)
			agentSrv, agentClosed := newKeyringAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			// 服务器部分成功后发一个 MFA 挑战；交互方阻塞等待答案，期间取消。
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
					return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
						KeyboardInteractiveCallback: func(c ssh.ConnMetadata, ch ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
							_, err := ch("Verification", "Enter code", []string{"Code: "}, []bool{false})
							return nil, err
						},
					}}
				},
			})
			host, port := sshSrv.hostPort()

			ctx, cancel := context.WithCancel(context.Background())
			blocked := make(chan struct{})
			caller := &blockingAgentCaller{blocked: blocked}

			m := NewManager()
			done := make(chan error, 1)
			go func() {
				_, _, err := m.Dial(ConnectConfig{
					Host:     host,
					Port:     port,
					Username: "alice",
					AuthType: "agent",
					Ctx:      ctx,
					Agent: &AgentConfig{
						Source:      agentSource(agentSrv.path),
						Fingerprint: sshagent.FingerprintSHA256(pub),
						MFA:         caller,
					},
					HostKeyVerifyFunc: AutoTrustFirstRejectChangeVerifyFunc(),
				})
				done <- err
			}()

			select {
			case <-blocked:
			case <-time.After(2 * time.Second):
				convey.So("timed out waiting for MFA wait to start", convey.ShouldBeNil)
			}
			cancel()

			select {
			case err := <-done:
				convey.So(agentCode(err), convey.ShouldEqual, sshagent.CodeCancelled)
			case <-time.After(2 * time.Second):
				convey.So("timed out waiting for canceled dial", convey.ShouldBeNil)
			}
			convey.So(waitClose(agentClosed, time.Second), convey.ShouldBeTrue)
		})
	})
}

// blockingAgentCaller 阻塞在挑战上，直到 ctx 取消。
type blockingAgentCaller struct {
	blocked chan struct{}
	once    sync.Once
}

func (b *blockingAgentCaller) SubmitChallenge(ctx context.Context, _ sshagent.MFAChallenge) ([]string, error) {
	b.once.Do(func() { close(b.blocked) })
	<-ctx.Done()
	return nil, &sshagent.Error{Code: sshagent.CodeCancelled, Message: "challenge canceled"}
}

type recordingAgentCaller struct {
	mu      sync.Mutex
	calls   []sshagent.MFAChallenge
	answers []string
}

func (r *recordingAgentCaller) SubmitChallenge(_ context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, ch)
	return append([]string(nil), r.answers...), nil
}

func (r *recordingAgentCaller) challengeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingAgentCaller) first() sshagent.MFAChallenge {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[0]
}

func mustPub(t *testing.T, priv ed25519.PrivateKey) ssh.PublicKey {
	t.Helper()
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return s.PublicKey()
}
