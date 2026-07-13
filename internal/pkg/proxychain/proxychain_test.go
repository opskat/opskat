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
