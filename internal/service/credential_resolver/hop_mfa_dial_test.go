package credential_resolver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/pkg/sshtuning"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/sshagent"
)

type mfaFunc func(ctx context.Context, ch sshagent.MFAChallenge) ([]string, error)

func (f mfaFunc) SubmitChallenge(ctx context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	return f(ctx, ch)
}

// hopMFA is a responder registered through WithMFA: it records which asset each new
// connection's caller was made for and which asset each challenge was answered for.
// Like opsctl's --mfa-code caller, one caller answers one round only.
type hopMFA struct {
	answer     string
	mu         sync.Mutex
	callers    []int64
	challenged []int64
}

func (h *hopMFA) newCaller(assetID int64) sshagent.InteractiveCaller {
	h.mu.Lock()
	h.callers = append(h.callers, assetID)
	h.mu.Unlock()
	used := false
	return mfaFunc(func(context.Context, sshagent.MFAChallenge) ([]string, error) {
		if used {
			return nil, &sshagent.Error{Code: sshagent.CodeMFAFailed, Message: "one answer per connection"}
		}
		used = true
		h.mu.Lock()
		defer h.mu.Unlock()
		h.challenged = append(h.challenged, assetID)
		return []string{h.answer}, nil
	})
}

func (h *hopMFA) snapshot() (callers, challenged []int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]int64(nil), h.callers...), append([]int64(nil), h.challenged...)
}

// otpAfterPassword is otpAfterPublicKey with the password as the first factor.
func otpAfterPassword(want string) *ssh.ServerConfig {
	afterKey := otpAfterPublicKey(want).PublicKeyCallback
	return &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != "hop-password" {
				return nil, fmt.Errorf("bad password")
			}
			return afterKey(c, nil)
		},
	}
}

// newForwardingSSHServer is an SSH hop: it authenticates with cfg and forwards
// direct-tcpip channels to the address they name.
func newForwardingSSHServer(t *testing.T, cfg *ssh.ServerConfig) (string, int) {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	require.NoError(t, err)
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveForwarding(conn, cfg)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func serveForwarding(conn net.Conn, cfg *ssh.ServerConfig) {
	defer func() { _ = conn.Close() }()
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "direct-tcpip" {
			_ = nc.Reject(ssh.UnknownChannelType, "only direct-tcpip")
			continue
		}
		go forwardDirectTCPIP(nc)
	}
}

func forwardDirectTCPIP(nc ssh.NewChannel) {
	var target struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &target); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	upstream, err := net.Dial("tcp", net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))))
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, chReqs, err := nc.Accept()
	if err != nil {
		_ = upstream.Close()
		return
	}
	go ssh.DiscardRequests(chReqs)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, ch); done <- struct{}{} }()
	go func() { _, _ = io.Copy(ch, upstream); done <- struct{}{} }()
	<-done
	_ = ch.Close()
	_ = upstream.Close()
}

func createPasswordHopAsset(t *testing.T, ctx context.Context, host string, port int) int64 {
	t.Helper()
	encrypted, err := credential_svc.Default().Encrypt("hop-password")
	require.NoError(t, err)
	asset := &asset_entity.Asset{
		Name: "password-hop", Type: asset_entity.AssetTypeSSH, Status: asset_entity.StatusActive, Createtime: 1,
		Config: fmt.Sprintf(`{"host":%q,"port":%d,"username":"alice","auth_type":"password","password":%q}`, host, port, encrypted),
	}
	require.NoError(t, asset_repo.Asset().Create(ctx, asset))
	return asset.ID
}

func dialAndClose(ctx context.Context, assetID int64) error {
	client, closers, err := Default().DialAssetSSH(ctx, assetID)
	if err != nil {
		return err
	}
	_ = client.Close()
	for _, c := range closers {
		_ = c.Close()
	}
	return nil
}

func errCode(err error) string {
	code, _ := sshagent.CodeOf(err)
	return code
}

