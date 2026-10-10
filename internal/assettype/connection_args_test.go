package assettype

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

const testConnectionKey = "__opskat_connection"

func extConnSpec(conn ExtensionConnectionSpec) ExtensionTypeSpec {
	spec := extSpec()
	conn.ConfigKey = testConnectionKey
	spec.Connection = conn
	return spec
}

func registerExtConn(t *testing.T, conn ExtensionConnectionSpec) AssetTypeHandler {
	t.Helper()
	require.NoError(t, RegisterExtensionType(extConnSpec(conn)))
	t.Cleanup(func() { Unregister("acme-store") })
	h, ok := Get("acme-store")
	require.True(t, ok)
	return h
}

// storedConnection reads what the extension host reads back: the host-reserved key of
// the asset's stored Config.
func storedConnection(t *testing.T, a *asset_entity.Asset) asset_entity.ExtensionConnectionConfig {
	t.Helper()
	var wrapper map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(a.Config), &wrapper))
	var conn asset_entity.ExtensionConnectionConfig
	if raw, ok := wrapper[testConnectionKey]; ok {
		require.NoError(t, json.Unmarshal(raw, &conn))
	}
	return conn
}

func decrypted(t *testing.T, ciphertext string) string {
	t.Helper()
	plain, err := credential_svc.Default().Decrypt(ciphertext)
	require.NoError(t, err)
	return plain
}

func TestExtensionTypeAcceptsDeclaredConnectionFields(t *testing.T) {
	h := registerExtConn(t, ExtensionConnectionSpec{SSHTunnel: true, TLS: true})

	prepared, err := PrepareCreate("acme-store", map[string]any{
		"endpoint": "https://acme.test", "token": "s3cret",
		"ssh_asset_id": float64(7),
		"tls":          true, "tls_server_name": "acme.internal",
		"tls_ca_pem": "CA-PEM", "tls_cert_pem": "CERT-PEM", "tls_key_pem": "KEY-PEM",
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"endpoint": "https://acme.test", "ssh_asset_id": float64(7),
		"tls": true, "tls_server_name": "acme.internal",
		"tls_ca_pem": "CA-PEM", "tls_cert_pem": "CERT-PEM",
	}, prepared.Approval, "the client key is a secret and stays out of the approval view")

	asset := &asset_entity.Asset{Type: "acme-store"}
	require.NoError(t, h.ApplyCreateArgs(context.Background(), asset, prepared.Config))

	assert.Equal(t, int64(7), asset.SSHTunnelID)
	conn := storedConnection(t, asset)
	require.NotNil(t, conn.TLS)
	assert.True(t, conn.TLS.Enabled)
	assert.Equal(t, "acme.internal", conn.TLS.ServerName)
	assert.Equal(t, "CA-PEM", conn.TLS.CACert)
	assert.Equal(t, "CERT-PEM", conn.TLS.ClientCert)
	assert.NotEqual(t, "KEY-PEM", conn.TLS.ClientKey, "the client key is stored encrypted")
	assert.Equal(t, "KEY-PEM", decrypted(t, conn.TLS.ClientKey))

	var stored map[string]any
	require.NoError(t, json.Unmarshal([]byte(asset.Config), &stored))
	assert.Equal(t, "https://acme.test", stored["endpoint"])
	for _, hostField := range []string{"ssh_asset_id", "tls", "tls_server_name", "tls_ca_pem", "tls_cert_pem", "tls_key_pem"} {
		assert.NotContains(t, stored, hostField, "host connection fields never reach the extension's own config")
	}
}

func TestExtensionTypeRejectsUndeclaredConnectionFields(t *testing.T) {
	registerExtConn(t, ExtensionConnectionSpec{SSHTunnel: true})

	for _, field := range []string{"tls", "tls_ca_pem", "proxy_chain"} {
		_, err := PrepareCreate("acme-store", map[string]any{"endpoint": "https://acme.test", field: "x"})
		require.Error(t, err, field)
		assert.Contains(t, err.Error(), field)
	}
}

func TestRegisterExtensionTypeRejectsSchemaFieldNamedLikeAConnectionField(t *testing.T) {
	spec := extConnSpec(ExtensionConnectionSpec{TLS: true})
	spec.ConfigFields = append(spec.ConfigFields, "tls_insecure")

	err := RegisterExtensionType(spec)
	t.Cleanup(func() { Unregister("acme-store") })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls_insecure")
}

