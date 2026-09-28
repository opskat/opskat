package redis_svc

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func liveStandaloneConfig(t *testing.T, addr string) *asset_entity.RedisConfig {
	host, portText, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return &asset_entity.RedisConfig{Host: host, Port: port}
}

// TestProbeClusterLive：OPSKAT_REDIS_CLUSTER_TEST_NODES（逗号分隔种子节点）/ _PASSWORD。
func TestProbeClusterLive(t *testing.T) {
	nodes := strings.TrimSpace(os.Getenv("OPSKAT_REDIS_CLUSTER_TEST_NODES"))
	if nodes == "" {
		t.Skip("set OPSKAT_REDIS_CLUSTER_TEST_NODES to run live Redis cluster probe test")
	}
	password := os.Getenv("OPSKAT_REDIS_CLUSTER_TEST_PASSWORD")
	seeds := strings.Split(nodes, ",")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	svc := New(nil)

	t.Run("standalone config on a cluster node detects cluster", func(t *testing.T) {
		got, err := svc.Probe(ctx, liveStandaloneConfig(t, seeds[0]), password)
		require.NoError(t, err)
		assert.Equal(t, asset_entity.RedisModeCluster, got.DetectedMode)
		assert.True(t, got.ModeMismatch)
		assert.Nil(t, got.Cluster)
	})

	t.Run("cluster config reports state and topology", func(t *testing.T) {
		got, err := svc.Probe(ctx, &asset_entity.RedisConfig{Mode: asset_entity.RedisModeCluster, Nodes: seeds}, password)
		require.NoError(t, err)
		t.Logf("%+v cluster=%+v", got, got.Cluster)
		assert.Equal(t, asset_entity.RedisModeCluster, got.DetectedMode)
		assert.False(t, got.ModeMismatch)
		require.NotNil(t, got.Cluster)
		assert.Equal(t, "ok", got.Cluster.State)
		assert.Equal(t, 3, got.Cluster.Masters)
		assert.Equal(t, 3, got.Cluster.Replicas)
		assert.Empty(t, got.Cluster.UnreachableNodes)
	})

	t.Run("announced node mapped to a closed port is unreachable", func(t *testing.T) {
		client := redis.NewClusterClient(&redis.ClusterOptions{Addrs: seeds, Password: password})
		defer func() { _ = client.Close() }()
		text, err := client.ClusterNodes(ctx).Result()
		require.NoError(t, err)
		var replica string
		for _, n := range parseClusterNodes(text) {
			if !n.isMaster() {
				replica = n.Addr
				break
			}
		}
		require.NotEmpty(t, replica)

		got, err := svc.Probe(ctx, &asset_entity.RedisConfig{
			Mode: asset_entity.RedisModeCluster, Nodes: seeds,
			NodeAddressMap: map[string]string{replica: "127.0.0.1:1"},
		}, password)
		require.NoError(t, err)
		require.NotNil(t, got.Cluster)
		assert.Equal(t, []string{replica}, got.Cluster.UnreachableNodes)
	})

	t.Run("wrong data password is an error", func(t *testing.T) {
		_, err := svc.Probe(ctx, &asset_entity.RedisConfig{Mode: asset_entity.RedisModeCluster, Nodes: seeds}, "wrong-password")
		require.Error(t, err)
	})
}

// TestProbeStandaloneLive：OPSKAT_REDIS_STANDALONE_TEST_ADDR / _PASSWORD。
func TestProbeStandaloneLive(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("OPSKAT_REDIS_STANDALONE_TEST_ADDR"))
	if addr == "" {
		t.Skip("set OPSKAT_REDIS_STANDALONE_TEST_ADDR to run live Redis standalone probe test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	got, err := New(nil).Probe(ctx, liveStandaloneConfig(t, addr), os.Getenv("OPSKAT_REDIS_STANDALONE_TEST_PASSWORD"))

	require.NoError(t, err)
	assert.Equal(t, RedisProbeResult{DetectedMode: asset_entity.RedisModeStandalone}, got)
}

// TestProbeSentinelLive：OPSKAT_REDIS_SENTINEL_TEST_ADDR / _MASTER / _SENTINEL_PASSWORD / _PASSWORD。
func TestProbeSentinelLive(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_ADDR"))
	if addr == "" {
		t.Skip("set OPSKAT_REDIS_SENTINEL_TEST_ADDR to run live Redis sentinel probe test")
	}
	master := os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_MASTER")
	sentinelPassword := os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_SENTINEL_PASSWORD")
	password := os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_PASSWORD")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	svc := New(nil)
	sentinelCfg := func(masterName, sentinelPassword string) *asset_entity.RedisConfig {
		return &asset_entity.RedisConfig{
			Mode: asset_entity.RedisModeSentinel, Nodes: []string{addr},
			MasterName: masterName, SentinelPassword: sentinelPassword,
		}
	}

	t.Run("groups and other sentinels without master name", func(t *testing.T) {
		got, err := svc.Probe(ctx, sentinelCfg("", sentinelPassword), "")
		require.NoError(t, err)
		t.Logf("%+v sentinel=%+v", got, got.Sentinel)
		assert.Equal(t, asset_entity.RedisModeSentinel, got.DetectedMode)
		require.NotNil(t, got.Sentinel)
		require.Len(t, got.Sentinel.Groups, 1)
		assert.Equal(t, master, got.Sentinel.Groups[0].Name)
		assert.Positive(t, got.Sentinel.Groups[0].Replicas)
		assert.Equal(t, got.Sentinel.Groups[0].MasterAddr, got.Sentinel.MasterAddr)
		assert.Len(t, got.Sentinel.OtherSentinels, 2)
	})

	t.Run("configured master name connects the data nodes", func(t *testing.T) {
		got, err := svc.Probe(ctx, sentinelCfg(master, sentinelPassword), password)
		require.NoError(t, err)
		require.NotNil(t, got.Sentinel)
		assert.NotEmpty(t, got.Sentinel.MasterAddr)
		assert.False(t, got.ModeMismatch)

		_, err = svc.Probe(ctx, sentinelCfg(master, sentinelPassword), "wrong-password")
		require.Error(t, err, "data node auth failure is an error")
	})

	t.Run("missing sentinel password reports AuthRequired", func(t *testing.T) {
		got, err := svc.Probe(ctx, sentinelCfg(master, ""), password)
		require.NoError(t, err)
		require.NotNil(t, got.Sentinel)
		assert.True(t, got.Sentinel.AuthRequired)

		got, err = svc.Probe(ctx, sentinelCfg(master, "wrong-password"), password)
		require.NoError(t, err)
		require.NotNil(t, got.Sentinel)
		assert.True(t, got.Sentinel.AuthRequired, "wrong sentinel password")
	})
}
