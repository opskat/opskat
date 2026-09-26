package connpool

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/socksdial/socksdialtest"
	"github.com/opskat/opskat/internal/sshpool"
)

func TestBuildRedisOptionsByMode(t *testing.T) {
	t.Run("cluster uses seed nodes and shared auth", func(t *testing.T) {
		opts, err := buildRedisOptions(&asset_entity.RedisConfig{
			Mode:     asset_entity.RedisModeCluster,
			Nodes:    []string{"10.0.0.1:7001", "10.0.0.2:7002"},
			Username: "default",
		}, "secret")
		require.NoError(t, err)

		c := opts.Cluster()
		assert.Equal(t, []string{"10.0.0.1:7001", "10.0.0.2:7002"}, c.Addrs)
		assert.Equal(t, "default", c.Username)
		assert.Equal(t, "secret", c.Password)
	})

	t.Run("sentinel uses sentinel nodes, master name and separate sentinel auth", func(t *testing.T) {
		opts, err := buildRedisOptions(&asset_entity.RedisConfig{
			Mode:             asset_entity.RedisModeSentinel,
			Nodes:            []string{"10.0.0.1:26379"},
			MasterName:       "mymaster",
			Username:         "data-user",
			Database:         3,
			SentinelUsername: "sentinel-user",
			SentinelPassword: "sentinel-secret",
			TLS:              true,
			TLSInsecure:      true,
		}, "data-secret")
		require.NoError(t, err)

		f := opts.Failover()
		assert.Equal(t, []string{"10.0.0.1:26379"}, f.SentinelAddrs)
		assert.Equal(t, "mymaster", f.MasterName)
		assert.Equal(t, "data-user", f.Username)
		assert.Equal(t, "data-secret", f.Password)
		assert.Equal(t, 3, f.DB)
		assert.Equal(t, "sentinel-user", f.SentinelUsername)
		assert.Equal(t, "sentinel-secret", f.SentinelPassword)
		require.NotNil(t, f.TLSConfig)
		assert.True(t, f.TLSConfig.InsecureSkipVerify)
	})

	t.Run("unknown mode is rejected", func(t *testing.T) {
		_, err := buildRedisOptions(&asset_entity.RedisConfig{Mode: "replica", Host: "h", Port: 6379}, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "replica")
	})
}

func TestNewRedisClientByMode(t *testing.T) {
	cases := []struct {
		cfg  *asset_entity.RedisConfig
		want any
	}{
		{&asset_entity.RedisConfig{Host: "127.0.0.1", Port: 1}, &redis.Client{}},
		{&asset_entity.RedisConfig{Mode: asset_entity.RedisModeCluster, Nodes: []string{"127.0.0.1:1"}}, &redis.ClusterClient{}},
		{&asset_entity.RedisConfig{Mode: asset_entity.RedisModeSentinel, Nodes: []string{"127.0.0.1:1"}, MasterName: "m"}, &redis.Client{}},
	}
	for _, tc := range cases {
		opts, err := buildRedisOptions(tc.cfg, "")
		require.NoError(t, err)
		client := newRedisClient(tc.cfg, opts)
		assert.IsType(t, tc.want, client, "mode %q", tc.cfg.Mode)
		require.NoError(t, client.Close())
	}
}

// forwardRecorder 是一个只记录 direct-tcpip 目标并转发到该目标的 SSH 服务器。
type forwardRecorder struct {
	mu      sync.Mutex
	targets []string
}

func (r *forwardRecorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.targets...)
}

type forwardDialer struct{ addr string }

func (d forwardDialer) DialAsset(_ context.Context, _ int64) (*ssh.Client, []io.Closer, error) {
	client, err := ssh.Dial("tcp", d.addr, &ssh.ClientConfig{
		User:            "u",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	return client, nil, err
}

func startForwardRecorder(t *testing.T) (*forwardRecorder, *sshpool.Pool) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(priv)
	require.NoError(t, err)
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	rec := &forwardRecorder{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer func() { _ = sc.Close() }()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					var d struct {
						DestAddr string
						DestPort uint32
						SrcAddr  string
						SrcPort  uint32
					}
					if err := ssh.Unmarshal(nc.ExtraData(), &d); err != nil {
						_ = nc.Reject(ssh.ConnectionFailed, "bad payload")
						continue
					}
					target := net.JoinHostPort(d.DestAddr, strconv.Itoa(int(d.DestPort)))
					rec.mu.Lock()
					rec.targets = append(rec.targets, target)
					rec.mu.Unlock()
					_ = nc.Reject(ssh.ConnectionFailed, "recorded")
				}
			}()
		}
	}()
	pool := sshpool.NewPool(forwardDialer{addr: ln.Addr().String()}, time.Minute)
	t.Cleanup(pool.Close)
	return rec, pool
}

