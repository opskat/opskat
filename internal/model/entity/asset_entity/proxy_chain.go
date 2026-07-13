package asset_entity

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

func EffectiveProxyChain(chain *ProxyChainConfig, tunnelID int64, proxy *ProxyConfig) *ProxyChainConfig {
	if chain != nil && len(chain.Layers) > 0 {
		return NormalizeProxyChain(chain)
	}
	var layers []ProxyChainLayer
	if tunnelID > 0 {
		enabled := true
		layers = append(layers, ProxyChainLayer{
			ID:         fmt.Sprintf("legacy-ssh-%d", tunnelID),
			Name:       "SSH Tunnel",
			Enabled:    &enabled,
			Type:       ProxyChainLayerSSH,
			Order:      1,
			SSHAssetID: tunnelID,
		})
	}
	if proxy != nil && strings.TrimSpace(proxy.Host) != "" {
		enabled := true
		layers = append(layers, ProxyChainLayer{
			ID:       "legacy-socks5",
			Name:     "SOCKS5 Proxy",
			Enabled:  &enabled,
			Type:     ProxyChainLayerSOCKS5,
			Order:    len(layers) + 1,
			Host:     proxy.Host,
			Port:     proxy.Port,
			Username: proxy.Username,
			Password: proxy.Password,
		})
	}
	if len(layers) == 0 {
		return nil
	}
	return &ProxyChainConfig{Layers: layers}
}

func NormalizeProxyChain(chain *ProxyChainConfig) *ProxyChainConfig {
	if chain == nil || len(chain.Layers) == 0 {
		return nil
	}
	layers := append([]ProxyChainLayer(nil), chain.Layers...)
	sort.SliceStable(layers, func(i, j int) bool {
		if layers[i].Order == layers[j].Order {
			return i < j
		}
		return layers[i].Order < layers[j].Order
	})
	out := layers[:0]
	for i, layer := range layers {
		if layer.Enabled != nil && !*layer.Enabled {
			continue
		}
		if layer.Order <= 0 {
			layer.Order = i + 1
		}
		out = append(out, layer)
	}
	if len(out) == 0 {
		return nil
	}
	return &ProxyChainConfig{Layers: out}
}

func ValidateProxyChain(chain *ProxyChainConfig) error {
	chain = NormalizeProxyChain(chain)
	if chain == nil {
		return nil
	}
	for i, layer := range chain.Layers {
		switch layer.Type {
		case ProxyChainLayerSSH:
			if layer.SSHAssetID <= 0 {
				return fmt.Errorf("代理链第 %d 层 SSH 资产不能为空", i+1)
			}
		case ProxyChainLayerSOCKS5:
			if strings.TrimSpace(layer.Host) == "" {
				return fmt.Errorf("代理链第 %d 层 SOCKS5 主机不能为空", i+1)
			}
			if layer.Port <= 0 || layer.Port > 65535 {
				return fmt.Errorf("代理链第 %d 层 SOCKS5 端口无效", i+1)
			}
		case ProxyChainLayerHTTPTunnel:
			if i != 0 {
				return fmt.Errorf("HTTP 隧道必须是代理链第一层")
			}
			if strings.TrimSpace(layer.URL) == "" {
				return fmt.Errorf("代理链第 %d 层 HTTP 隧道 URL 不能为空", i+1)
			}
			u, err := url.Parse(strings.TrimSpace(layer.URL))
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("代理链第 %d 层 HTTP 隧道 URL 无效", i+1)
			}
			if strings.TrimSpace(layer.Token) == "" {
				return fmt.Errorf("代理链第 %d 层 HTTP 隧道 Token 不能为空", i+1)
			}
		default:
			return fmt.Errorf("代理链第 %d 层类型不支持: %s", i+1, layer.Type)
		}
	}
	return nil
}
