// Package socksdial 提供基于 SOCKS5 代理的 TCP 拨号,供 SSH 与数据库连接共用。
package socksdial

import (
	"context"
	"fmt"
	"net"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	"golang.org/x/net/proxy"
)

// Dial 通过 SOCKS5 代理建立到 targetAddr 的 TCP 连接。
// proxyCfg.Password 必须为明文,调用方负责解密(credential_resolver.DecryptProxyPassword)。
func Dial(ctx context.Context, proxyCfg *asset_entity.ProxyConfig, targetAddr string) (net.Conn, error) {
	if proxyCfg.Type != "" && proxyCfg.Type != "socks5" {
		return nil, fmt.Errorf("不支持的代理类型: %s", proxyCfg.Type)
	}

	proxyAddr := fmt.Sprintf("%s:%d", proxyCfg.Host, proxyCfg.Port)
	var auth *proxy.Auth
	if proxyCfg.Username != "" {
		auth = &proxy.Auth{
			User:     proxyCfg.Username,
			Password: proxyCfg.Password,
		}
	}
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, auth, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("创建SOCKS代理失败: %w", err)
	}
	var conn net.Conn
	if cd, ok := dialer.(proxy.ContextDialer); ok {
		conn, err = cd.DialContext(ctx, "tcp", targetAddr)
	} else {
		conn, err = dialer.Dial("tcp", targetAddr)
	}
	if err != nil {
		return nil, fmt.Errorf("通过SOCKS代理连接失败: %w", err)
	}
	return conn, nil
}
