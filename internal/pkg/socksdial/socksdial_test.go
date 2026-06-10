package socksdial_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEchoServer 启动一个回显 TCP 服务,返回监听地址
func startEchoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// startSocks5Server 启动一个极简 SOCKS5 服务(CONNECT)。
// user 为空表示 no-auth,否则要求 RFC 1929 用户名/密码认证。
func startSocks5Server(t *testing.T, user, pass string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSocks5(conn, user, pass)
		}
	}()
	return ln.Addr().String()
}

func handleSocks5(conn net.Conn, user, pass string) {
	defer func() { _ = conn.Close() }()
	// 协商: VER NMETHODS METHODS...
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 5 {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if user != "" {
		// 要求用户名/密码认证
		if _, err := conn.Write([]byte{5, 2}); err != nil {
			return
		}
		// RFC 1929: VER ULEN UNAME PLEN PASSWD
		authHead := make([]byte, 2)
		if _, err := io.ReadFull(conn, authHead); err != nil || authHead[0] != 1 {
			return
		}
		uname := make([]byte, authHead[1])
		if _, err := io.ReadFull(conn, uname); err != nil {
			return
		}
		plen := make([]byte, 1)
		if _, err := io.ReadFull(conn, plen); err != nil {
			return
		}
		passwd := make([]byte, plen[0])
		if _, err := io.ReadFull(conn, passwd); err != nil {
			return
		}
		if string(uname) != user || string(passwd) != pass {
			_, _ = conn.Write([]byte{1, 1}) // 认证失败
			return
		}
		if _, err := conn.Write([]byte{1, 0}); err != nil {
			return
		}
	} else {
		if _, err := conn.Write([]byte{5, 0}); err != nil {
			return
		}
	}
	// 请求: VER CMD RSV ATYP DST.ADDR DST.PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil || req[0] != 5 || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3: // 域名
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", binary.BigEndian.Uint16(portBuf))))
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = target.Close() }()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(target, conn) }()
	_, _ = io.Copy(conn, target)
}

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
	echoAddr := startEchoServer(t)
	proxyHost, proxyPort := splitAddr(t, startSocks5Server(t, "", ""))

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
	echoAddr := startEchoServer(t)
	proxyHost, proxyPort := splitAddr(t, startSocks5Server(t, "", ""))

	conn, err := socksdial.Dial(context.Background(), &asset_entity.ProxyConfig{
		Host: proxyHost,
		Port: proxyPort,
	}, echoAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	roundTrip(t, conn)
}

func TestDialUserPass(t *testing.T) {
	echoAddr := startEchoServer(t)
	proxyHost, proxyPort := splitAddr(t, startSocks5Server(t, "alice", "secret"))

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
	echoAddr := startEchoServer(t)
	proxyHost, proxyPort := splitAddr(t, startSocks5Server(t, "alice", "secret"))

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
	echoAddr := startEchoServer(t)
	proxyHost, proxyPort := splitAddr(t, startSocks5Server(t, "", ""))

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
