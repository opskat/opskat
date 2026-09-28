package redis_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testClusterInfo = "cluster_state:fail\r\ncluster_slots_assigned:16384\r\ncluster_slots_ok:10923\r\n" +
	"cluster_slots_pfail:0\r\ncluster_slots_fail:5461\r\ncluster_known_nodes:5\r\ncluster_size:3\r\n"

func nodeInfo(keys int64, mem int64, ops int64) string {
	return fmt.Sprintf("# Server\r\nredis_version:7.2.4\r\n# Memory\r\nused_memory:%d\r\nused_memory_human:%dB\r\n"+
		"# Stats\r\ninstantaneous_ops_per_sec:%d\r\n# Keyspace\r\ndb0:keys=%d,expires=0,avg_ttl=0\r\n", mem, mem, ops, keys)
}

func infoNodeFake(info string) fakeClusterNode {
	return func(args []any) (any, error) {
		switch strings.ToUpper(joinArgs(args)) {
		case "CLUSTER NODES":
			return testClusterNodes, nil
		case "CLUSTER INFO":
			return testClusterInfo, nil
		case "INFO":
			return info, nil
		}
		return nil, fmt.Errorf("unexpected %v", args)
	}
}

func newOverviewCluster() *fakeCluster {
	return &fakeCluster{
		shards: []string{m1, m2, m3},
		nodes: map[string]fakeClusterNode{
			m1: infoNodeFake(nodeInfo(70, 1000, 12)),
			m2: downNode,
			m3: infoNodeFake(nodeInfo(65, 3000, 5)),
			r1: infoNodeFake(nodeInfo(70, 900, 1)),
			r2: downNode,
		},
	}
}

func TestClusterOverview(t *testing.T) {
	t.Run("summary, nodes grouped under masters and per-node metrics", func(t *testing.T) {
		got, err := clusterOverview(context.Background(), newOverviewCluster(), "")
		require.NoError(t, err)

		assert.Equal(t, "fail", got.State)
		assert.Equal(t, 16384, got.SlotsAssigned)
		assert.Equal(t, 10923, got.SlotsOK)
		assert.Equal(t, 0, got.SlotsPFail)
		assert.Equal(t, 5461, got.SlotsFail)
		assert.Equal(t, int64(135), got.TotalKeys)
		assert.True(t, got.KeysPartial)

		require.Len(t, got.Masters, 3)
		a, b, c := got.Masters[0], got.Masters[1], got.Masters[2]
		assert.Equal(t, m1, a.Addr)
		assert.Equal(t, "id1111", a.ID)
		assert.Equal(t, "master", a.Role)
		assert.Equal(t, "0-5460", a.Slots)
		assert.Equal(t, 5461, a.SlotCount)
		assert.Equal(t, "ok", a.Status)
		assert.True(t, a.Reachable)
		assert.Equal(t, int64(70), a.Keys)
		assert.Equal(t, int64(1000), a.UsedMemory)
		assert.Equal(t, "1000B", a.UsedMemoryHuman)
		assert.Equal(t, int64(12), a.OpsPerSec)
		require.Len(t, a.Replicas, 1)
		assert.Equal(t, r1, a.Replicas[0].Addr)
		assert.Equal(t, "replica", a.Replicas[0].Role)
		assert.Equal(t, "id1111", a.Replicas[0].MasterID)
		assert.Equal(t, "ok", a.Replicas[0].Status)

		assert.Equal(t, m2, b.Addr)
		assert.Equal(t, "unreachable", b.Status)
		assert.False(t, b.Reachable)
		assert.Contains(t, b.Error, "connection refused")
		assert.Equal(t, int64(-1), b.Keys)
		assert.Equal(t, int64(-1), b.UsedMemory)
		assert.Equal(t, int64(-1), b.OpsPerSec)
		require.Len(t, b.Replicas, 1)
		assert.Equal(t, r2, b.Replicas[0].Addr)
		assert.Equal(t, "fail", b.Replicas[0].Status)
		assert.NotEmpty(t, b.Replicas[0].Error)

		assert.Equal(t, m3, c.Addr)
		assert.Empty(t, c.Replicas)

		assert.Equal(t, m1, got.InfoNode, "defaults to first master")
		assert.Equal(t, nodeInfo(70, 1000, 12), got.Info)
		assert.Empty(t, got.InfoError)
	})

	t.Run("info node can be a replica", func(t *testing.T) {
		got, err := clusterOverview(context.Background(), newOverviewCluster(), r1)
		require.NoError(t, err)
		assert.Equal(t, r1, got.InfoNode)
		assert.Equal(t, nodeInfo(70, 900, 1), got.Info)
	})

	t.Run("unreachable info node reports its error without failing the overview", func(t *testing.T) {
		got, err := clusterOverview(context.Background(), newOverviewCluster(), m2)
		require.NoError(t, err)
		assert.Equal(t, m2, got.InfoNode)
		assert.Empty(t, got.Info)
		assert.Contains(t, got.InfoError, "connection refused")
	})

	t.Run("unknown info node is rejected", func(t *testing.T) {
		_, err := clusterOverview(context.Background(), newOverviewCluster(), "10.9.9.9:1")
		require.ErrorContains(t, err, "10.9.9.9:1")
	})

	t.Run("all keys known when every master is reachable", func(t *testing.T) {
		c := newOverviewCluster()
		c.nodes[m2] = infoNodeFake(nodeInfo(5, 1, 1))
		got, err := clusterOverview(context.Background(), c, "")
		require.NoError(t, err)
		assert.Equal(t, int64(140), got.TotalKeys)
		assert.False(t, got.KeysPartial)
	})
}

