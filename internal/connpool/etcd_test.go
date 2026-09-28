package connpool

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestEtcdDirectDial(t *testing.T) {
	t.Run("dials tcp endpoints", func(t *testing.T) {
		echo := socksdialtest.StartEcho(t)
		conn, err := etcdDirectDial(context.Background(), echo)
		require.NoError(t, err)
		_ = conn.Close()
	})

	t.Run("restores unix socket endpoints that grpc passes verbatim to custom dialers", func(t *testing.T) {
		dir, err := os.MkdirTemp("/tmp", "etcd") // macOS unix socket 路径上限 104 字节,t.TempDir 过长
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		sock := filepath.Join(dir, "etcd.sock")
		ln, err := net.Listen("unix", sock)
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()

		for _, addr := range []string{"unix://" + sock, "unix:" + sock} {
			conn, err := etcdDirectDial(context.Background(), addr)
			require.NoError(t, err, addr)
			_ = conn.Close()
		}
	})
}
