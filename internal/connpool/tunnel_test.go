package connpool

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/sshpool"
)

// startSSHServer 起一个免认证的进程内 SSH 服务器，每条连接的通道请求交给 serve 处理，返回监听地址。
func startSSHServer(t *testing.T, serve func(chans <-chan ssh.NewChannel)) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer func() { _ = sc.Close() }()
				go ssh.DiscardRequests(reqs)
				serve(chans)
			}()
		}
	}()
	return ln.Addr().String()
}

// directTCPIPTarget 解出 direct-tcpip 通道请求要转发到的 host:port。
func directTCPIPTarget(nc ssh.NewChannel) (string, error) {
	var d struct {
		DestAddr string
		DestPort uint32
		SrcAddr  string
		SrcPort  uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &d); err != nil {
		return "", err
	}
	return net.JoinHostPort(d.DestAddr, strconv.Itoa(int(d.DestPort))), nil
}

// forwardPolicyServer 模拟 sshd 的转发策略：策略在连接建立时定下，之后改策略只影响新连接
// （sshd reload 不动已有连接）。放行的通道原样回显数据。
type forwardPolicyServer struct {
	addr   string
	permit atomic.Pointer[func(target string) bool]
	logins atomic.Int32
}

func (s *forwardPolicyServer) setPermit(permit func(target string) bool) {
	s.permit.Store(&permit)
}

func startForwardPolicyServer(t *testing.T, permit func(target string) bool) *forwardPolicyServer {
	t.Helper()
	s := &forwardPolicyServer{}
	s.setPermit(permit)
	s.addr = startSSHServer(t, func(chans <-chan ssh.NewChannel) {
		s.logins.Add(1)
		permit := *s.permit.Load()
		for nc := range chans {
			target, err := directTCPIPTarget(nc)
			if err != nil {
				_ = nc.Reject(ssh.ConnectionFailed, "bad payload")
				continue
			}
			if !permit(target) {
				_ = nc.Reject(ssh.Prohibited, "open failed")
				continue
			}
			ch, reqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go ssh.DiscardRequests(reqs)
			go func() {
				_, _ = io.Copy(ch, ch)
				_ = ch.Close()
			}()
		}
	})
	return s
}

func (s *forwardPolicyServer) pool(t *testing.T, opts ...sshpool.Option) *sshpool.Pool {
	t.Helper()
	pool := sshpool.NewPool(forwardDialer{addr: s.addr}, time.Minute, opts...)
	t.Cleanup(pool.Close)
	return pool
}

func forbidAll(string) bool { return false }
func permitAll(string) bool { return true }

func requireEcho(t *testing.T, conn net.Conn) {
	t.Helper()
	_, err := conn.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, "ping", string(buf))
}

func requireProhibited(t *testing.T, err error) {
	t.Helper()
	var openErr *ssh.OpenChannelError
	require.ErrorAs(t, err, &openErr)
	require.Equal(t, ssh.Prohibited, openErr.Reason)
}

func TestSSHTunnelForwardingPolicy(t *testing.T) {
	t.Run("picks up a policy the server relaxed after the pooled connection was made", func(t *testing.T) {
		srv := startForwardPolicyServer(t, forbidAll)
		tunnel := NewSSHTunnel(9, "127.0.0.1", 3306, srv.pool(t, sshpool.WithRefusedRedialInterval(0)))

		_, err := tunnel.Dial(context.Background())
		requireProhibited(t, err)

		srv.setPermit(permitAll)

		conn, err := tunnel.Dial(context.Background())
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		requireEcho(t, conn)
	})
	t.Run("does not log in again on every refusal while the server still forbids forwarding", func(t *testing.T) {
		srv := startForwardPolicyServer(t, forbidAll)
		tunnel := NewSSHTunnel(9, "127.0.0.1", 3306, srv.pool(t, sshpool.WithRefusedRedialInterval(time.Hour)))

		for range 5 {
			_, err := tunnel.Dial(context.Background())
			requireProhibited(t, err)
		}

		assert.EqualValues(t, 1, srv.logins.Load())
	})

	t.Run("keeps a connection that another tunnel is still using", func(t *testing.T) {
		const mysql, mongo = "127.0.0.1:3306", "127.0.0.1:27017"
		srv := startForwardPolicyServer(t, func(target string) bool { return target == mysql })
		tunnel := NewSSHTunnel(9, "", 0, srv.pool(t, sshpool.WithRefusedRedialInterval(0)))

		inUse, err := tunnel.DialAddr(context.Background(), mysql)
		require.NoError(t, err)
		t.Cleanup(func() { _ = inUse.Close() })

		_, err = tunnel.DialAddr(context.Background(), mongo)
		requireProhibited(t, err)

		requireEcho(t, inUse)
		assert.EqualValues(t, 1, srv.logins.Load())
	})
	t.Run("leaves the replacement alone when the refused connection already left the pool", func(t *testing.T) {
		srv := startForwardPolicyServer(t, forbidAll)
		pool := srv.pool(t, sshpool.WithRefusedRedialInterval(0))

		refused, err := pool.Get(context.Background(), 9)
		require.NoError(t, err)
		pool.Remove(9)
		replacement, err := pool.Get(context.Background(), 9)
		require.NoError(t, err)

		assert.True(t, pool.ReleaseRefused(9, refused), "a fresh Get no longer returns the refused connection")

		entries := pool.List()
		require.Len(t, entries, 1)
		assert.Equal(t, 1, entries[0].RefCount)
		_, _, err = replacement.SendRequest("keepalive@openssh.com", true, nil)
		assert.NoError(t, err)
	})
}
