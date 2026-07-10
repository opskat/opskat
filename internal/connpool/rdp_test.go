package connpool

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type failingPoolDialer struct{ err error }

func (d failingPoolDialer) DialAsset(context.Context, int64) (*ssh.Client, []io.Closer, error) {
	return nil, nil, d.err
}

func TestRDPDialContextDirectReturnsNil(t *testing.T) {
	dial := RDPDialContext(0, &asset_entity.RDPConfig{Host: "h", Port: 3389}, nil)

	assert.Nil(t, dial)
}

func TestRDPDialContextProxyRoutesViaSocks(t *testing.T) {
	echoAddr := socksdialtest.StartEcho(t)
	proxyAddr := socksdialtest.Start(t, "", "")
	host, portStr, err := net.SplitHostPort(proxyAddr)
	require.NoError(t, err)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	cfg := &asset_entity.RDPConfig{
		Host:  "ignored",
		Port:  1,
		Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: host, Port: port},
	}
	dial := RDPDialContext(0, cfg, nil)
	require.NotNil(t, dial)

	conn, err := dial(context.Background(), "tcp", echoAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	msg := []byte("ping")
	_, err = conn.Write(msg)
	require.NoError(t, err)
	buf := make([]byte, len(msg))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, msg, buf)
}

func TestRDPDialContextTunnelPreferredOverProxy(t *testing.T) {
	sentinel := errors.New("ssh dial refused")
	pool := sshpool.NewPool(failingPoolDialer{err: sentinel}, time.Minute)
	defer pool.Close()

	cfg := &asset_entity.RDPConfig{
		Host:  "target",
		Port:  3389,
		Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "127.0.0.1", Port: 1},
	}
	dial := RDPDialContext(7, cfg, pool)
	require.NotNil(t, dial)

	_, err := dial(context.Background(), "tcp", "target:3389")
	// 走 SSH 池而非 SOCKS 代理：错误来自池的 dialer
	require.ErrorContains(t, err, "ssh dial refused")
}
