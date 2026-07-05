package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func proxyChainEnabled(v bool) *bool {
	return &v
}

func TestEffectiveProxyChainCompatibility(t *testing.T) {
	t.Run("new proxy_chain takes precedence over legacy fields", func(t *testing.T) {
		chain := &ProxyChainConfig{Layers: []ProxyChainLayer{{
			ID:      "http-1",
			Name:    "HTTP 1",
			Type:    ProxyChainLayerHTTPTunnel,
			Order:   1,
			URL:     "https://dbx.example.com/dbx_tunnel.php",
			Token:   "enc-token",
			Enabled: proxyChainEnabled(true),
		}}}

		got := EffectiveProxyChain(chain, 9, &ProxyConfig{Type: "socks5", Host: "p", Port: 1080})

		require.NotNil(t, got)
		require.Len(t, got.Layers, 1)
		assert.Equal(t, ProxyChainLayerHTTPTunnel, got.Layers[0].Type)
	})

	t.Run("legacy ssh tunnel becomes one ssh layer", func(t *testing.T) {
		got := EffectiveProxyChain(nil, 7, nil)

		require.NotNil(t, got)
		require.Len(t, got.Layers, 1)
		assert.Equal(t, ProxyChainLayerSSH, got.Layers[0].Type)
		assert.EqualValues(t, 7, got.Layers[0].SSHAssetID)
	})

	t.Run("legacy socks5 proxy becomes one socks5 layer", func(t *testing.T) {
		got := EffectiveProxyChain(nil, 0, &ProxyConfig{Type: "socks5", Host: "p", Port: 1080, Username: "u", Password: "enc"})

		require.NotNil(t, got)
		require.Len(t, got.Layers, 1)
		assert.Equal(t, ProxyChainLayerSOCKS5, got.Layers[0].Type)
		assert.Equal(t, "p", got.Layers[0].Host)
		assert.Equal(t, "enc", got.Layers[0].Password)
	})
}

func TestNormalizeProxyChainSkipsDisabledAndSorts(t *testing.T) {
	got := NormalizeProxyChain(&ProxyChainConfig{Layers: []ProxyChainLayer{
		{ID: "disabled", Type: ProxyChainLayerSOCKS5, Enabled: proxyChainEnabled(false), Order: 1, Host: "skip", Port: 1080},
		{ID: "second", Type: ProxyChainLayerSOCKS5, Enabled: proxyChainEnabled(true), Order: 20, Host: "p2", Port: 1080},
		{ID: "first", Type: ProxyChainLayerSOCKS5, Enabled: proxyChainEnabled(true), Order: 10, Host: "p1", Port: 1080},
	}})

	require.NotNil(t, got)
	require.Len(t, got.Layers, 2)
	assert.Equal(t, "first", got.Layers[0].ID)
	assert.Equal(t, "second", got.Layers[1].ID)
}

func TestValidateProxyChainLayers(t *testing.T) {
	tests := []struct {
		name  string
		chain *ProxyChainConfig
		want  string
	}{
		{
			name:  "ssh requires asset id",
			chain: &ProxyChainConfig{Layers: []ProxyChainLayer{{Type: ProxyChainLayerSSH, Enabled: proxyChainEnabled(true)}}},
			want:  "SSH 资产",
		},
		{
			name:  "socks5 requires host",
			chain: &ProxyChainConfig{Layers: []ProxyChainLayer{{Type: ProxyChainLayerSOCKS5, Enabled: proxyChainEnabled(true), Port: 1080}}},
			want:  "SOCKS5 主机",
		},
		{
			name:  "socks5 requires valid port",
			chain: &ProxyChainConfig{Layers: []ProxyChainLayer{{Type: ProxyChainLayerSOCKS5, Enabled: proxyChainEnabled(true), Host: "p", Port: 70000}}},
			want:  "SOCKS5 端口",
		},
		{
			name:  "http tunnel requires url",
			chain: &ProxyChainConfig{Layers: []ProxyChainLayer{{Type: ProxyChainLayerHTTPTunnel, Enabled: proxyChainEnabled(true), Token: "t"}}},
			want:  "HTTP 隧道 URL",
		},
		{
			name: "http tunnel must be first",
			chain: &ProxyChainConfig{Layers: []ProxyChainLayer{
				{Type: ProxyChainLayerSOCKS5, Enabled: proxyChainEnabled(true), Order: 1, Host: "p", Port: 1080},
				{Type: ProxyChainLayerHTTPTunnel, Enabled: proxyChainEnabled(true), Order: 2, URL: "https://dbx.example.com/dbx_tunnel.php", Token: "t"},
			}},
			want: "第一层",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProxyChain(tt.chain)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
