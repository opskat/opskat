//go:build etcdfixture

package etcd_demo

import (
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"go.etcd.io/etcd/server/v3/embed"
)

// TestEtcdFixtureUp 启动一个本地 embed etcd 服务,监听 127.0.0.1:12379,
// 阻塞 30 分钟或直到 Ctrl+C。仅在 `-tags etcdfixture` 下编译,不进 make test。
func TestEtcdFixtureUp(t *testing.T) {
	dir, err := os.MkdirTemp("", "etcd-fixture-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	cfg := embed.NewConfig()
	cfg.Dir = filepath.Join(dir, "data")
	lcurl, _ := url.Parse("http://127.0.0.1:12379")
	lpurl, _ := url.Parse("http://127.0.0.1:12380")
	cfg.ListenClientUrls = []url.URL{*lcurl}
	cfg.AdvertiseClientUrls = []url.URL{*lcurl}
	cfg.ListenPeerUrls = []url.URL{*lpurl}
	cfg.AdvertisePeerUrls = []url.URL{*lpurl}
	cfg.InitialCluster = "default=http://127.0.0.1:12380"
	cfg.LogLevel = "info"

	e, err := embed.StartEtcd(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	select {
	case <-e.Server.ReadyNotify():
	case <-time.After(15 * time.Second):
		t.Fatal("embed etcd start timeout")
	}
	t.Logf("etcd fixture ready: %s", lcurl.String())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
		t.Log("fixture interrupted")
	case <-time.After(30 * time.Minute):
		t.Log("fixture timeout 30m")
	}
}
