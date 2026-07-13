package connpool

import (
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func kafkaProxyChainEnabled(v bool) *bool {
	return &v
}

func TestKafkaConfigFingerprintIncludesProxy(t *testing.T) {
	base := &asset_entity.KafkaConfig{Brokers: []string{"b1:9092"}}
	withProxy := &asset_entity.KafkaConfig{
		Brokers: []string{"b1:9092"},
		Proxy:   &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080, Password: "enc"},
	}
	asset := &asset_entity.Asset{}

	assert.NotEqual(t, KafkaConfigFingerprint(asset, base), KafkaConfigFingerprint(asset, withProxy),
		"加代理后 fingerprint 必须变化,否则命中旧 client")

	same := &asset_entity.KafkaConfig{
		Brokers: []string{"b1:9092"},
		Proxy:   &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080, Password: "enc"},
	}
	assert.Equal(t, KafkaConfigFingerprint(asset, withProxy), KafkaConfigFingerprint(asset, same))

	changedPassword := &asset_entity.KafkaConfig{
		Brokers: []string{"b1:9092"},
		Proxy:   &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080, Password: "enc2"},
	}
	assert.NotEqual(t, KafkaConfigFingerprint(asset, withProxy), KafkaConfigFingerprint(asset, changedPassword))
}

func TestKafkaConfigFingerprintIncludesProxyChain(t *testing.T) {
	base := &asset_entity.KafkaConfig{Brokers: []string{"b1:9092"}}
	withChain := &asset_entity.KafkaConfig{
		Brokers: []string{"b1:9092"},
		ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{
			{ID: "ssh-1", Type: asset_entity.ProxyChainLayerSSH, Enabled: kafkaProxyChainEnabled(true), Order: 1, SSHAssetID: 7},
			{ID: "socks-1", Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: kafkaProxyChainEnabled(true), Order: 2, Host: "p", Port: 1080, Password: "enc"},
		}},
	}
	changedOrder := &asset_entity.KafkaConfig{
		Brokers: []string{"b1:9092"},
		ProxyChain: &asset_entity.ProxyChainConfig{Layers: []asset_entity.ProxyChainLayer{
			{ID: "socks-1", Type: asset_entity.ProxyChainLayerSOCKS5, Enabled: kafkaProxyChainEnabled(true), Order: 1, Host: "p", Port: 1080, Password: "enc"},
			{ID: "ssh-1", Type: asset_entity.ProxyChainLayerSSH, Enabled: kafkaProxyChainEnabled(true), Order: 2, SSHAssetID: 7},
		}},
	}

	assert.NotEqual(t, KafkaConfigFingerprint(&asset_entity.Asset{}, base), KafkaConfigFingerprint(&asset_entity.Asset{}, withChain))
	assert.NotEqual(t, KafkaConfigFingerprint(&asset_entity.Asset{}, withChain), KafkaConfigFingerprint(&asset_entity.Asset{}, changedOrder))
}

func TestBuildKafkaOptionsProxy(t *testing.T) {
	t.Run("proxy without ssh pool", func(t *testing.T) {
		cfg := &asset_entity.KafkaConfig{
			Brokers: []string{"b1:9092"},
			Proxy:   &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
		}
		opts, err := BuildKafkaOptions(&asset_entity.Asset{}, cfg, "", nil)
		require.NoError(t, err)
		client, err := kgo.NewClient(opts...)
		require.NoError(t, err)
		client.Close()
	})

	t.Run("proxy with tls does not set DialTLSConfig", func(t *testing.T) {
		// franz-go 禁止同时设置 Dialer 与 DialTLSConfig,若实现冲突 NewClient 会报错
		cfg := &asset_entity.KafkaConfig{
			Brokers:     []string{"b1:9092"},
			TLS:         true,
			TLSInsecure: true,
			Proxy:       &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
		}
		opts, err := BuildKafkaOptions(&asset_entity.Asset{}, cfg, "", nil)
		require.NoError(t, err)
		client, err := kgo.NewClient(opts...)
		require.NoError(t, err)
		client.Close()
	})
}
