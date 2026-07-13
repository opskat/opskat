package connpool

import (
	"context"
	"net"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
)

// RDPDialContext 返回 RDP 客户端的自定义拨号函数(隧道 > 代理 > 直连,与其它资产类型一致)。
// 直连返回 nil,调用方走库默认拨号。cfg.Proxy.Password 为明文,由调用方负责解密。
// RDP 的 TLS 在协议内自行协商,这里不做 TLS 包裹;每条隧道连接独立持有 SSH 池引用,
// 随连接关闭自动释放,无需额外 Closer。
func RDPDialContext(ctx context.Context, tunnelID int64, cfg *asset_entity.RDPConfig, sshPool *sshpool.Pool) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	if cfg.ProxyChain != nil {
		dial, err := ProxyChainDialContext(ctx, cfg.ProxyChain)
		if err != nil {
			return nil, err
		}
		if dial != nil {
			return dial, nil
		}
	}
	var dial dialContextFunc
	switch {
	case tunnelID > 0 && sshPool != nil:
		dial = tunnelDialFunc(NewSSHTunnel(tunnelID, cfg.Host, cfg.Port, sshPool))
	case cfg.Proxy != nil:
		dial = proxyDialFunc(cfg.Proxy)
	default:
		return nil, nil
	}
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		return dial(ctx, addr)
	}, nil
}
