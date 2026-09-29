package redis_svc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClusterBrowseLive 在真实集群上验证多主节点扫描续扫、库计数、key 详情 slot/节点与概览解析。
// OPSKAT_REDIS_CLUSTER_TEST_NODES：逗号分隔的种子节点；OPSKAT_REDIS_CLUSTER_TEST_PASSWORD：可选密码。
func TestClusterBrowseLive(t *testing.T) {
	nodes := strings.TrimSpace(os.Getenv("OPSKAT_REDIS_CLUSTER_TEST_NODES"))
	if nodes == "" {
		t.Skip("set OPSKAT_REDIS_CLUSTER_TEST_NODES to run live Redis cluster test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:    strings.Split(nodes, ","),
		Password: os.Getenv("OPSKAT_REDIS_CLUSTER_TEST_PASSWORD"),
	})
	defer func() { _ = client.Close() }()
	exec := &goRedisClusterExecutor{goRedisExecutor: &goRedisExecutor{client: client}, cluster: client}

	dbs, err := listDatabases(ctx, exec)
	require.NoError(t, err)
	require.Len(t, dbs, 1)
	require.NotEmpty(t, dbs[0].Masters)
	t.Logf("db0 keys=%d masters=%+v", dbs[0].Keys, dbs[0].Masters)

	seen := map[string]bool{}
	cursor := "0"
	pages := 0
	var last RedisScanResponse
	for {
		last, err = scanKeys(ctx, exec, RedisScanRequest{Cursor: cursor, Count: 50})
		require.NoError(t, err)
		for _, k := range last.Keys {
			seen[k] = true
		}
		pages++
		cursor = last.Cursor
		if !last.HasMore {
			break
		}
	}
	t.Logf("pages=%d keys=%d scanned=%d/%d", pages, len(seen), last.ScannedMasters, last.TotalMasters)
	assert.Equal(t, int(dbs[0].Keys), len(seen), "paged scan covers every key once")
	assert.Equal(t, last.TotalMasters, last.ScannedMasters)
	assert.Greater(t, pages, 1)

	scoped, err := scanKeys(ctx, exec, RedisScanRequest{Count: 2000, Node: dbs[0].Masters[0].Addr})
	require.NoError(t, err)
	assert.Len(t, scoped.Keys, int(dbs[0].Masters[0].Keys))

	var anyKey string
	for k := range seen {
		anyKey = k
		break
	}
	exact, err := scanKeys(ctx, exec, RedisScanRequest{Match: anyKey, Exact: true})
	require.NoError(t, err)
	assert.Equal(t, []string{anyKey}, exact.Keys)
	detail, err := getKeyDetail(ctx, exec, RedisKeyRequest{Key: anyKey})
	require.NoError(t, err)
	require.NotNil(t, detail.Slot)
	slot, err := client.ClusterKeySlot(ctx, anyKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int(slot), *detail.Slot)
	t.Logf("%s slot=%d node=%s type=%s", anyKey, *detail.Slot, detail.Node, detail.Type)

	del, err := deleteKeys(ctx, exec, []string{"opskat:svc-live:missing-a", "opskat:svc-live:missing-b"})
	require.NoError(t, err)
	assert.Equal(t, RedisDeleteResult{}, del)

	overview, err := clusterOverview(ctx, exec, "")
	require.NoError(t, err)
	assert.Equal(t, "ok", overview.State)
	assert.Equal(t, 16384, overview.SlotsOK)
	assert.Equal(t, dbs[0].Keys, overview.TotalKeys)
	assert.NotEmpty(t, overview.Info)
	for _, m := range overview.Masters {
		t.Logf("master %s %s slots=%s keys=%d mem=%s ops=%d status=%s replicas=%d", m.Addr, m.ID[:8], m.Slots, m.Keys, m.UsedMemoryHuman, m.OpsPerSec, m.Status, len(m.Replicas))
	}
}

// TestSentinelOverviewLive 在真实哨兵上验证 SENTINEL 回复解析。
// OPSKAT_REDIS_SENTINEL_TEST_ADDR / _MASTER / _SENTINEL_PASSWORD / _PASSWORD。
func TestSentinelOverviewLive(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_ADDR"))
	if addr == "" {
		t.Skip("set OPSKAT_REDIS_SENTINEL_TEST_ADDR to run live Redis sentinel test")
	}
	master := os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_MASTER")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sentinel := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_SENTINEL_PASSWORD")})
	defer func() { _ = sentinel.Close() }()
	data := redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName: master, SentinelAddrs: []string{addr},
		SentinelPassword: os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_SENTINEL_PASSWORD"),
		Password:         os.Getenv("OPSKAT_REDIS_SENTINEL_TEST_PASSWORD"),
	})
	defer func() { _ = data.Close() }()

	got, err := sentinelOverview(ctx, sentinelSource{exec: &goRedisExecutor{client: sentinel}, addr: addr}, &goRedisExecutor{client: data}, nil, master)
	require.NoError(t, err)
	t.Logf("%+v", got)
	assert.Equal(t, "ok", got.Master.Status)
	assert.NotEqual(t, ":", got.Master.Addr)
	assert.Positive(t, got.Quorum)
	require.NotEmpty(t, got.Replicas)
	for _, r := range got.Replicas {
		assert.GreaterOrEqual(t, r.LagSeconds, int64(0), r.Addr)
	}
	assert.Greater(t, len(got.Sentinels), 1)
	assert.Empty(t, got.MasterError)
}
