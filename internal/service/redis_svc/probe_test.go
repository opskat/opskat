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

// replyErr 模拟服务端回复的错误（实现 redis.Error）。
type replyErr string

func (e replyErr) Error() string { return string(e) }
func (replyErr) RedisError()     {}

const probeClusterNodes = testClusterNodes +
	"id6666 10.0.0.6:7006@17006 slave id3333 0 1700 3 connected\n" +
	"id7777 10.0.0.7:7007@17007 master - 0 1700 4 connected\n"

// failoverClusterNodes 复现 E20：故障转移后 7006 接管了 7003 的 slot（10923-16383），
// 7003 仍带 master 标志但已无 slot 且被集群判定故障（fail）。真实拓扑是 3 主 2 从；
// 7003 不应再计入「主」。overview_test.go 复用同一份拓扑文本验证概览摘要走同一规则。
const (
	failM1 = "10.0.0.1:7001"
	failM2 = "10.0.0.2:7002"
	failM6 = "10.0.0.6:7006"
	failM3 = "10.0.0.3:7003"
	failR1 = "10.0.0.4:7004"
	failR2 = "10.0.0.5:7005"
)

const failoverClusterNodes = "" +
	"id1111 10.0.0.1:7001@17001 master - 0 1700 1 connected 0-5460\n" +
	"id2222 10.0.0.2:7002@17002 master - 0 1700 2 connected 5461-10922\n" +
	"id6666 10.0.0.6:7006@17006 master - 0 1700 3 connected 10923-16383\n" +
	"id3333 10.0.0.3:7003@17003 master,fail - 0 1700 3 disconnected\n" +
	"id4444 10.0.0.4:7004@17004 slave id1111 0 1700 1 connected\n" +
	"id5555 10.0.0.5:7005@17005 slave id2222 0 1700 2 connected\n"

func TestProbeCluster(t *testing.T) {
	up := func(args []any) (any, error) {
		switch strings.ToUpper(joinArgs(args)) {
		case "CLUSTER NODES":
			return probeClusterNodes, nil
		case "CLUSTER INFO":
			return "cluster_state:fail\r\ncluster_slots_assigned:16384\r\n", nil
		case "INFO SERVER":
			return "# Server\r\nredis_mode:cluster\r\n", nil
		case "PING":
			return "PONG", nil
		}
		return nil, fmt.Errorf("unexpected command %v", args)
	}
	cluster := &fakeCluster{
		// 客户端拓扑（CLUSTER SLOTS）只含持有 slot 的主节点及其在线从节点。
		shards: []string{m1, m2, m3, r1, r2},
		nodes: map[string]fakeClusterNode{
			m1: up, m2: up, m3: up, r1: up,
			// 应答了错误回复：节点本身可达。
			r2: func([]any) (any, error) { return nil, replyErr("NOAUTH Authentication required.") },
			// 宕机的从节点、失去 slot 的主节点：只出现在 CLUSTER NODES 中。
			"10.0.0.6:7006": downNode,
			"10.0.0.7:7007": downNode,
		},
	}

	got, mode := probeCluster(context.Background(), cluster)

	require.NotNil(t, got)
	assert.Equal(t, "cluster", mode)
	assert.Equal(t, "fail", got.State, "cluster_state is reported as-is")
	assert.Equal(t, 4, got.Masters)
	assert.Equal(t, 3, got.Replicas)
	assert.Equal(t, []string{"10.0.0.6:7006", "10.0.0.7:7007"}, got.UnreachableNodes,
		"every CLUSTER NODES address is probed, including ones outside the client's slot map")
	assert.Empty(t, cluster.callsTo("routed:"), "nothing is routed to a random slot's master")
}

// 集群状态与 redis_mode 只从应答了 PING 的节点读取，不会落到不可达的节点上。
func TestProbeClusterReadsStateFromAnsweringNode(t *testing.T) {
	up := func(args []any) (any, error) {
		switch strings.ToUpper(joinArgs(args)) {
		case "CLUSTER NODES":
			return testClusterNodes, nil
		case "CLUSTER INFO":
			return "cluster_state:ok\r\n", nil
		case "INFO SERVER":
			return "# Server\r\nredis_mode:cluster\r\n", nil
		case "PING":
			return "PONG", nil
		}
		return nil, fmt.Errorf("unexpected command %v", args)
	}
	cluster := &fakeCluster{
		shards: []string{m1},
		nodes:  map[string]fakeClusterNode{m1: downNode, m2: downNode, m3: up, r1: downNode, r2: downNode},
	}
	cluster.nodes[m1] = func(args []any) (any, error) {
		if strings.EqualFold(joinArgs(args), "CLUSTER NODES") {
			return testClusterNodes, nil
		}
		return downNode(args)
	}

	got, mode := probeCluster(context.Background(), cluster)

	require.NotNil(t, got)
	assert.Equal(t, "cluster", mode)
	assert.Equal(t, "ok", got.State)
	assert.Equal(t, []string{m1, m2, r1, r2}, got.UnreachableNodes)
}

