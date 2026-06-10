package connpool

import (
	"context"
	"crypto/tls"
	"net"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial"
)

// dialContextFunc 按目标地址建立底层 TCP 连接。
// 隧道实现忽略 addr(目标在建隧道时已确定),代理实现按 addr 拨号。
type dialContextFunc func(ctx context.Context, addr string) (net.Conn, error)

// tunnelDialFunc 把 SSH 隧道包装为 dialContextFunc。
func tunnelDialFunc(t *SSHTunnel) dialContextFunc {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return t.Dial(ctx)
	}
}

// proxyDialFunc 把 SOCKS5 代理配置包装为 dialContextFunc。
// p.Password 必须为明文,由调用方负责解密。
func proxyDialFunc(p *asset_entity.ProxyConfig) dialContextFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		return socksdial.Dial(ctx, p, addr)
	}
}

// wrapTLSClient 在自定义底层连接上手动完成 TLS 握手。
// 驱动设置自定义 dialer 后自带的 TLS 选项被绕过或互斥(go-redis 绕过 TLSConfig、
// franz-go 禁止 Dialer 与 DialTLSConfig 共存),需在 dialer 内手动包裹。
// ServerName 为空时默认取目标 host,保证经隧道/代理远端解析时 SNI 仍正确。
func wrapTLSClient(ctx context.Context, conn net.Conn, tlsConfig *tls.Config, host string) (net.Conn, error) {
	cfgClone := tlsConfig.Clone()
	if cfgClone.ServerName == "" {
		cfgClone.ServerName = host
	}
	tlsConn := tls.Client(conn, cfgClone)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// tlsWrappedDialFunc 把 dial 与可选的 TLS 包裹组合为驱动可用的三参 dialer。
// tlsConfig 为 nil 时仅做底层拨号。
func tlsWrappedDialFunc(dial dialContextFunc, tlsConfig *tls.Config) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, addr)
		if err != nil {
			return nil, err
		}
		if tlsConfig == nil {
			return conn, nil
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		return wrapTLSClient(ctx, conn, tlsConfig, host)
	}
}
