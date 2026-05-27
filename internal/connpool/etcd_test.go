package connpool

import (
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
)

func TestBuildEtcdClientConfig_Defaults(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints: []string{"127.0.0.1:2379"},
	}
	clientCfg, err := buildEtcdClientConfig(cfg, "")
	assert.NoError(t, err)
	assert.Equal(t, []string{"127.0.0.1:2379"}, clientCfg.Endpoints)
	assert.Equal(t, 5*time.Second, clientCfg.DialTimeout)
	assert.Empty(t, clientCfg.Username)
	assert.Nil(t, clientCfg.TLS)
}

func TestBuildEtcdClientConfig_Auth(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints: []string{"e1:2379"},
		Username:  "root",
	}
	c, err := buildEtcdClientConfig(cfg, "s3cret")
	assert.NoError(t, err)
	assert.Equal(t, "root", c.Username)
	assert.Equal(t, "s3cret", c.Password)
}

func TestBuildEtcdClientConfig_TLSInsecure(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints:   []string{"e1:2379"},
		TLS:         true,
		TLSInsecure: true,
	}
	c, err := buildEtcdClientConfig(cfg, "")
	assert.NoError(t, err)
	assert.NotNil(t, c.TLS)
	assert.True(t, c.TLS.InsecureSkipVerify)
}

func TestBuildEtcdClientConfig_CustomDialTimeout(t *testing.T) {
	cfg := &asset_entity.EtcdConfig{
		Endpoints:          []string{"e1:2379"},
		DialTimeoutSeconds: 12,
	}
	c, err := buildEtcdClientConfig(cfg, "")
	assert.NoError(t, err)
	assert.Equal(t, 12*time.Second, c.DialTimeout)
}

func TestEtcdTunnelID(t *testing.T) {
	assert.Equal(t, int64(7), etcdTunnelID(
		&asset_entity.Asset{SSHTunnelID: 7},
		&asset_entity.EtcdConfig{SSHAssetID: 3},
	))
	assert.Equal(t, int64(3), etcdTunnelID(
		&asset_entity.Asset{},
		&asset_entity.EtcdConfig{SSHAssetID: 3},
	))
	assert.Zero(t, etcdTunnelID(&asset_entity.Asset{}, &asset_entity.EtcdConfig{}))
}

func TestEtcdPool_InvalidateRemovesEntry(t *testing.T) {
	pool := newEtcdPool()
	pool.put(1, &etcdEntry{client: nil, lastUsed: time.Now().Unix()})
	pool.put(2, &etcdEntry{client: nil, lastUsed: time.Now().Unix()})
	assert.NotNil(t, pool.get(1))

	pool.invalidate(1)
	assert.Nil(t, pool.get(1))
	assert.NotNil(t, pool.get(2))
}

func TestEtcdPool_GCStaleEntries(t *testing.T) {
	pool := newEtcdPool()
	pool.put(1, &etcdEntry{lastUsed: time.Now().Add(-10 * time.Minute).Unix()})
	pool.put(2, &etcdEntry{lastUsed: time.Now().Unix()})

	pool.gc(5 * time.Minute)
	assert.Nil(t, pool.get(1))
	assert.NotNil(t, pool.get(2))
}

func TestEtcdPool_GetRefreshesLastUsed(t *testing.T) {
	pool := newEtcdPool()
	old := time.Now().Add(-3 * time.Minute).Unix()
	pool.put(1, &etcdEntry{lastUsed: old})
	pool.get(1) // 此 get 应刷新 lastUsed
	e := pool.get(1)
	assert.True(t, e.lastUsed > old, "lastUsed should advance after get")
}