// E20：概览摘要的 MasterCount / ReplicaCount 与 probe 用同一条计数规则——已失去 slot 且
// 被判定故障的前主节点不计入 MasterCount，即便它仍以 Role="master" 出现在 Masters 节点表格里。
func TestClusterOverviewMasterReplicaCounts(t *testing.T) {
	up := func(args []any) (any, error) {
		switch strings.ToUpper(joinArgs(args)) {
		case "CLUSTER NODES":
			return failoverClusterNodes, nil
		case "CLUSTER INFO":
			return "cluster_state:ok\r\n", nil
		case "INFO":
			return nodeInfo(10, 100, 1), nil
		}
		return nil, fmt.Errorf("unexpected %v", args)
	}
	c := &fakeCluster{
		shards: []string{failM1, failM2, failM6},
		nodes: map[string]fakeClusterNode{
			failM1: up, failM2: up, failM6: up, failR1: up, failR2: up,
			failM3: downNode,
		},
	}

	got, err := clusterOverview(context.Background(), c, "")

	require.NoError(t, err)
	assert.Equal(t, 3, got.MasterCount, "the fail-flagged master that lost its slots is not counted")
	assert.Equal(t, 2, got.ReplicaCount)
	// The node table itself still lists the failed former master, unlike the count.
	require.Len(t, got.Masters, 4)
}

func TestSentinelOverview(t *testing.T) {
	sentinel := &fakeRedisExecutor{results: []any{
		map[any]any{"name": "mymaster", "ip": "172.20.0.11", "port": "6379", "flags": "master", "quorum": "2",
			"num-slaves": "2", "num-other-sentinels": "2"},
		[]any{
			map[any]any{"ip": "172.20.0.12", "port": "6379", "flags": "slave", "master-link-status": "ok", "slave-repl-offset": "1000"},
			[]any{"ip", "172.20.0.13", "port", "6379", "flags", "slave,s_down,disconnected", "master-link-status", "err", "slave-repl-offset", "0"},
		},
		[]any{
			map[any]any{"ip": "172.20.0.22", "port": "26379", "flags": "sentinel"},
			map[any]any{"ip": "172.20.0.23", "port": "26379", "flags": "sentinel,s_down"},
		},
	}}
	data := &fakeRedisExecutor{results: []any{
		"# Replication\r\nrole:master\r\nconnected_slaves:1\r\nslave0:ip=172.20.0.12,port=6379,state=online,offset=990,lag=1\r\nmaster_repl_offset:1000\r\n",
	}}

	got, err := sentinelOverview(context.Background(), sentinelSource{exec: sentinel, addr: "10.0.0.9:26379"}, data, nil, "mymaster")

	require.NoError(t, err)
	assert.Equal(t, []any{"SENTINEL", "MASTER", "mymaster"}, sentinel.calls[0])
	assert.Equal(t, []any{"SENTINEL", "REPLICAS", "mymaster"}, sentinel.calls[1])
	assert.Equal(t, []any{"SENTINEL", "SENTINELS", "mymaster"}, sentinel.calls[2])
	assert.Equal(t, []any{"INFO", "replication"}, data.calls[0])

	assert.Equal(t, "mymaster", got.MasterName)
	assert.Equal(t, RedisSentinelNode{Addr: "172.20.0.11:6379", Flags: "master", Status: "ok"}, got.Master)
	assert.Equal(t, 2, got.Quorum)
	assert.Equal(t, []RedisSentinelReplica{
		{Addr: "172.20.0.12:6379", Flags: "slave", Status: "ok", LinkStatus: "ok", Offset: 990, LagSeconds: 1, LagBytes: 10},
		{Addr: "172.20.0.13:6379", Flags: "slave,s_down,disconnected", Status: "sdown", LinkStatus: "err", Offset: 0, LagSeconds: -1, LagBytes: -1},
	}, got.Replicas)
	assert.Equal(t, []RedisSentinelNode{
		{Addr: "10.0.0.9:26379", Flags: "sentinel", Status: "ok", Queried: true},
		{Addr: "172.20.0.22:26379", Flags: "sentinel", Status: "ok"},
		{Addr: "172.20.0.23:26379", Flags: "sentinel,s_down", Status: "sdown"},
	}, got.Sentinels)
	assert.Empty(t, got.MasterError)
}

func TestSentinelOverviewMasterUnavailable(t *testing.T) {
	sentinel := &fakeRedisExecutor{results: []any{
		[]any{"name", "mymaster", "ip", "172.20.0.11", "port", "6379", "flags", "master,s_down,o_down", "quorum", "2"},
		[]any{[]any{"ip", "172.20.0.12", "port", "6379", "flags", "slave", "master-link-status", "err", "slave-repl-offset", "500"}},
		[]any{},
	}}

	got, err := sentinelOverview(context.Background(), sentinelSource{exec: sentinel, addr: "10.0.0.9:26379"}, nil, errors.New("dial master: refused"), "mymaster")

	require.NoError(t, err)
	assert.Equal(t, "odown", got.Master.Status)
	assert.Contains(t, got.MasterError, "refused")
	require.Len(t, got.Replicas, 1)
	assert.Equal(t, int64(500), got.Replicas[0].Offset)
	assert.Equal(t, int64(-1), got.Replicas[0].LagSeconds)
	assert.Equal(t, int64(-1), got.Replicas[0].LagBytes)
	assert.Len(t, got.Sentinels, 1)
}

func TestSentinelOverviewSentinelError(t *testing.T) {
	sentinel := &fakeRedisExecutor{errs: []error{errors.New("ERR No such master with that name")}}
	_, err := sentinelOverview(context.Background(), sentinelSource{exec: sentinel, addr: "s:1"}, nil, nil, "nope")
	require.ErrorContains(t, err, "No such master")
}
