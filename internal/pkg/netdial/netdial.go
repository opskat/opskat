// Package netdial 是本机直连用户配置主机（资产、代理、跳板机）的统一拨号入口。
//
// 系统解析器（macOS mDNSResponder、Linux nss-mdns）把 .local 视为 mDNS 专用域：
// 内网用单播 DNS 提供的 xxx.local 会先等 mDNS 超时（macOS 约 5s）再转单播，甚至直接解析失败，
// 表现为数据库连接慢、Redis 集群拨号超时。Dialer 对 .local 主机名先用 Go 内置解析器
// 直接向上级 DNS 做单播查询；查不到再交回系统解析器，保留真正 Bonjour 主机的 mDNS 解析。
package netdial

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// unicastLookupTimeout 限制 .local 单播查询的耗时：公共 DNS 常直接丢弃 .local 查询，
// 不能让它吃掉整个拨号超时。
const unicastLookupTimeout = 2 * time.Second

// unicastResolver 不经过系统 getaddrinfo，按 /etc/hosts 与 resolv.conf（Windows 为网卡 DNS）
// 直接发单播 DNS 查询，不做 mDNS。
var unicastResolver = &net.Resolver{PreferGo: true}

// lookupNetworks 把拨号网络映射为 LookupIP 的地址族；不在表中的网络（如 unix）不做解析。
var lookupNetworks = map[string]string{"tcp": "ip", "tcp4": "ip4", "tcp6": "ip6"}

// Dialer 在 net.Dialer 之上替换 .local 主机名的解析策略，其余拨号参数（超时、保活等）原样生效。
// Timeout 与 net.Dialer 语义一致：包含域名解析耗时。
type Dialer struct {
	net.Dialer
}

// Default 返回与 http.DefaultTransport 拨号参数一致的 Dialer，用于替换库的默认拨号。
func Default() *Dialer {
	return &Dialer{Dialer: net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}}
}

// Dial 覆盖内嵌 net.Dialer 的 Dial，避免调用方绕过 DialContext 的解析策略。
func (d *Dialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

// DialContext 拨号 address。.local 主机名先经单播 DNS 解析后按地址逐个拨号，其余地址交给 net.Dialer。
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	ipNetwork, ok := lookupNetworks[network]
	if err != nil || !ok || !isLocalDomain(host) {
		return d.Dialer.DialContext(ctx, network, address)
	}
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}
	ips, err := lookupUnicast(ctx, ipNetwork, host)
	if err != nil {
		logger.Ctx(ctx).Debug("unicast lookup for .local host failed, falling back to system resolver",
			zap.String("host", host), zap.Error(err))
		return d.Dialer.DialContext(ctx, network, address)
	}
	var errs []error
	for _, ip := range ips {
		conn, err := d.Dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// Resolve 按与 DialContext 相同的策略解析 host，供要先检查解析结果、再拨号已检查地址的调用方使用
// （如扩展 HTTP 的内网地址拦截），同一主机名只解析一次：.local 先查单播 DNS，查不到再交系统解析器
// （d.Resolver，未设置时为 net.DefaultResolver）。network 为拨号网络（tcp / tcp4 / tcp6）。
func (d *Dialer) Resolve(ctx context.Context, network, host string) ([]net.IP, error) {
	ipNetwork, ok := lookupNetworks[network]
	if !ok {
		return nil, fmt.Errorf("netdial: cannot resolve host for network %q", network)
	}
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}
	if isLocalDomain(host) {
		ips, err := lookupUnicast(ctx, ipNetwork, host)
		if err == nil {
			return ips, nil
		}
		logger.Ctx(ctx).Debug("unicast lookup for .local host failed, falling back to system resolver",
			zap.String("host", host), zap.Error(err))
	}
	resolver := d.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return resolver.LookupIP(ctx, ipNetwork, host)
}

func lookupUnicast(ctx context.Context, network, host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(ctx, unicastLookupTimeout)
	defer cancel()
	return unicastResolver.LookupIP(ctx, network, host)
}

// IsLocalAddr 判断地址（主机名、host:port 或 scheme://host:port/...）的主机是否属于 .local。
// 供按配置决定是否替换驱动默认拨号的调用方使用。
func IsLocalAddr(addr string) bool {
	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err != nil {
			return false
		}
		return isLocalDomain(u.Hostname())
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return isLocalDomain(host)
	}
	return isLocalDomain(addr)
}

func isLocalDomain(host string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSuffix(host, ".")), ".local")
}
