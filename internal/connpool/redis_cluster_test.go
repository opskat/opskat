package connpool

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