func TestExtensionTypeUpdateSwitchesCertificateSourceAndKeepsTheRest(t *testing.T) {
	h := registerExtConn(t, ExtensionConnectionSpec{SSHTunnel: true, TLS: true})
	asset := &asset_entity.Asset{Type: "acme-store", SSHTunnelID: 7, Config: `{
		"endpoint":"https://acme.test",
		"__opskat_connection":{"tls":{"enabled":true,"serverName":"acme.internal","caFile":"/etc/ca.pem"}}}`}

	prepared, err := PrepareUpdate("acme-store", map[string]any{"tls_ca_pem": "CA-PEM", "tls_insecure": true})
	require.NoError(t, err)
	require.NoError(t, h.ApplyUpdateArgs(context.Background(), asset, prepared.Config))

	conn := storedConnection(t, asset)
	require.NotNil(t, conn.TLS)
	assert.Equal(t, "CA-PEM", conn.TLS.CACert)
	assert.Empty(t, conn.TLS.CAFile, "giving the certificate as content replaces the stored path")
	assert.True(t, conn.TLS.Enabled)
	assert.True(t, conn.TLS.Insecure)
	assert.Equal(t, "acme.internal", conn.TLS.ServerName)
	assert.Equal(t, int64(7), asset.SSHTunnelID)
}

func TestTLSCertificateGivenAsBothPathAndContentIsRejected(t *testing.T) {
	registerExtConn(t, ExtensionConnectionSpec{TLS: true})

	// The asset form has one source switch for the three certificates, so a request
	// mixing the two sources — even across different certificates — describes an asset
	// the form could not show without dropping half of it.
	for _, assetType := range []string{"acme-store", asset_entity.AssetTypeEtcd, asset_entity.AssetTypeKafka, asset_entity.AssetTypeRedis} {
		for _, mixed := range []map[string]any{
			{"tls_ca_file": "/etc/ca.pem", "tls_ca_pem": "CA-PEM"},
			{"tls_ca_file": "/etc/ca.pem", "tls_cert_pem": "CERT-PEM"},
		} {
			_, err := PrepareUpdate(assetType, mixed)
			require.Error(t, err, assetType)
			assert.Contains(t, err.Error(), "tls_ca_file", assetType)
		}
	}
}

var chainArg = []any{
	map[string]any{"type": "http_tunnel", "url": "https://gw.example.com/t?x=1", "token": "tok"},
	map[string]any{"type": "ssh", "ssh_asset_id": float64(12)},
	map[string]any{"type": "socks5", "host": "10.0.0.5", "port": float64(1080), "username": "u", "password": "pw"},
}

func assertStoredChain(t *testing.T, chain *asset_entity.ProxyChainConfig) {
	t.Helper()
	require.NotNil(t, chain)
	require.Len(t, chain.Layers, 3)
	require.NoError(t, asset_entity.ValidateProxyChain(chain))

	tunnel, ssh, socks := chain.Layers[0], chain.Layers[1], chain.Layers[2]
	assert.Equal(t, asset_entity.ProxyChainLayerHTTPTunnel, tunnel.Type)
	assert.Equal(t, "https://gw.example.com/t?x=1", tunnel.URL)
	assert.Equal(t, "tok", decrypted(t, tunnel.Token), "the tunnel token is stored encrypted")
	assert.Equal(t, asset_entity.ProxyChainLayerSSH, ssh.Type)
	assert.Equal(t, int64(12), ssh.SSHAssetID)
	assert.Equal(t, asset_entity.ProxyChainLayerSOCKS5, socks.Type)
	assert.Equal(t, "10.0.0.5", socks.Host)
	assert.Equal(t, 1080, socks.Port)
	assert.Equal(t, "u", socks.Username)
	assert.Equal(t, "pw", decrypted(t, socks.Password), "the proxy password is stored encrypted")

	ids := map[string]struct{}{}
	for i, layer := range chain.Layers {
		assert.Equal(t, i+1, layer.Order)
		assert.NotEmpty(t, layer.Name)
		ids[layer.ID] = struct{}{}
	}
	assert.Len(t, ids, 3, "the form keys its rows by layer id, so each layer needs its own")
}