func TestRedisTransportDialsRequestedAddress(t *testing.T) {
	t.Run("ssh tunnel dials the node go-redis asks for, after address mapping", func(t *testing.T) {
		rec, pool := startForwardRecorder(t)
		cfg := &asset_entity.RedisConfig{
			Mode:           asset_entity.RedisModeCluster,
			Nodes:          []string{"10.0.0.1:7001"},
			NodeAddressMap: map[string]string{"172.18.0.3:7002": "10.0.0.2:17002"},
		}
		opts, err := buildRedisOptions(cfg, "")
		require.NoError(t, err)
		tunnel, err := configureRedisTransport(opts, &asset_entity.Asset{SSHTunnelID: 9}, cfg, pool)
		require.NoError(t, err)
		require.NotNil(t, tunnel)
		require.NotNil(t, opts.Dialer)

		ctx := context.Background()
		_, err = opts.Dialer(ctx, "tcp", "10.0.0.1:7001")
		require.Error(t, err)
		_, err = opts.Dialer(ctx, "tcp", "172.18.0.3:7002")
		require.Error(t, err)
		_, err = opts.Dialer(ctx, "tcp", "10.0.0.4:7004")
		require.Error(t, err)
		assert.Equal(t, []string{"10.0.0.1:7001", "10.0.0.2:17002", "10.0.0.4:7004"}, rec.seen())
	})

	t.Run("fixed-target tunnel keeps dialing its configured target", func(t *testing.T) {
		rec, pool := startForwardRecorder(t)
		dial := tunnelDialFunc(NewSSHTunnel(9, "db.internal", 3306, pool))
		_, err := dial(context.Background(), "ignored:1")
		require.Error(t, err)
		assert.Equal(t, []string{"db.internal:3306"}, rec.seen())
	})

	t.Run("socks proxy dials the mapped address", func(t *testing.T) {
		echo := socksdialtest.StartEcho(t)
		proxyHost, proxyPort := splitHostPortInt(t, socksdialtest.Start(t, "", ""))
		cfg := &asset_entity.RedisConfig{
			Mode:           asset_entity.RedisModeSentinel,
			Nodes:          []string{"10.0.0.1:26379"},
			MasterName:     "m",
			NodeAddressMap: map[string]string{"172.18.0.2:6379": echo},
			Proxy:          &asset_entity.ProxyConfig{Type: "socks5", Host: proxyHost, Port: proxyPort},
		}
		opts, err := buildRedisOptions(cfg, "")
		require.NoError(t, err)
		_, err = configureRedisTransport(opts, &asset_entity.Asset{}, cfg, nil)
		require.NoError(t, err)

		assertEcho(t, opts.Dialer, "172.18.0.2:6379")
	})

	t.Run("direct connection honors address mapping", func(t *testing.T) {
		echo := socksdialtest.StartEcho(t)
		cfg := &asset_entity.RedisConfig{
			Mode:           asset_entity.RedisModeCluster,
			Nodes:          []string{"10.0.0.1:7001"},
			NodeAddressMap: map[string]string{"172.18.0.2:7001": echo},
		}
		opts, err := buildRedisOptions(cfg, "")
		require.NoError(t, err)
		tunnel, err := configureRedisTransport(opts, &asset_entity.Asset{}, cfg, nil)
		require.NoError(t, err)
		assert.Nil(t, tunnel)

		assertEcho(t, opts.Dialer, "172.18.0.2:7001")
		assertEcho(t, opts.Dialer, echo)
	})

	t.Run("direct connection without mapping keeps go-redis default dialer", func(t *testing.T) {
		cfg := &asset_entity.RedisConfig{Mode: asset_entity.RedisModeCluster, Nodes: []string{"10.0.0.1:7001"}, TLS: true}
		opts, err := buildRedisOptions(cfg, "")
		require.NoError(t, err)
		_, err = configureRedisTransport(opts, &asset_entity.Asset{}, cfg, nil)
		require.NoError(t, err)
		assert.Nil(t, opts.Dialer)
		assert.NotNil(t, opts.TLSConfig)
	})
}

func TestDialRedisLogsOmitSecrets(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := ln.Addr().String()
	require.NoError(t, ln.Close())

	cfg := &asset_entity.RedisConfig{
		Mode:             asset_entity.RedisModeSentinel,
		Nodes:            []string{closed},
		MasterName:       "mymaster",
		SentinelPassword: "sentinel-plain-secret",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = DialRedis(ctx, &asset_entity.Asset{ID: 42}, cfg, "data-plain-secret", nil)
	require.Error(t, err)

	entries := logs.All()
	require.NotEmpty(t, entries)
	var sawMode bool
	for _, e := range entries {
		line := fmt.Sprintf("%s %v", e.Message, e.ContextMap())
		assert.NotContains(t, line, "sentinel-plain-secret")
		assert.NotContains(t, line, "data-plain-secret")
		if e.ContextMap()["mode"] == asset_entity.RedisModeSentinel && e.ContextMap()["assetID"] == int64(42) {
			sawMode = true
		}
	}
	assert.True(t, sawMode, "connection log should record asset and mode")
}

func splitHostPortInt(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return host, port
}

func assertEcho(t *testing.T, dial func(context.Context, string, string) (net.Conn, error), addr string) {
	t.Helper()
	require.NotNil(t, dial)
	conn, err := dial(context.Background(), "tcp", addr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	msg := []byte("ping")
	_, err = conn.Write(msg)
	require.NoError(t, err)
	buf := make([]byte, len(msg))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	assert.Equal(t, msg, buf)
}
