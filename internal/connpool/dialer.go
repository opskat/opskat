package connpool

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/internal/pkg/proxychain"
	"github.com/opskat/opskat/internal/pkg/socksdial"
	"github.com/opskat/opskat/internal/service/credential_resolver"
)

// dialContextFunc 按目标地址建立底层 TCP 连接。
// tunnelDialFunc 忽略 addr(目标在建隧道时已确定),其余实现按 addr 拨号。
type dialContextFunc func(ctx context.Context, addr string) (net.Conn, error)

// networkDialFunc 是驱动要求的三参拨号签名(按 network 区分 tcp / unix / udp)。
type networkDialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// DialContext 使 networkDialFunc 满足 mssql 驱动的 Dialer 接口。
func (f networkDialFunc) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return f(ctx, network, addr)
}

// ignoreNetwork 把只按地址拨号的 dialContextFunc 适配为三参签名;隧道/代理只承载 TCP,忽略 network。
func (f dialContextFunc) ignoreNetwork() networkDialFunc {
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		return f(ctx, addr)
	}
}

// localAwareDial 按拨号时的实际地址分流:.local 主机交给 local(统一拨号器,绕过系统解析器的 mDNS),
// 其余地址原样交给 fallback(驱动默认拨号),保证非 .local 行为与驱动默认一致。
// 按实际地址而非配置判断,才能覆盖集群宣告、advertised listener 等自动发现的 .local 节点。
func localAwareDial(local, fallback networkDialFunc) networkDialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if netdial.IsLocalAddr(addr) {
			return local(ctx, network, addr)
		}
		return fallback(ctx, network, addr)
	}
}

// directTLSDialer 经统一拨号器直连并按需包裹 TLS(tlsConfig 为 nil 时不做 TLS)。timeout 覆盖
// TCP 拨号与 TLS 握手,与 kgo / go-redis 默认 dialer(tls.Dialer + net.Dialer{Timeout})语义一致。
func directTLSDialer(tlsConfig *tls.Config, timeout time.Duration) networkDialFunc {
	d := &netdial.Dialer{}
	dial := tlsWrappedDialFunc(func(ctx context.Context, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", addr)
	}, tlsConfig)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return dial(ctx, network, addr)
	}
}

// tunnelDialFunc 把 SSH 隧道包装为固定目标的 dialContextFunc。
func tunnelDialFunc(t *SSHTunnel) dialContextFunc {
	return func(ctx context.Context, _ string) (net.Conn, error) {
		return t.Dial(ctx)
	}
}

// tunnelAddrDialFunc 把 SSH 隧道包装为按请求地址转发的 dialContextFunc。
func tunnelAddrDialFunc(t *SSHTunnel) dialContextFunc {
	return t.DialAddr
}

// directDialFunc 直连目标地址,超时与保活对齐 go-redis 默认 dialer。
func directDialFunc() dialContextFunc {
	d := &netdial.Dialer{Dialer: net.Dialer{Timeout: 5 * time.Second, KeepAlive: 5 * time.Minute}}
	return func(ctx context.Context, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp", addr)
	}
}

// mappedDialFunc 拨号前把命中映射表左侧的地址改写为右侧的实际地址。
func mappedDialFunc(dial dialContextFunc, addrMap map[string]string) dialContextFunc {
	if len(addrMap) == 0 {
		return dial
	}
	return func(ctx context.Context, addr string) (net.Conn, error) {
		if actual, ok := addrMap[addr]; ok {
			addr = actual
		}
		return dial(ctx, addr)
	}
}

// proxyDialFunc 把 SOCKS5 代理配置包装为 dialContextFunc。
// p.Password 必须为明文,由调用方负责解密。
func proxyDialFunc(p *asset_entity.ProxyConfig) dialContextFunc {
	return func(ctx context.Context, addr string) (net.Conn, error) {
		return socksdial.Dial(ctx, p, addr)
	}
}

func chainDialFunc(ctx context.Context, chain *asset_entity.ProxyChainConfig) (dialContextFunc, error) {
	layers, err := credential_resolver.Default().ResolveProxyChain(ctx, chain, 5)
	if err != nil {
		return nil, err
	}
	if len(layers) == 0 {
		return nil, nil
	}
	return func(ctx context.Context, addr string) (net.Conn, error) {
		return proxychain.Chain{Layers: layers}.Dial(ctx, addr)
	}, nil
}

// ProxyChainDialContext resolves an asset proxy chain into a standard DialContext.
// A nil function means direct connection. Resolution failures must be returned to
// the caller instead of being downgraded to a direct connection.
func ProxyChainDialContext(ctx context.Context, chain *asset_entity.ProxyChainConfig) (func(context.Context, string, string) (net.Conn, error), error) {
	dial, err := chainDialFunc(ctx, chain)
	if err != nil || dial == nil {
		return nil, err
	}
	return func(ctx context.Context, _ string, addr string) (net.Conn, error) {
		return dial(ctx, addr)
	}, nil
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
