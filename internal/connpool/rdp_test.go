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
	dial, err := RDPDialContext(context.Background(), 0, &asset_entity.RDPConfig{Host: "h", Port: 3389}, nil)

	require.NoError(t, err)
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
	dial, err := RDPDialContext(context.Background(), 0, cfg, nil)
	require.NoError(t, err)
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
	dial, err := RDPDialContext(context.Background(), 7, cfg, pool)
	require.NoError(t, err)
	require.NotNil(t, dial)

	_, err = dial(context.Background(), "tcp", "target:3389")
	// 走 SSH 池而非 SOCKS 代理：错误来自池的 dialer
	require.ErrorContains(t, err, "ssh dial refused")
}

func TestRDPDialContextProxyChainRoutesViaSocks(t *testing.T) {
	echoAddr := socksdialtest.StartEcho(t)
	proxyAddr := socksdialtest.Start(t, "", "")
	host, portStr, err := net.SplitHostPort(proxyAddr)
	require.NoError(t, err)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	enabled := true
	cfg := &asset_entity.RDPConfig{ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{{
		Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: &enabled, Host: host, Port: port,
	}}}}
	dial, err := RDPDialContext(context.Background(), 0, cfg, nil)
	require.NoError(t, err)
	conn, err := dial(context.Background(), "tcp", echoAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte("chain"))
	require.NoError(t, err)
	buf := make([]byte, 5)
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, "chain", string(buf))
}