var chainSummary = []string{"http_tunnel: https://gw.example.com/t", "ssh: asset #12", "socks5: u@10.0.0.5:1080"}

func TestExtensionTypeStoresAProxyChain(t *testing.T) {
	h := registerExtConn(t, ExtensionConnectionSpec{SSHTunnel: true, ProxyChain: true})

	prepared, err := PrepareCreate("acme-store", map[string]any{"endpoint": "https://acme.test", "proxy_chain": chainArg})
	require.NoError(t, err)
	assert.Equal(t, chainSummary, prepared.Approval["proxy_chain"],
		"the approver sees each hop, never the secrets riding on it")

	asset := &asset_entity.Asset{Type: "acme-store"}
	require.NoError(t, h.ApplyCreateArgs(context.Background(), asset, prepared.Config))
	assertStoredChain(t, storedConnection(t, asset).ProxyChain)
}

func TestProxyChainAndSSHTunnelReplaceEachOther(t *testing.T) {
	h := registerExtConn(t, ExtensionConnectionSpec{SSHTunnel: true, ProxyChain: true})

	_, err := PrepareCreate("acme-store", map[string]any{
		"endpoint": "https://acme.test", "ssh_asset_id": float64(7), "proxy_chain": chainArg,
	})
	require.Error(t, err, "one request cannot ask for both paths")
	assert.Contains(t, err.Error(), "proxy_chain")

	asset := &asset_entity.Asset{Type: "acme-store", SSHTunnelID: 7, Config: `{"endpoint":"https://acme.test"}`}
	require.NoError(t, h.ApplyUpdateArgs(context.Background(), asset, map[string]any{"proxy_chain": chainArg}))
	assert.Zero(t, asset.SSHTunnelID, "a chain replaces the single SSH tunnel")
	assertStoredChain(t, storedConnection(t, asset).ProxyChain)

	require.NoError(t, h.ApplyUpdateArgs(context.Background(), asset, map[string]any{"ssh_asset_id": float64(9)}))
	assert.Equal(t, int64(9), asset.SSHTunnelID)
	assert.Nil(t, storedConnection(t, asset).ProxyChain, "a single SSH tunnel replaces the chain")

	require.NoError(t, h.ApplyUpdateArgs(context.Background(), asset, map[string]any{"proxy_chain": chainArg}))
	require.NoError(t, h.ApplyUpdateArgs(context.Background(), asset, map[string]any{"proxy_chain": []any{}}))
	assert.Nil(t, storedConnection(t, asset).ProxyChain, "an empty chain clears it")
}

func TestProxyChainArgRejectsMalformedLayers(t *testing.T) {
	for name, chain := range map[string]any{
		"not a list":             "ssh",
		"layer not an object":    []any{"ssh"},
		"unknown layer type":     []any{map[string]any{"type": "vpn"}},
		"field of another type":  []any{map[string]any{"type": "ssh", "ssh_asset_id": float64(1), "host": "h"}},
		"ssh without an asset":   []any{map[string]any{"type": "ssh"}},
		"socks5 without a port":  []any{map[string]any{"type": "socks5", "host": "h"}},
		"http tunnel not first":  []any{map[string]any{"type": "ssh", "ssh_asset_id": float64(1)}, map[string]any{"type": "http_tunnel", "url": "https://gw", "token": "t"}},
		"http tunnel sans token": []any{map[string]any{"type": "http_tunnel", "url": "https://gw"}},
	} {
		_, err := PrepareUpdate(asset_entity.AssetTypeDatabase, map[string]any{"proxy_chain": chain})
		assert.Error(t, err, name)
	}
}