// E20：master,fail 且已无 slot 的前主节点不计入「主」，probe 报告真实的 3 主 2 从。
func TestProbeClusterExcludesFailedMasterWithoutSlots(t *testing.T) {
	up := func(args []any) (any, error) {
		switch strings.ToUpper(joinArgs(args)) {
		case "CLUSTER NODES":
			return failoverClusterNodes, nil
		case "CLUSTER INFO":
			return "cluster_state:ok\r\n", nil
		case "INFO SERVER":
			return "# Server\r\nredis_mode:cluster\r\n", nil
		case "PING":
			return "PONG", nil
		}
		return nil, fmt.Errorf("unexpected command %v", args)
	}
	cluster := &fakeCluster{
		shards: []string{failM1, failM2, failM6, failR1, failR2},
		nodes: map[string]fakeClusterNode{
			failM1: up, failM2: up, failM6: up, failR1: up, failR2: up,
			failM3: downNode,
		},
	}

	got, mode := probeCluster(context.Background(), cluster)

	require.NotNil(t, got)
	assert.Equal(t, "cluster", mode)
	assert.Equal(t, 3, got.Masters, "the fail-flagged master that lost its slots is not counted")
	assert.Equal(t, 2, got.Replicas)
}

func TestProbeClusterTopologyUnavailable(t *testing.T) {
	cluster := &fakeCluster{
		shards: []string{m1},
		nodes:  map[string]fakeClusterNode{m1: downNode},
	}

	got, mode := probeCluster(context.Background(), cluster)
	assert.Nil(t, got, "nothing is fabricated when the topology can't be read")
	assert.Empty(t, mode)
}

func TestDetectRedisMode(t *testing.T) {
	exec := &fakeRedisExecutor{results: []any{"# Server\r\nredis_version:7.2.4\r\nredis_mode:sentinel\r\n"}}
	assert.Equal(t, "sentinel", detectRedisMode(context.Background(), exec))
	assert.Equal(t, []any{"INFO", "server"}, exec.calls[0])

	failing := &fakeRedisExecutor{errs: []error{replyErr("NOPERM")}}
	assert.Equal(t, "", detectRedisMode(context.Background(), failing))
}

// fakeSentinel 按命令回复一个哨兵的 INFO / SENTINEL MASTERS / SENTINEL SENTINELS。
func fakeSentinel(sentinels map[string][]any) redisExecutor {
	return execFunc(func(args []any) (any, error) {
		cmd := strings.ToUpper(joinArgs(args))
		switch {
		case cmd == "INFO SERVER":
			return "# Server\r\nredis_mode:sentinel\r\n", nil
		case cmd == "SENTINEL MASTERS":
			return []any{
				[]any{"name", "mymaster", "ip", "10.0.1.1", "port", "6379", "num-slaves", "2"},
				map[any]any{"name": "cache", "ip": "10.0.1.9", "port": "6380", "num-slaves": "0"},
			}, nil
		case strings.HasPrefix(cmd, "SENTINEL SENTINELS "):
			return sentinels[fmt.Sprint(args[2])], nil
		}
		return nil, fmt.Errorf("unexpected command %v", args)
	})
}

type execFunc func(args []any) (any, error)

func (f execFunc) Do(_ context.Context, args ...any) (any, error) { return f(args) }

func sentinelEntry(ip, port string) []any { return []any{"name", "x", "ip", ip, "port", port} }

