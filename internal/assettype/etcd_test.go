package assettype

import (
	"context"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEtcdHandler_Registered(t *testing.T) {
	h, ok := Get("etcd")
	require.True(t, ok, "etcd handler should be registered in init()")
	assert.Equal(t, "etcd", h.Type())
	assert.Equal(t, 2379, h.DefaultPort())
}

func TestEtcdHandler_ValidateCreateArgs(t *testing.T) {
	h := &etcdHandler{}
	assert.Error(t, h.ValidateCreateArgs(map[string]any{}), "empty args should fail")
	assert.NoError(t, h.ValidateCreateArgs(map[string]any{
		"endpoints": []any{"e1:2379"},
	}))
	assert.NoError(t, h.ValidateCreateArgs(map[string]any{
		"endpoints": []string{"a:2379", "b:2379"},
	}))
}

func TestEtcdHandler_SafeViewNoSecrets(t *testing.T) {
	h := &etcdHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeEtcd}
	require.NoError(t, a.SetEtcdConfig(&asset_entity.EtcdConfig{
		Endpoints:   []string{"e1:2379"},
		Username:    "root",
		Password:    "should-not-leak-encrypted-blob",
		TLS:         true,
		TLSKeyFile:  "/path/to/key.pem",
		TLSCertFile: "/path/to/cert.pem",
	}))
	view := h.SafeView(a)
	require.NotNil(t, view)
	assert.Contains(t, view, "endpoints")
	assert.Contains(t, view, "username")
	assert.Contains(t, view, "tls")
	assert.NotContains(t, view, "password")
	assert.NotContains(t, view, "tls_key_file")
	assert.NotContains(t, view, "tls_cert_file")
	assert.NotContains(t, view, "tls_ca_file")
}

func TestEtcdHandler_ApplyCreateArgs(t *testing.T) {
	h := &etcdHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeEtcd}
	err := h.ApplyCreateArgs(context.Background(), a, map[string]any{
		"endpoints":               []any{"e1:2379", "e2:2379"},
		"username":                "root",
		"tls":                     true,
		"tls_insecure":            true,
		"tls_server_name":         "etcd.example.com",
		"tls_ca_file":             "/ca.pem",
		"tls_cert_file":           "/cert.pem",
		"tls_key_file":            "/key.pem",
		"dial_timeout_seconds":    5,
		"command_timeout_seconds": 15,
		"ssh_asset_id":            int64(42),
	})
	require.NoError(t, err)
	cfg, err := a.GetEtcdConfig()
	require.NoError(t, err)
	assert.Equal(t, []string{"e1:2379", "e2:2379"}, cfg.Endpoints)
	assert.Equal(t, "root", cfg.Username)
	assert.True(t, cfg.TLS)
	assert.True(t, cfg.TLSInsecure)
	assert.Equal(t, "etcd.example.com", cfg.TLSServerName)
	assert.Equal(t, 5, cfg.DialTimeoutSeconds)
	assert.Equal(t, 15, cfg.CommandTimeoutSeconds)
	assert.Equal(t, int64(42), a.SSHTunnelID)
}

func TestEtcdHandler_ApplyUpdateArgs_PartialFields(t *testing.T) {
	h := &etcdHandler{}
	a := &asset_entity.Asset{Type: asset_entity.AssetTypeEtcd}
	require.NoError(t, a.SetEtcdConfig(&asset_entity.EtcdConfig{
		Endpoints: []string{"old:2379"}, Username: "old",
	}))
	err := h.ApplyUpdateArgs(context.Background(), a, map[string]any{
		"endpoints": []any{"new:2379"},
		// username 不传 — 应保留原值
	})
	require.NoError(t, err)
	cfg, _ := a.GetEtcdConfig()
	assert.Equal(t, []string{"new:2379"}, cfg.Endpoints)
	assert.Equal(t, "old", cfg.Username, "missing field should preserve existing value")
}

func TestEtcdHandler_DefaultPolicy(t *testing.T) {
	h := &etcdHandler{}
	p := h.DefaultPolicy()
	require.NotNil(t, p)
	policy, ok := p.(*asset_entity.EtcdPolicy)
	require.True(t, ok)
	assert.NotEmpty(t, policy.Groups, "default etcd policy should reference builtin groups")
}