// builtinChainTypes maps every built-in type that dials through a proxy chain to how
// its stored chain is read back.
var builtinChainTypes = map[string]struct {
	create map[string]any
	chain  func(*asset_entity.Asset) (*asset_entity.ProxyChainConfig, error)
}{
	asset_entity.AssetTypeSSH: {
		create: map[string]any{"host": "h", "username": "root", "password": "pw"},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetSSHConfig()
			return cfg.ProxyChain, err
		},
	},
	asset_entity.AssetTypeRDP: {
		create: map[string]any{"host": "h", "username": "admin", "password": "pw"},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetRDPConfig()
			return cfg.ProxyChain, err
		},
	},
	asset_entity.AssetTypeVNC: {
		create: map[string]any{"host": "h", "password": "pw"},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetVNCConfig()
			return cfg.ProxyChain, err
		},
	},
	asset_entity.AssetTypeDatabase: {
		create: map[string]any{"driver": "mysql", "host": "h", "username": "app"},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetDatabaseConfig()
			return cfg.ProxyChain, err
		},
	},
	asset_entity.AssetTypeRedis: {
		create: map[string]any{"host": "h", "username": "default"},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetRedisConfig()
			return cfg.ProxyChain, err
		},
	},
	asset_entity.AssetTypeMongoDB: {
		create: map[string]any{"host": "h", "username": "app"},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetMongoDBConfig()
			return cfg.ProxyChain, err
		},
	},
	asset_entity.AssetTypeEtcd: {
		create: map[string]any{"endpoints": []any{"h:2379"}},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetEtcdConfig()
			return cfg.ProxyChain, err
		},
	},
	asset_entity.AssetTypeKafka: {
		create: map[string]any{"brokers": []any{"h:9092"}},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetKafkaConfig()
			return cfg.ProxyChain, err
		},
	},
	asset_entity.AssetTypeK8s: {
		create: map[string]any{"kubeconfig": "apiVersion: v1"},
		chain: func(a *asset_entity.Asset) (*asset_entity.ProxyChainConfig, error) {
			cfg, err := a.GetK8sConfig()
			return cfg.ProxyChain, err
		},
	},
}

func TestBuiltinTypesStoreAProxyChain(t *testing.T) {
	for assetType, tc := range builtinChainTypes {
		t.Run(assetType, func(t *testing.T) {
			args := map[string]any{"proxy_chain": chainArg}
			for k, v := range tc.create {
				args[k] = v
			}
			prepared, err := PrepareCreate(assetType, args)
			require.NoError(t, err)
			assert.Equal(t, chainSummary, prepared.Approval["proxy_chain"])

			asset := &asset_entity.Asset{Type: assetType}
			require.NoError(t, prepared.Handler.ApplyCreateArgs(context.Background(), asset, prepared.Config))
			chain, err := tc.chain(asset)
			require.NoError(t, err)
			assertStoredChain(t, chain)
			assert.Zero(t, asset.SSHTunnelID)

			require.NoError(t, prepared.Handler.ApplyUpdateArgs(context.Background(), asset, map[string]any{"proxy_chain": []any{}}))
			chain, err = tc.chain(asset)
			require.NoError(t, err)
			assert.Nil(t, chain, "an empty chain clears it")
		})
	}
}

func TestBuiltinSSHTunnelUpdateReplacesTheStoredChain(t *testing.T) {
	for _, assetType := range []string{
		asset_entity.AssetTypeDatabase, asset_entity.AssetTypeRedis, asset_entity.AssetTypeMongoDB,
		asset_entity.AssetTypeEtcd, asset_entity.AssetTypeKafka, asset_entity.AssetTypeK8s,
	} {
		t.Run(assetType, func(t *testing.T) {
			tc := builtinChainTypes[assetType]
			args := map[string]any{"proxy_chain": chainArg}
			for k, v := range tc.create {
				args[k] = v
			}
			prepared, err := PrepareCreate(assetType, args)
			require.NoError(t, err)
			asset := &asset_entity.Asset{Type: assetType}
			require.NoError(t, prepared.Handler.ApplyCreateArgs(context.Background(), asset, prepared.Config))

			require.NoError(t, prepared.Handler.ApplyUpdateArgs(context.Background(), asset, map[string]any{"ssh_asset_id": float64(9)}))
			chain, err := tc.chain(asset)
			require.NoError(t, err)
			assert.Nil(t, chain)
		})
	}
}

