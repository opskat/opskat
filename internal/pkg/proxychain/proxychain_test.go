package proxychain

import (
	"context"
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
