package connpool

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyDialFuncRoutesViaProxy(t *testing.T) {
	echoAddr := socksdialtest.StartEcho(t)
	proxyAddr := socksdialtest.Start(t, "", "")
	host, portStr, err := net.SplitHostPort(proxyAddr)
	require.NoError(t, err)
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	dial := proxyDialFunc(&asset_entity.ProxyConfig{Type: "socks5", Host: host, Port: port})
	conn, err := dial(context.Background(), echoAddr)
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

// startTLSEcho 启动一个自签名证书的 TLS 回显服务
func startTLSEcho(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
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

func TestWrapTLSClient(t *testing.T) {
	tlsAddr := startTLSEcho(t)
	raw, err := net.Dial("tcp", tlsAddr)
	require.NoError(t, err)

	host, _, err := net.SplitHostPort(tlsAddr)
	require.NoError(t, err)
	conn, err := wrapTLSClient(context.Background(), raw, &tls.Config{InsecureSkipVerify: true}, host)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	msg := []byte("tls ping")
	_, err = conn.Write(msg)
	require.NoError(t, err)
	buf := make([]byte, len(msg))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, msg, buf)
}

func TestLocalAwareDial(t *testing.T) {
	errLocal := errors.New("local")
	errFallback := errors.New("fallback")
	dial := localAwareDial(
		func(context.Context, string, string) (net.Conn, error) { return nil, errLocal },
		func(context.Context, string, string) (net.Conn, error) { return nil, errFallback },
	)

	t.Run(".local hosts go through the unified dialer", func(t *testing.T) {
		_, err := dial(context.Background(), "tcp", "node1.corp.local:7001")
		assert.ErrorIs(t, err, errLocal)
	})

	t.Run("other hosts keep the driver default dial", func(t *testing.T) {
		for _, addr := range []string{"10.0.0.1:7001", "node1.corp.example:7001"} {
			_, err := dial(context.Background(), "tcp", addr)
			assert.ErrorIs(t, err, errFallback, addr)
		}
	})
}

func TestDirectTLSDialer(t *testing.T) {
	t.Run("timeout covers the tls handshake like the drivers' default dialers", func(t *testing.T) {
		// 只接受 TCP、从不应答 TLS 握手的 broker:超时必须覆盖握手,否则拨号永久挂起。
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				t.Cleanup(func() { _ = c.Close() })
			}
		}()

		dial := directTLSDialer(&tls.Config{InsecureSkipVerify: true}, 300*time.Millisecond)
		start := time.Now()
		_, err = dial(context.Background(), "tcp", ln.Addr().String())
		require.Error(t, err)
		assert.Less(t, time.Since(start), 2*time.Second)
	})

	t.Run("plaintext dials the broker address", func(t *testing.T) {
		echo := socksdialtest.StartEcho(t)
		conn, err := directTLSDialer(nil, time.Second)(context.Background(), "tcp", echo)
		require.NoError(t, err)
		_ = conn.Close()
	})
}