func TestBuiltinTLSCertificateContent(t *testing.T) {
	for assetType, tc := range map[string]struct {
		create map[string]any
		read   func(*asset_entity.Asset) (enabled bool, ca, cert, key, caFile string, err error)
	}{
		asset_entity.AssetTypeEtcd: {
			create: map[string]any{"endpoints": []any{"h:2379"}},
			read: func(a *asset_entity.Asset) (bool, string, string, string, string, error) {
				cfg, err := a.GetEtcdConfig()
				return cfg.TLS, cfg.TLSCAPEM, cfg.TLSCertPEM, cfg.TLSKeyPEM, cfg.TLSCAFile, err
			},
		},
		asset_entity.AssetTypeKafka: {
			create: map[string]any{"brokers": []any{"h:9092"}},
			read: func(a *asset_entity.Asset) (bool, string, string, string, string, error) {
				cfg, err := a.GetKafkaConfig()
				return cfg.TLS, cfg.TLSCAPEM, cfg.TLSCertPEM, cfg.TLSKeyPEM, cfg.TLSCAFile, err
			},
		},
		asset_entity.AssetTypeRedis: {
			create: map[string]any{"host": "h", "username": "default"},
			read: func(a *asset_entity.Asset) (bool, string, string, string, string, error) {
				cfg, err := a.GetRedisConfig()
				return cfg.TLS, cfg.TLSCAPEM, cfg.TLSCertPEM, cfg.TLSKeyPEM, cfg.TLSCAFile, err
			},
		},
	} {
		t.Run(assetType, func(t *testing.T) {
			args := map[string]any{"tls": true, "tls_ca_pem": "CA-PEM", "tls_cert_pem": "CERT-PEM", "tls_key_pem": "KEY-PEM"}
			for k, v := range tc.create {
				args[k] = v
			}
			prepared, err := PrepareCreate(assetType, args)
			require.NoError(t, err)
			assert.Equal(t, "CA-PEM", prepared.Approval["tls_ca_pem"])
			assert.NotContains(t, prepared.Approval, "tls_key_pem")

			asset := &asset_entity.Asset{Type: assetType}
			require.NoError(t, prepared.Handler.ApplyCreateArgs(context.Background(), asset, prepared.Config))
			enabled, ca, cert, key, _, err := tc.read(asset)
			require.NoError(t, err)
			assert.True(t, enabled)
			assert.Equal(t, "CA-PEM", ca)
			assert.Equal(t, "CERT-PEM", cert)
			assert.Equal(t, "KEY-PEM", decrypted(t, key), "the client key is stored encrypted")

			update, err := PrepareUpdate(assetType, map[string]any{"tls_ca_file": "/etc/ca.pem"})
			require.NoError(t, err)
			require.NoError(t, prepared.Handler.ApplyUpdateArgs(context.Background(), asset, update.Config))
			_, ca, cert, key, caFile, err := tc.read(asset)
			require.NoError(t, err)
			assert.Equal(t, "/etc/ca.pem", caFile)
			assert.Empty(t, ca+cert+key, "switching to paths replaces all the stored content: the three share one source")
		})
	}
}

// SSH 与 RDP 的隧道只存在资产的 SSHTunnelID 列上（拨号读的就是它）。
func TestSSHAndRDPStoreTheirSSHTunnel(t *testing.T) {
	for _, assetType := range []string{asset_entity.AssetTypeSSH, asset_entity.AssetTypeRDP} {
		t.Run(assetType, func(t *testing.T) {
			args := map[string]any{"ssh_asset_id": float64(7)}
			for k, v := range builtinChainTypes[assetType].create {
				args[k] = v
			}
			prepared, err := PrepareCreate(assetType, args)
			require.NoError(t, err)
			asset := &asset_entity.Asset{Type: assetType}
			require.NoError(t, prepared.Handler.ApplyCreateArgs(context.Background(), asset, prepared.Config))
			assert.Equal(t, int64(7), asset.SSHTunnelID)

			require.NoError(t, prepared.Handler.ApplyUpdateArgs(context.Background(), asset, map[string]any{"ssh_asset_id": float64(9)}))
			assert.Equal(t, int64(9), asset.SSHTunnelID)
			require.NoError(t, prepared.Handler.ApplyUpdateArgs(context.Background(), asset, map[string]any{"ssh_asset_id": float64(0)}))
			assert.Zero(t, asset.SSHTunnelID, "0 detaches the tunnel")
		})
	}
}
