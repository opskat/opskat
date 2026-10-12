package connpool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/internal/connpool/connpooltest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTLSConfig(t *testing.T) {
	certPEM, keyPEM := connpooltest.SelfSignedPEM(t)
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))

	t.Run("证书内容直接构建出 CA 池与客户端证书", func(t *testing.T) {
		cfg, err := BuildTLSConfig("ES", TLSFields{
			ServerName: "es.example.com", CAPEM: string(certPEM), CertPEM: string(certPEM), KeyPEM: string(keyPEM),
		})
		require.NoError(t, err)
		assert.Equal(t, "es.example.com", cfg.ServerName)
		assert.NotNil(t, cfg.RootCAs)
		assert.Len(t, cfg.Certificates, 1)
	})

	t.Run("按路径读取证书", func(t *testing.T) {
		cfg, err := BuildTLSConfig("Redis", TLSFields{CAFile: certFile, CertFile: certFile, KeyFile: keyFile})
		require.NoError(t, err)
		assert.NotNil(t, cfg.RootCAs)
		assert.Len(t, cfg.Certificates, 1)
	})

	t.Run("每一项各自取路径或内容", func(t *testing.T) {
		cfg, err := BuildTLSConfig("Redis", TLSFields{CAFile: certFile, CertPEM: string(certPEM), KeyPEM: string(keyPEM)})
		require.NoError(t, err)
		assert.NotNil(t, cfg.RootCAs)
		assert.Len(t, cfg.Certificates, 1)
	})

	t.Run("同一项既给路径又给内容时报错，不替用户挑一个", func(t *testing.T) {
		_, err := BuildTLSConfig("Redis", TLSFields{CAFile: certFile, CAPEM: string(certPEM)})
		require.ErrorContains(t, err, "Redis")
	})

	t.Run("未提供证书时沿用系统根证书且不带客户端证书", func(t *testing.T) {
		cfg, err := BuildTLSConfig("ES", TLSFields{Insecure: true})
		require.NoError(t, err)
		assert.True(t, cfg.InsecureSkipVerify)
		assert.Nil(t, cfg.RootCAs)
		assert.Empty(t, cfg.Certificates)
	})

	t.Run("无法解析的 CA 证书报错", func(t *testing.T) {
		_, err := BuildTLSConfig("ES", TLSFields{CAPEM: "not a certificate"})
		require.ErrorContains(t, err, "ES")
	})

	t.Run("路径读不到时报错", func(t *testing.T) {
		_, err := BuildTLSConfig("Redis", TLSFields{CAFile: filepath.Join(dir, "missing.pem")})
		require.Error(t, err)
		_, err = BuildTLSConfig("Redis", TLSFields{CertFile: certFile, KeyFile: filepath.Join(dir, "missing.pem")})
		require.Error(t, err)
	})

	t.Run("客户端证书和私钥缺一报错", func(t *testing.T) {
		_, err := BuildTLSConfig("ES", TLSFields{CertPEM: string(certPEM)})
		require.Error(t, err)
		_, err = BuildTLSConfig("ES", TLSFields{KeyFile: keyFile})
		require.Error(t, err)
	})

	t.Run("私钥与证书不匹配报错", func(t *testing.T) {
		_, otherKey := connpooltest.SelfSignedPEM(t)
		_, err := BuildTLSConfig("ES", TLSFields{CertPEM: string(certPEM), KeyPEM: string(otherKey)})
		require.Error(t, err)
	})
}

// 资产配置里以内容形式保存的私钥是密文：连接前解密，解不开就不连。
func TestAssetTLSUsesTheStoredEncryptedKey(t *testing.T) {
	credential_svc.SetDefault(credential_svc.New("tls-test-key", []byte("0123456789abcdef")))
	certPEM, keyPEM := connpooltest.SelfSignedPEM(t)
	cert := string(certPEM)
	encryptedKey, err := credential_svc.Default().Encrypt(string(keyPEM))
	require.NoError(t, err)

	t.Run("Redis", func(t *testing.T) {
		cfg, err := buildRedisTLSConfig(&asset_entity.RedisConfig{TLSCAPEM: cert, TLSCertPEM: cert, TLSKeyPEM: encryptedKey})
		require.NoError(t, err)
		assert.NotNil(t, cfg.RootCAs)
		assert.Len(t, cfg.Certificates, 1)
	})
	t.Run("etcd", func(t *testing.T) {
		cfg, err := buildEtcdTLSConfig(&asset_entity.EtcdConfig{TLSCAPEM: cert, TLSCertPEM: cert, TLSKeyPEM: encryptedKey})
		require.NoError(t, err)
		assert.NotNil(t, cfg.RootCAs)
		assert.Len(t, cfg.Certificates, 1)
	})
	t.Run("Kafka", func(t *testing.T) {
		cfg, err := buildKafkaTLSConfig(&asset_entity.KafkaConfig{TLSCAPEM: cert, TLSCertPEM: cert, TLSKeyPEM: encryptedKey})
		require.NoError(t, err)
		assert.NotNil(t, cfg.RootCAs)
		assert.Len(t, cfg.Certificates, 1)
	})
	t.Run("没加密过的私钥不被当成密钥使用", func(t *testing.T) {
		_, err := buildRedisTLSConfig(&asset_entity.RedisConfig{TLSCertPEM: cert, TLSKeyPEM: string(keyPEM)})
		require.Error(t, err)
	})
}