func TestDialAssetSSHAnswersMFAOnConnectionPathHop(t *testing.T) {
	convey.Convey("连接路径上要 MFA 的 SSH 跳板，由 ctx 里的应答方以跳板资产为对象应答", t, func() {
		ctx := setupAgentDialTest(t)
		setupCredentialSvc(t)
		plainTarget := func(hopID int64) int64 {
			host, port := newDialTestSSHServer(t, &ssh.ServerConfig{}).hostPort()
			return createHopAssetAt(t, ctx, host, port, hopID, "")
		}

		convey.Convey("密钥认证的跳板", func() {
			host, port := newForwardingSSHServer(t, otpAfterPublicKey("123456"))
			hop := createKeyFileAsset(t, ctx, host, port)
			target := plainTarget(hop)

			mfa := &hopMFA{answer: "123456"}
			require.NoError(t, dialAndClose(WithMFA(ctx, mfa.newCaller), target))
			_, challenged := mfa.snapshot()
			assert.Equal(t, []int64{hop}, challenged)
		})

		convey.Convey("口令认证的跳板", func() {
			host, port := newForwardingSSHServer(t, otpAfterPassword("123456"))
			hop := createPasswordHopAsset(t, ctx, host, port)
			target := plainTarget(hop)

			mfa := &hopMFA{answer: "123456"}
			require.NoError(t, dialAndClose(WithMFA(ctx, mfa.newCaller), target))
			_, challenged := mfa.snapshot()
			assert.Equal(t, []int64{hop}, challenged)
		})

		agentHop := func() int64 {
			priv, pub := testKeyPair(t)
			agentPath, _ := newDialTestAgent(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			src := &ssh_agent_source_entity.SSHAgentSource{Name: "work", EndpointType: "unix_socket", Endpoint: agentPath}
			require.NoError(t, ssh_agent_source_repo.SSHAgentSource().Create(ctx, src))
			host, port := newForwardingSSHServer(t, otpAfterPublicKey("654321"))
			return createAgentAsset(t, ctx, host, port, src.ID, sshagent.FingerprintSHA256(pub))
		}

		convey.Convey("Agent 认证的跳板", func() {
			hop := agentHop()
			target := plainTarget(hop)

			mfa := &hopMFA{answer: "654321"}
			require.NoError(t, dialAndClose(WithMFA(ctx, mfa.newCaller), target))
			_, challenged := mfa.snapshot()
			assert.Equal(t, []int64{hop}, challenged)
		})

		convey.Convey("Agent 认证的跳板拒绝答案时报 MFA 验证失败", func() {
			target := plainTarget(agentHop())

			mfa := &hopMFA{answer: "000000"}
			err := dialAndClose(WithMFA(ctx, mfa.newCaller), target)
			assert.Equal(t, sshagent.CodeMFAFailed, errCode(err), "err: %v", err)
		})

		convey.Convey("跳板与目标都要 MFA 时按拨号顺序逐台应答", func() {
			hopHost, hopPort := newForwardingSSHServer(t, otpAfterPublicKey("123456"))
			hop := createKeyFileAsset(t, ctx, hopHost, hopPort)
			host, port := newDialTestSSHServer(t, otpAfterPublicKey("123456")).hostPort()
			target := createHopAssetAt(t, ctx, host, port, hop, "")

			mfa := &hopMFA{answer: "123456"}
			require.NoError(t, dialAndClose(WithMFA(ctx, mfa.newCaller), target))
			_, challenged := mfa.snapshot()
			assert.Equal(t, []int64{hop, target}, challenged)
		})

		convey.Convey("跳板拒绝答案时报 MFA 验证失败，不拿同一个码重试", func() {
			host, port := newForwardingSSHServer(t, otpAfterPublicKey("123456"))
			hop := createKeyFileAsset(t, ctx, host, port)
			target := plainTarget(hop)

			mfa := &hopMFA{answer: "000000"}
			err := dialAndClose(WithMFA(ctx, mfa.newCaller), target)
			assert.Equal(t, sshagent.CodeMFAFailed, errCode(err), "err: %v", err)
			_, challenged := mfa.snapshot()
			assert.Equal(t, []int64{hop}, challenged)
		})

		convey.Convey("ctx 不带应答方（桌面端路径）时与以前一样报需要 MFA", func() {
			host, port := newForwardingSSHServer(t, otpAfterPublicKey("123456"))
			hop := createKeyFileAsset(t, ctx, host, port)
			target := plainTarget(hop)

			err := dialAndClose(ctx, target)
			assert.Equal(t, sshagent.CodeMFARequired, errCode(err), "err: %v", err)
		})

		convey.Convey("解析一次、拨号多次（扩展的 HTTP 连接）时每次握手各取一个应答方", func() {
			host, port := newForwardingSSHServer(t, otpAfterPublicKey("123456"))
			hop := createKeyFileAsset(t, ctx, host, port)
			echo, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = echo.Close() })

			mfa := &hopMFA{answer: "123456"}
			layers, err := Default().ResolveProxyChain(WithMFA(ctx, mfa.newCaller), sshChain(hop), 5)
			require.NoError(t, err)
			for range 2 {
				conn, err := proxychain.Chain{Layers: layers}.Dial(context.Background(), echo.Addr().String())
				require.NoError(t, err)
				_ = conn.Close()
			}
			callers, challenged := mfa.snapshot()
			assert.Equal(t, []int64{hop, hop}, callers)
			assert.Equal(t, []int64{hop, hop}, challenged)
		})
	})
}

