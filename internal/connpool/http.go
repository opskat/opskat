package connpool

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/netdial"
	"github.com/opskat/opskat/internal/sshpool"
)

// HTTPConnConfig 描述一个 HTTP 客户端的网络路径与 TLS 设置（通用资产的 HTTP 执行方式与
// 它的「测试连接」共用）。
type HTTPConnConfig struct {
	// TunnelID 非 0 时经该 SSH 资产转发，拨号目标取请求 URL 的 host:port（在 SSH 远端解析）。
	TunnelID int64
	// ProxyChain 解析出至少一层时优先于 TunnelID（与 Kafka / etcd 的 代理链 > 隧道 顺序一致）。
	ProxyChain *asset_entity.ProxyChainConfig
	TLS        TLSFields
}

// GenericHTTPConn 取通用资产的连接配置：隧道在 Asset.SSHTunnelID，代理链与 TLS 在 GenericConfig。
func GenericHTTPConn(asset *asset_entity.Asset, cfg *asset_entity.GenericConfig) HTTPConnConfig {
	return HTTPConnConfig{
		TunnelID:   asset.SSHTunnelID,
		ProxyChain: cfg.ProxyChain,
		TLS: TLSFields{
			ServerName: cfg.TLSServerName,
			Insecure:   cfg.TLSInsecure,
			CAFile:     cfg.TLSCAFile,
			CertFile:   cfg.TLSCertFile,
			KeyFile:    cfg.TLSKeyFile,
		},
	}
}

// HTTPRoute 是 NewHTTPTransport 实际选用的网络路径，供日志与「测试连接」的结果展示。
type HTTPRoute string

const (
	HTTPRouteDirect     HTTPRoute = "direct"
	HTTPRouteSSHTunnel  HTTPRoute = "ssh_tunnel"
	HTTPRouteProxyChain HTTPRoute = "proxy_chain"
)

// HTTPRouteFor 是 HTTP 网络路径的唯一判定：代理链（经 NormalizeProxyChain 去掉停用层后仍有层）
// > SSH 隧道 > 直连。NewHTTPTransport 据此拨号，help 与「测试连接」据此描述，三者不会各说各话。
func HTTPRouteFor(conn HTTPConnConfig) HTTPRoute {
	switch {
	case asset_entity.NormalizeProxyChain(conn.ProxyChain) != nil:
		return HTTPRouteProxyChain
	case conn.TunnelID > 0:
		return HTTPRouteSSHTunnel
	default:
		return HTTPRouteDirect
	}
}

// NewHTTPTransport 按 代理链 > SSH 隧道 > 直连 构建一个 *http.Transport，并返回选用的路径。
// TLS 握手由 transport 在拨出的连接上完成（ServerName 为空时取请求 URL 的 host），因此经
// 隧道 / 代理远端解析时 SNI 与证书校验仍针对目标主机。
//
// 失败一律原样返回：代理链解析失败、配置了隧道却没有 SSH 连接池、TLS 证书读取失败都不会
// 退回直连或跳过校验。调用方用完后应调用 CloseIdleConnections 释放连接（隧道连接关闭时
// 归还 SSH 池引用）。
func NewHTTPTransport(ctx context.Context, conn HTTPConnConfig, sshPool *sshpool.Pool) (*http.Transport, HTTPRoute, error) {
	tlsConfig, err := BuildTLSConfig("HTTP", conn.TLS)
	if err != nil {
		return nil, "", err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig

	var dial dialContextFunc
	route := HTTPRouteFor(conn)
	switch route {
	case HTTPRouteProxyChain:
		if dial, err = chainDialFunc(ctx, conn.ProxyChain); err != nil {
			return nil, "", fmt.Errorf("解析代理链失败: %w", err)
		}
	case HTTPRouteSSHTunnel:
		if sshPool == nil {
			return nil, "", fmt.Errorf("配置了 SSH 隧道（资产 %d）但 SSH 连接池不可用", conn.TunnelID)
		}
		dial = tunnelAddrDialFunc(&SSHTunnel{sshAssetID: conn.TunnelID, pool: sshPool})
	}
	if dial == nil {
		// 直连：保留 http.DefaultTransport 的环境变量代理，拨号换成统一拨号器（.local 走单播 DNS）。
		transport.DialContext = netdial.Default().DialContext
		return transport, route, nil
	}
	// 经隧道 / 代理链时目标已由该路径决定，环境变量代理不能再插进来。
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, _ string, addr string) (net.Conn, error) {
		return dial(ctx, addr)
	}
	return transport, route, nil
}
