package proxychain

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type deadlineErrorConn struct {
	net.Conn
}

func TestChainDialsThroughTwoRealSOCKS5Layers(t *testing.T) {
	target := socksdialtest.StartEcho(t)
	first := socksdialtest.Start(t, "", "")
	second := socksdialtest.Start(t, "chain-user", "chain-pass")
	layer := func(addr, user, pass string) Layer {
		host, portText, err := net.SplitHostPort(addr)
		require.NoError(t, err)
		port, err := strconv.Atoi(portText)
		require.NoError(t, err)
		return Layer{Type: LayerSOCKS5, Host: host, Port: port, Username: user, Password: pass}
	}

	conn, err := (Chain{Layers: []Layer{
		layer(first, "", ""),
		layer(second, "chain-user", "chain-pass"),
	}}).Dial(context.Background(), target)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	message := []byte("through-two-live-proxies")
	_, err = conn.Write(message)
	require.NoError(t, err)
	got := make([]byte, len(message))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	require.Equal(t, message, got)
}

func (c deadlineErrorConn) SetDeadline(time.Time) error {
	return errors.New("ssh: tcpChan: deadline not supported")
}
func (c deadlineErrorConn) SetReadDeadline(time.Time) error {
	return errors.New("ssh: tcpChan: deadline not supported")
}
func (c deadlineErrorConn) SetWriteDeadline(time.Time) error {
	return errors.New("ssh: tcpChan: deadline not supported")
}

func TestDeadlineIgnoredConn(t *testing.T) {
	left, right := net.Pipe()
	defer func() {
		_ = left.Close()
	}()
	defer func() {
		_ = right.Close()
	}()

	conn := deadlineIgnoredConn{Conn: deadlineErrorConn{Conn: left}}
	if err := conn.SetDeadline(time.Now()); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}
	if err := conn.SetReadDeadline(time.Now()); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	if err := conn.SetWriteDeadline(time.Now()); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
}

// TestSSHLayerHandshakeOverride: SSH 层的 Handshake 覆盖标准 ssh.NewClientConn，
// 供 Agent 认证等需要持有传输走完整握手的场景复用同一拨号管线。
func TestSSHLayerHandshakeOverride(t *testing.T) {
	// 真实的 TCP 端点：dialSSHLayer 先连到该层，再调用 Handshake 接管握手。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	host, portText, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	var called bool
	layer := Layer{
		Type: LayerSSH, Host: host, Port: port,
		Handshake: func(_ context.Context, conn net.Conn, addr string) (*ssh.Client, error) {
			called = true
			_ = conn
			_ = addr
			return nil, errors.New("handshake-invoked")
		},
	}
	_, err = (Chain{Layers: []Layer{layer}}).Dial(context.Background(), "target:1")
	require.True(t, called, "Handshake 应被调用而非走标准 NewClientConn")
	require.Error(t, err)
	require.Contains(t, err.Error(), "handshake-invoked")
}

func layerAt(t *testing.T, layerType, addr string) Layer {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return Layer{Type: layerType, Host: host, Port: port}
}

// plainHandshake is an SSH layer handshake with no deadline of its own, like
// ssh.NewClientConn: only the layer's EstablishTimeout bounds it.
func plainHandshake(cfg *ssh.ClientConfig) func(context.Context, net.Conn, string) (*ssh.Client, error) {
	return func(_ context.Context, conn net.Conn, addr string) (*ssh.Client, error) {
		c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
		if err != nil {
			return nil, err
		}
		return ssh.NewClient(c, chans, reqs), nil
	}
}

// dialResult 在后台拨号，超过 limit 仍未返回即判定为卡死。
func dialResult(t *testing.T, chain Chain, target string, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		conn, err := chain.Dial(context.Background(), target)
		if conn != nil {
			_ = conn.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("Dial 在 %s 内没有返回", limit)
		return nil
	}
}

// silentListener 接受连接但从不应答，返回一个在对端关闭连接后关闭的通道。
func silentListener(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	peerClosed := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(io.Discard, conn)
		close(peerClosed)
	}()
	return ln.Addr().String(), peerClosed
}

func requireClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s", what)
	}
}

func TestSSHLayerHandshakeStallIsBounded(t *testing.T) {
	addr, peerClosed := silentListener(t)
	layer := layerAt(t, LayerSSH, addr)
	layer.Handshake = plainHandshake(&ssh.ClientConfig{User: "root", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	layer.EstablishTimeout = 150 * time.Millisecond

	err := dialResult(t, Chain{Layers: []Layer{layer}}, "10.0.0.1:9200", 3*time.Second)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), addr, "报错要指出卡在哪一跳")
	requireClosed(t, peerClosed, "握手超时后到这一跳的连接应被关闭")
}

func TestSSHLayerChannelOpenStallIsBounded(t *testing.T) {
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(hostKey)
	require.NoError(t, err)
	serverCfg := &ssh.ServerConfig{NoClientAuth: true}
	serverCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	clientGone := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		sshConn, chans, reqs, err := ssh.NewServerConn(conn, serverCfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		// 收下 direct-tcpip 请求但既不接受也不拒绝：跳板机连下一地址迟迟没有结果。
		var pending []ssh.NewChannel
		go func() {
			for ch := range chans {
				pending = append(pending, ch)
			}
		}()
		_ = sshConn.Wait()
		close(clientGone)
	}()

	layer := layerAt(t, LayerSSH, ln.Addr().String())
	layer.Handshake = plainHandshake(&ssh.ClientConfig{User: "root", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	// 同一个时限也管握手这一步，而这里握手要真的走完，留足余量免得负载高时先在握手上超时。
	layer.EstablishTimeout = time.Second

	err = dialResult(t, Chain{Layers: []Layer{layer}}, "10.0.0.1:9200", 10*time.Second)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "10.0.0.1:9200", "报错要指出没连上的下一地址")
	requireClosed(t, clientGone, "通道打不开时这一跳的 SSH 连接应被关闭")
}

func TestSOCKS5LayerHandshakeStallIsBounded(t *testing.T) {
	addr, peerClosed := silentListener(t)
	layer := layerAt(t, LayerSOCKS5, addr)
	layer.EstablishTimeout = 150 * time.Millisecond

	err := dialResult(t, Chain{Layers: []Layer{layer}}, "10.0.0.1:9200", 3*time.Second)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), addr, "报错要指出卡在哪一跳")
	requireClosed(t, peerClosed, "握手超时后到这一跳的连接应被关闭")
}
