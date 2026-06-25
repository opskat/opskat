package asset_entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleProxy() *ProxyConfig {
	return &ProxyConfig{Type: "socks5", Host: "proxy.example.com", Port: 1080, Username: "pu", Password: "enc"}
}

func TestProxyConfigRoundTrip(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		a := &Asset{Type: AssetTypeDatabase}
		cfg := &DatabaseConfig{Driver: DriverMySQL, Host: "h", Port: 3306, Username: "u", Proxy: sampleProxy()}
		require.NoError(t, a.SetDatabaseConfig(cfg))
		got, err := a.GetDatabaseConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.Proxy, got.Proxy)
	})
	t.Run("redis", func(t *testing.T) {
		a := &Asset{Type: AssetTypeRedis}
		cfg := &RedisConfig{Host: "h", Port: 6379, Proxy: sampleProxy()}
		require.NoError(t, a.SetRedisConfig(cfg))
		got, err := a.GetRedisConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.Proxy, got.Proxy)
	})
	t.Run("mongodb", func(t *testing.T) {
		a := &Asset{Type: AssetTypeMongoDB}
		cfg := &MongoDBConfig{Host: "h", Port: 27017, Proxy: sampleProxy()}
		require.NoError(t, a.SetMongoDBConfig(cfg))
		got, err := a.GetMongoDBConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.Proxy, got.Proxy)
	})
	t.Run("etcd", func(t *testing.T) {
		a := &Asset{Type: AssetTypeEtcd}
		cfg := &EtcdConfig{Endpoints: []string{"10.0.0.1:2379"}, Proxy: sampleProxy()}
		require.NoError(t, a.SetEtcdConfig(cfg))
		got, err := a.GetEtcdConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.Proxy, got.Proxy)
	})
	t.Run("kafka", func(t *testing.T) {
		a := &Asset{Type: AssetTypeKafka}
		cfg := &KafkaConfig{Brokers: []string{"10.0.0.1:9092"}, Proxy: sampleProxy()}
		require.NoError(t, a.SetKafkaConfig(cfg))
		got, err := a.GetKafkaConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.Proxy, got.Proxy)
	})
	t.Run("k8s", func(t *testing.T) {
		a := &Asset{Type: AssetTypeK8s}
		cfg := &K8sConfig{Kubeconfig: "enc-kubeconfig", Namespace: "prod", Proxy: sampleProxy()}
		require.NoError(t, a.SetK8sConfig(cfg))
		got, err := a.GetK8sConfig()
		require.NoError(t, err)
		assert.Equal(t, cfg.Proxy, got.Proxy)
	})
}

func TestValidateDatabaseSQLiteRejectsProxy(t *testing.T) {
	a := &Asset{Type: AssetTypeDatabase, Name: "x", GroupID: 1}
	cfg := &DatabaseConfig{Driver: DriverSQLite, Path: "/tmp/x.db", Proxy: sampleProxy()}
	require.NoError(t, a.SetDatabaseConfig(cfg))
	err := a.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "代理")
}
