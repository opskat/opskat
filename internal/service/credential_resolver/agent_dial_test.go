package credential_resolver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/glebarez/sqlite"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/host_key_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/host_key_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/sshagent"
)

func setupAgentDialTest(t *testing.T) context.Context {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(
		&ssh_agent_source_entity.SSHAgentSource{},
		&asset_entity.Asset{},
		&host_key_entity.HostKey{},
	))
	db.SetDefault(gdb)
	ssh_agent_source_repo.RegisterSSHAgentSource(ssh_agent_source_repo.New())
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	host_key_repo.RegisterHostKey(host_key_repo.NewHostKey())
	return context.Background()
}

type dialTestSSHServer struct {
	t    *testing.T
	ln   net.Listener
	addr string
	mu   sync.Mutex
	seen []string
}

func (s *dialTestSSHServer) record(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.seen {
		if f == fp {
			return
		}
	}
	s.seen = append(s.seen, fp)
}

func (s *dialTestSSHServer) offeredFingerprints() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func newDialTestSSHServer(t *testing.T, cfg *ssh.ServerConfig) *dialTestSSHServer {
	t.Helper()
	s := &dialTestSSHServer{t: t}
	inner := cfg.PublicKeyCallback
	cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		s.record(sshagent.FingerprintSHA256(key))
		if inner != nil {
			return inner(c, key)
		}
		return nil, nil
	}
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	require.NoError(t, err)
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s.ln = ln
	s.addr = ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _, _, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				var b [1]byte
				_, _ = conn.Read(b[:])
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *dialTestSSHServer) hostPort() (string, int) {
	host, portStr, err := net.SplitHostPort(s.addr)
	require.NoError(s.t, err)
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	require.NoError(s.t, err)
	return host, port
}

func newDialTestAgent(t *testing.T, keys ...agent.AddedKey) (string, chan struct{}) {
	t.Helper()
	kr := agent.NewKeyring()
	for _, k := range keys {
		require.NoError(t, kr.Add(k))
	}
	// macOS 限制 unix socket 路径 ≤104 字节，t.TempDir() 带长测试名会超限；
	// 用短随机目录避免跨平台路径超长。
	dir, err := os.MkdirTemp("", "a")
	require.NoError(t, err)
	path := filepath.Join(dir, "a")
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = ln.Close()
		_ = os.Remove(path)
		_ = os.Remove(dir)
	})
	closed := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer close(closed)
				defer func() { _ = conn.Close() }()
				_ = agent.ServeAgent(kr, conn)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return path, closed
}

func waitClose(ch chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// createAgentAsset 插入一个 auth_type=agent 的 SSH 资产，config 引用指定来源与指纹。
func createAgentAsset(t *testing.T, ctx context.Context, host string, port int, sourceID int64, fingerprint string) int64 {
	t.Helper()
	asset := &asset_entity.Asset{
		Name:       "box",
		Type:       asset_entity.AssetTypeSSH,
		Status:     asset_entity.StatusActive,
		Createtime: 1,
		Config: fmt.Sprintf(`{"host":%q,"port":%d,"username":"alice","auth_type":"agent","agent_source_id":%d,"agent_key_fingerprint":%q}`,
			host, port, sourceID, fingerprint),
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))
	return asset.ID
}

func TestDialAssetSSHAgentAuth(t *testing.T) {
	convey.Convey("DialAssetSSH 经 Agent 认证工厂建立连接（sshpool/AI/opsctl/隧道共用入口）", t, func() {
		convey.Convey("只提供所选密钥、握手后关闭 Agent 传输", func() {
			ctx := setupAgentDialTest(t)
			privA, pubA := testKeyPair(t)
			privB, pubB := testKeyPair(t)
			agentPath, agentClosed := newDialTestAgent(t,
				agent.AddedKey{PrivateKey: privA, Comment: "a"},
				agent.AddedKey{PrivateKey: privB, Comment: "b"},
			)
			src := &ssh_agent_source_entity.SSHAgentSource{
				Name: "work", EndpointType: "unix_socket", Endpoint: agentPath,
			}
			require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(ctx, src))

			sshSrv := newDialTestSSHServer(t, &ssh.ServerConfig{})
			host, port := sshSrv.hostPort()
			assetID := createAgentAsset(t, ctx, host, port, src.ID, sshagent.FingerprintSHA256(pubB))

			client, closers, err := Default().DialAssetSSH(ctx, assetID)
			assert.NoError(t, err)
			assert.NotNil(t, client)
			defer func() { _ = client.Close() }()
			for _, c := range closers {
				defer func(c interface{ Close() error }) { _ = c.Close() }(c)
			}

			assert.Equal(t, []string{sshagent.FingerprintSHA256(pubB)}, sshSrv.offeredFingerprints())
			assert.NotContains(t, sshSrv.offeredFingerprints(), sshagent.FingerprintSHA256(pubA))
			assert.True(t, waitClose(agentClosed, time.Second), "Agent 传输应在客户端返回前关闭")
		})

		convey.Convey("非交互新建连接需要 MFA 时返回 ssh_agent_mfa_required", func() {
			ctx := setupAgentDialTest(t)
			priv, pub := testKeyPair(t)
			agentPath, _ := newDialTestAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			src := &ssh_agent_source_entity.SSHAgentSource{
				Name: "work", EndpointType: "unix_socket", Endpoint: agentPath,
			}
			require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(ctx, src))

			sshSrv := newDialTestSSHServer(t, &ssh.ServerConfig{
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
			assetID := createAgentAsset(t, ctx, host, port, src.ID, sshagent.FingerprintSHA256(pub))

			_, _, err := Default().DialAssetSSH(ctx, assetID)
			assert.Error(t, err)
			code, ok := sshagent.CodeOf(err)
			assert.True(t, ok)
			assert.Equal(t, sshagent.CodeMFARequired, code)
		})
	})
}

func testKeyPair(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	s, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	return priv, s.PublicKey()
}

func TestResolveProxyChainAgentLayerUsesFactory(t *testing.T) {
	convey.Convey("代理链 SSH 层为 Agent 认证时经同一认证工厂（Handshake 接管握手）", t, func() {
		ctx := setupAgentDialTest(t)
		priv, pub := testKeyPair(t)
		agentPath, _ := newDialTestAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
		src := &ssh_agent_source_entity.SSHAgentSource{
			Name: "work", EndpointType: "unix_socket", Endpoint: agentPath,
		}
		require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(ctx, src))

		// 一个 agent SSH 资产作为代理链 SSH 层（不真正拨号，只断言握手接管）。
		proxyAsset := &asset_entity.Asset{
			Name: "tunnel", Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive, Createtime: 1,
			Config: fmt.Sprintf(`{"host":"127.0.0.1","port":1,"username":"u","auth_type":"agent","agent_source_id":%d,"agent_key_fingerprint":%q}`,
				src.ID, sshagent.FingerprintSHA256(pub)),
		}
		require.NoError(t, asset_repo.Asset().Create(ctx, proxyAsset))

		enabled := true
		chain := &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{
			{ID: "ssh-1", Name: "tunnel", Enabled: &enabled, Type: asset_entity.ProxyChainLayerSSH, Order: 1, SSHAssetID: proxyAsset.ID},
		}}
		layers, err := Default().ResolveProxyChain(ctx, chain, 5)
		require.NoError(t, err)
		require.Len(t, layers, 1)
		assert.Nil(t, layers[0].SSHConfig, "Agent 层不携带常规 SSHConfig")
		assert.NotNil(t, layers[0].Handshake, "Agent 层必须由 Handshake 接管握手（经认证工厂）")
	})
}
