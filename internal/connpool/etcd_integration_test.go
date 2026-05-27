//go:build integration

package connpool

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/server/v3/embed"
)

func startEmbedEtcd(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "etcd-test-*")
	require.NoError(t, err)

	cfg := embed.NewConfig()
	cfg.Dir = filepath.Join(dir, "data")
	lcurl, _ := url.Parse("http://127.0.0.1:12379")
	lpurl, _ := url.Parse("http://127.0.0.1:12380")
	cfg.ListenClientUrls = []url.URL{*lcurl}
	cfg.AdvertiseClientUrls = []url.URL{*lcurl}
	cfg.ListenPeerUrls = []url.URL{*lpurl}
	cfg.AdvertisePeerUrls = []url.URL{*lpurl}
	cfg.InitialCluster = "default=http://127.0.0.1:12380"
	cfg.LogLevel = "error"

	e, err := embed.StartEtcd(cfg)
	require.NoError(t, err)

	select {
	case <-e.Server.ReadyNotify():
	case <-time.After(10 * time.Second):
		e.Close()
		os.RemoveAll(dir)
		t.Fatal("embed etcd start timeout")
	}

	return "127.0.0.1:12379", func() {
		e.Close()
		_ = os.RemoveAll(dir)
	}
}

func TestDialEtcd_E2E_PutGetDel(t *testing.T) {
	endpoint, stop := startEmbedEtcd(t)
	defer stop()

	asset := &asset_entity.Asset{ID: 99}
	cfg := &asset_entity.EtcdConfig{Endpoints: []string{endpoint}}

	ctx := context.Background()
	client, tunnel, err := DialEtcd(ctx, asset, cfg, "", nil)
	require.NoError(t, err)
	defer client.Close()
	assert.Nil(t, tunnel, "direct connection should have nil tunnel")

	_, err = client.Put(ctx, "/test/foo", "bar")
	require.NoError(t, err)

	resp, err := client.Get(ctx, "/test/foo")
	require.NoError(t, err)
	require.Len(t, resp.Kvs, 1)
	assert.Equal(t, "bar", string(resp.Kvs[0].Value))

	delResp, err := client.Delete(ctx, "/test/foo")
	require.NoError(t, err)
	assert.Equal(t, int64(1), delResp.Deleted)

	resp2, err := client.Get(ctx, "/test/foo")
	require.NoError(t, err)
	assert.Empty(t, resp2.Kvs)
}
