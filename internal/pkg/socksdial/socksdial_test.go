package socksdial_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundTrip 经 conn 写入并读回数据,断言回显一致
func roundTrip(t *testing.T, conn net.Conn) {
	t.Helper()
	msg := []byte("hello via socks5")
	_, err := conn.Write(msg)
	require.NoError(t, err)
	buf := make([]byte, len(msg))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, msg, buf)
}

func TestDialNoAuth(t *testing.T) {
	echoAddr := socksdialtest.StartEcho(t)
	proxyHost, proxyPort := splitAddr(t, socksdialtest.Start(t, "", ""))

	conn, err := socksdial.Dial(context.Background(), &asset_entity.ProxyConfig{
		Type: "socks5",
		Host: proxyHost,
		Port: proxyPort,
	}, echoAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	roundTrip(t, conn)
}

func TestDialEmptyTypeDefaultsToSocks5(t *testing.T) {
	echoAddr := socksdialtest.StartEcho(t)
	proxyHost, proxyPort := splitAddr(t, socksdialtest.Start(t, "", ""))

	conn, err := socksdial.Dial(context.Background(), &asset_entity.ProxyConfig{
		Host: proxyHost,
		Port: proxyPort,
	}, echoAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	roundTrip(t, conn)
}

func TestDialUserPass(t *testing.T) {
	echoAddr := socksdialtest.StartEcho(t)
	proxyHost, proxyPort := splitAddr(t, socksdialtest.Start(t, "alice", "secret"))

	conn, err := socksdial.Dial(context.Background(), &asset_entity.ProxyConfig{
		Type:     "socks5",
		Host:     proxyHost,
		Port:     proxyPort,
		Username: "alice",
		Password: "secret",
	}, echoAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	roundTrip(t, conn)
}

func TestDialWrongPassword(t *testing.T) {
	echoAddr := socksdialtest.StartEcho(t)
	proxyHost, proxyPort := splitAddr(t, socksdialtest.Start(t, "alice", "secret"))

	_, err := socksdial.Dial(context.Background(), &asset_entity.ProxyConfig{
		Type:     "socks5",
		Host:     proxyHost,
		Port:     proxyPort,
		Username: "alice",
		Password: "wrong",
	}, echoAddr)
	require.Error(t, err)
}

func TestDialUnsupportedType(t *testing.T) {
	_, err := socksdial.Dial(context.Background(), &asset_entity.ProxyConfig{
		Type: "http",
		Host: "127.0.0.1",
		Port: 1080,
	}, "127.0.0.1:3306")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "不支持的代理类型")
}

func TestDialContextCanceled(t *testing.T) {
	echoAddr := socksdialtest.StartEcho(t)
	proxyHost, proxyPort := splitAddr(t, socksdialtest.Start(t, "", ""))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := socksdial.Dial(ctx, &asset_entity.ProxyConfig{
		Type: "socks5",
		Host: proxyHost,
		Port: proxyPort,
	}, echoAddr)
	require.Error(t, err)
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)
	return host, port
}