// shortenDialTimeout shrinks the SSH dial timeout — the per-step limit of every hop
// handshake — for the test.
func shortenDialTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previous := sshtuning.Get()
	t.Cleanup(func() { sshtuning.Set(previous) })
	tuned := previous
	tuned.DialTimeout = d
	sshtuning.Set(tuned)
}

func TestHopMFAAnswerTimeIsNotCountedAgainstTheHandshakeLimit(t *testing.T) {
	convey.Convey("等人回答跳板 MFA 的时间不计入跳板握手时限，取消仍结束等待", t, func() {
		ctx := setupAgentDialTest(t)
		shortenDialTimeout(t, 200*time.Millisecond)
		host, port := newForwardingSSHServer(t, otpAfterPublicKey("123456"))
		hop := createKeyFileAsset(t, ctx, host, port)
		targetHost, targetPort := newDialTestSSHServer(t, &ssh.ServerConfig{}).hostPort()
		target := createHopAssetAt(t, ctx, targetHost, targetPort, hop, "")

		dialWithin := func(dialCtx context.Context, d time.Duration) error {
			done := make(chan error, 1)
			go func() { done <- dialAndClose(dialCtx, target) }()
			select {
			case err := <-done:
				return err
			case <-time.After(d):
				t.Fatalf("拨号在 %s 内没有返回", d)
				return nil
			}
		}

		convey.Convey("答案晚于时限到达仍能建连", func() {
			slow := func(int64) sshagent.InteractiveCaller {
				return mfaFunc(func(ctx context.Context, _ sshagent.MFAChallenge) ([]string, error) {
					select {
					case <-time.After(600 * time.Millisecond):
						return []string{"123456"}, nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				})
			}
			assert.NoError(t, dialWithin(WithMFA(ctx, slow), 5*time.Second))
		})

		convey.Convey("等待作答时取消，等待与拨号随之结束", func() {
			dialCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			waiting := make(chan struct{})
			never := func(int64) sshagent.InteractiveCaller {
				return mfaFunc(func(ctx context.Context, _ sshagent.MFAChallenge) ([]string, error) {
					close(waiting)
					<-ctx.Done()
					return nil, ctx.Err()
				})
			}
			go func() {
				<-waiting
				time.Sleep(400 * time.Millisecond) // past the step limit
				cancel()
			}()
			assert.ErrorIs(t, dialWithin(WithMFA(dialCtx, never), 5*time.Second), context.Canceled)
		})
	})
}
