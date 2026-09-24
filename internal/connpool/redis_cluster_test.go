package connpool

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// 静态拓扑（ClusterSlots）让集群客户端无需连网即可枚举节点。
func newStaticClusterClient(t *testing.T) *redis.ClusterClient {
	t.Helper()
	c := redis.NewClusterClient(&redis.ClusterOptions{
		ClusterSlots: func(context.Context) ([]redis.ClusterSlot, error) {
			return []redis.ClusterSlot{
				{Start: 8192, End: 16383, Nodes: []redis.ClusterNode{{Addr: "10.0.0.2:7002"}, {Addr: "10.0.0.4:7004"}}},
				{Start: 0, End: 8191, Nodes: []redis.ClusterNode{{Addr: "10.0.0.1:7001"}, {Addr: "10.0.0.3:7003"}}},
			}, nil
		},
	})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRedisClusterNodeAddrs(t *testing.T) {
	c := newStaticClusterClient(t)
	ctx := context.Background()

	masters, err := RedisClusterNodeAddrs(ctx, c, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.1:7001", "10.0.0.2:7002"}, masters)

	all, err := RedisClusterNodeAddrs(ctx, c, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.1:7001", "10.0.0.2:7002", "10.0.0.3:7003", "10.0.0.4:7004"}, all)
}

func TestRedisClusterNode(t *testing.T) {
	c := newStaticClusterClient(t)
	ctx := context.Background()

	node, err := RedisClusterNode(ctx, c, "10.0.0.4:7004")
	require.NoError(t, err)
	require.NotNil(t, node, "a replica is a node of the cluster")
	assert.Equal(t, "10.0.0.4:7004", node.Options().Addr)

	node, err = RedisClusterNode(ctx, c, "10.9.9.9:7009")
	require.NoError(t, err)
	assert.Nil(t, node)
}

// deadAddr 返回一个刚关闭监听、无人应答的本地地址。
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// clusterSlotsReply 构造 CLUSTER SLOTS 回复：每项为 [start, end, 主节点地址]。
func clusterSlotsReply(t *testing.T, ranges ...[3]string) string {
	t.Helper()
	var out strings.Builder
	fmt.Fprintf(&out, "*%d\r\n", len(ranges))
	for _, r := range ranges {
		host, port, err := net.SplitHostPort(r[2])
		require.NoError(t, err)
		fmt.Fprintf(&out, "*3\r\n:%s\r\n:%s\r\n*2\r\n%s:%s\r\n", r[0], r[1], bulk(host), port)
	}
	return out.String()
}

// 种子节点应答、另一个宣告的主节点（经地址映射）不可达时，集群连接必须成功：
// 连通性以种子应答为准，而不是随机 slot 所在的主节点。
func TestDialRedisClusterSucceedsWhenSeedAnswers(t *testing.T) {
	const deadAnnounced = "10.255.0.2:7002"
	var seed *respServer
	seed = startRESPServer(t, func(args []string) string {
		switch strings.ToUpper(args[0]) {
		case "PING":
			return "+PONG\r\n"
		case "CLUSTER":
			return clusterSlotsReply(t, [3]string{"0", "8191", seed.addr()}, [3]string{"8192", "16383", deadAnnounced})
		default:
			return "-ERR unknown command\r\n"
		}
	})
	cfg := &asset_entity.RedisConfig{
		Mode:           asset_entity.RedisModeCluster,
		Nodes:          []string{seed.addr()},
		NodeAddressMap: map[string]string{deadAnnounced: deadAddr(t)},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := 0; i < 20; i++ {
		client, closer, err := DialRedis(ctx, &asset_entity.Asset{ID: 7}, cfg, "", nil)
		require.NoError(t, err, "dial #%d", i)
		assert.Nil(t, closer)
		require.NoError(t, client.Close())
	}
}

// 种子应答但读不到集群拓扑（如未开启集群模式）时连接失败，并带出服务端的回复。
func TestDialRedisClusterFailsWhenSeedHasNoTopology(t *testing.T) {
	seed := startRESPServer(t, func(args []string) string {
		switch strings.ToUpper(args[0]) {
		case "PING":
			return "+PONG\r\n"
		case "CLUSTER":
			return "-ERR This instance has cluster support disabled\r\n"
		default:
			return "-ERR unknown command\r\n"
		}
	})
	cfg := &asset_entity.RedisConfig{Mode: asset_entity.RedisModeCluster, Nodes: []string{seed.addr()}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, err := DialRedis(ctx, &asset_entity.Asset{ID: 7}, cfg, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cluster support disabled")
}
