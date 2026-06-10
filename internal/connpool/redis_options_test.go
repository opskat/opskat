package connpool

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"
)

func TestBuildRedisOptions(t *testing.T) {
	t.Run("maps basic redis config", func(t *testing.T) {
		opts, err := buildRedisOptions(&asset_entity.RedisConfig{
			Host:     "redis.internal",
			Port:     6380,
			Username: "default",
			Database: 2,
		}, "secret")

		require.NoError(t, err)
		assert.Equal(t, "redis.internal:6380", opts.Addr)
		assert.Equal(t, "default", opts.Username)
		assert.Equal(t, "secret", opts.Password)
		assert.Equal(t, 2, opts.DB)
		assert.Nil(t, opts.TLSConfig)
	})

	t.Run("maps tls and command timeout config", func(t *testing.T) {
		opts, err := buildRedisOptions(&asset_entity.RedisConfig{
			Host:                  "redis.internal",
			Port:                  6380,
			TLS:                   true,
			TLSInsecure:           true,
			TLSServerName:         "cache.example.com",
			CommandTimeoutSeconds: 7,
		}, "")

		require.NoError(t, err)
		require.NotNil(t, opts.TLSConfig)
		assert.True(t, opts.TLSConfig.InsecureSkipVerify)
		assert.Equal(t, "cache.example.com", opts.TLSConfig.ServerName)
		assert.Equal(t, 7*time.Second, opts.ReadTimeout)
		assert.Equal(t, 7*time.Second, opts.WriteTimeout)
	})
}

func TestConfigureRedisTransport(t *testing.T) {
	t.Run("direct keeps default dialer and tls", func(t *testing.T) {
		cfg := &asset_entity.RedisConfig{Host: "h", Port: 6379, TLS: true, TLSInsecure: true}
		opts, err := buildRedisOptions(cfg, "")
		require.NoError(t, err)

		tunnel := configureRedisTransport(opts, &asset_entity.Asset{}, cfg, nil)
		assert.Nil(t, tunnel)
		assert.Nil(t, opts.Dialer)
		assert.NotNil(t, opts.TLSConfig)
	})

	t.Run("proxy sets custom dialer", func(t *testing.T) {
		cfg := &asset_entity.RedisConfig{
			Host: "h", Port: 6379,
			Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
		}
		opts, err := buildRedisOptions(cfg, "")
		require.NoError(t, err)

		tunnel := configureRedisTransport(opts, &asset_entity.Asset{}, cfg, nil)
		assert.Nil(t, tunnel)
		assert.NotNil(t, opts.Dialer)
		assert.Nil(t, opts.TLSConfig)
	})

	t.Run("proxy with tls moves tls into dialer", func(t *testing.T) {
		// go-redis 设置自定义 Dialer 后默认 dialer 的 TLS 逻辑被绕过,
		// TLSConfig 必须清空并在 dialer 内手动包裹,否则 TLS 静默失效。
		cfg := &asset_entity.RedisConfig{
			Host: "h", Port: 6379, TLS: true, TLSInsecure: true,
			Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
		}
		opts, err := buildRedisOptions(cfg, "")
		require.NoError(t, err)
		require.NotNil(t, opts.TLSConfig)

		tunnel := configureRedisTransport(opts, &asset_entity.Asset{}, cfg, nil)
		assert.Nil(t, tunnel)
		assert.NotNil(t, opts.Dialer)
		assert.Nil(t, opts.TLSConfig)
	})

	t.Run("tunnel takes precedence over proxy", func(t *testing.T) {
		cfg := &asset_entity.RedisConfig{
			Host: "h", Port: 6379, TLS: true, TLSInsecure: true,
			Proxy: &asset_entity.ProxyConfig{Type: "socks5", Host: "p", Port: 1080},
		}
		opts, err := buildRedisOptions(cfg, "")
		require.NoError(t, err)

		pool := sshpool.NewPool(nil, time.Minute)
		tunnel := configureRedisTransport(opts, &asset_entity.Asset{SSHTunnelID: 5}, cfg, pool)
		assert.NotNil(t, tunnel)
		assert.NotNil(t, opts.Dialer)
		assert.Nil(t, opts.TLSConfig)
	})
}