func TestProbeSentinels(t *testing.T) {
	var dialed []string
	dial := func(_ context.Context, addr string) (redisExecutor, func(), error) {
		dialed = append(dialed, addr)
		if addr == "s1:26379" {
			return nil, nil, errors.New("dial tcp: connection refused")
		}
		return fakeSentinel(map[string][]any{
			"mymaster": {sentinelEntry("s1", "26379"), sentinelEntry("s3", "26379"), sentinelEntry("10.9.9.9", "26379")},
		}), func() {}, nil
	}

	t.Run("configured master name selects the group", func(t *testing.T) {
		dialed = nil
		got, mode, err := probeSentinels(context.Background(), []string{"s1:26379", "s2:26379", "s4:26379"}, "mymaster",
			map[string]string{"10.9.9.9:26379": "s4:26379"}, dial)

		require.NoError(t, err)
		assert.Equal(t, "sentinel", mode)
		assert.Equal(t, []string{"s1:26379", "s2:26379"}, dialed, "stops at the first reachable sentinel")
		assert.False(t, got.AuthRequired)
		assert.Equal(t, []RedisProbeSentinelGroup{
			{Name: "cache", MasterAddr: "10.0.1.9:6380", Replicas: 0},
			{Name: "mymaster", MasterAddr: "10.0.1.1:6379", Replicas: 2},
		}, got.Groups)
		assert.Equal(t, "10.0.1.1:6379", got.MasterAddr)
		// s1 已配置；10.9.9.9 经地址映射对应已配置的 s4。
		assert.Equal(t, []string{"s3:26379"}, got.OtherSentinels)
	})

	t.Run("unknown master name leaves master and sentinels absent", func(t *testing.T) {
		got, _, err := probeSentinels(context.Background(), []string{"s2:26379"}, "nope", nil, dial)
		require.NoError(t, err)
		assert.Len(t, got.Groups, 2)
		assert.Empty(t, got.MasterAddr)
		assert.Empty(t, got.OtherSentinels)
	})

	t.Run("several groups and no master name: no group is picked", func(t *testing.T) {
		got, _, err := probeSentinels(context.Background(), []string{"s2:26379"}, "", nil, dial)
		require.NoError(t, err)
		assert.Empty(t, got.MasterAddr)
		assert.Empty(t, got.OtherSentinels)
	})
}

func TestProbeSentinelsSingleGroupWithoutMasterName(t *testing.T) {
	dial := func(context.Context, string) (redisExecutor, func(), error) {
		return execFunc(func(args []any) (any, error) {
			switch strings.ToUpper(joinArgs(args)) {
			case "INFO SERVER":
				return "redis_mode:sentinel\r\n", nil
			case "SENTINEL MASTERS":
				return []any{[]any{"name", "only", "ip", "10.0.1.1", "port", "6379", "num-slaves", "1"}}, nil
			case "SENTINEL SENTINELS ONLY":
				return []any{sentinelEntry("s9", "26379")}, nil
			}
			return nil, fmt.Errorf("unexpected command %v", args)
		}), func() {}, nil
	}

	got, _, err := probeSentinels(context.Background(), []string{"s1:26379"}, "", nil, dial)

	require.NoError(t, err)
	assert.Equal(t, "10.0.1.1:6379", got.MasterAddr)
	assert.Equal(t, []string{"s9:26379"}, got.OtherSentinels)
}

func TestProbeSentinelsAuth(t *testing.T) {
	t.Run("auth error from every sentinel reports AuthRequired", func(t *testing.T) {
		dial := func(_ context.Context, addr string) (redisExecutor, func(), error) {
			if addr == "s1:26379" {
				return nil, nil, fmt.Errorf("redis 连接失败: %w", replyErr("NOAUTH Authentication required."))
			}
			return nil, nil, errors.New("dial tcp: connection refused")
		}
		got, mode, err := probeSentinels(context.Background(), []string{"s1:26379", "s2:26379"}, "mymaster", nil, dial)
		require.NoError(t, err)
		assert.True(t, got.AuthRequired)
		assert.Empty(t, mode)
		assert.Empty(t, got.Groups)
	})

	t.Run("wrong password counts as auth error", func(t *testing.T) {
		dial := func(context.Context, string) (redisExecutor, func(), error) {
			return nil, nil, replyErr("WRONGPASS invalid username-password pair or user is disabled.")
		}
		got, _, err := probeSentinels(context.Background(), []string{"s1:26379"}, "", nil, dial)
		require.NoError(t, err)
		assert.True(t, got.AuthRequired)
	})

	t.Run("no reachable sentinel is an error", func(t *testing.T) {
		dial := func(context.Context, string) (redisExecutor, func(), error) {
			return nil, nil, errors.New("dial tcp: connection refused")
		}
		_, _, err := probeSentinels(context.Background(), []string{"s1:26379"}, "", nil, dial)
		require.ErrorContains(t, err, "connection refused")
	})
}
