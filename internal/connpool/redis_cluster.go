package connpool

import (
	"context"
	"slices"
	"sync"

	"github.com/redis/go-redis/v9"
)

// RedisClusterNodeAddrs 返回集群客户端当前已知的节点地址（按地址排序）；mastersOnly 时只含主节点。
// 地址即 go-redis 拓扑中的宣告地址，可直接交给 RedisClusterNode 取节点连接。
func RedisClusterNodeAddrs(ctx context.Context, c *redis.ClusterClient, mastersOnly bool) ([]string, error) {
	var mu sync.Mutex
	var addrs []string
	collect := func(_ context.Context, node *redis.Client) error {
		mu.Lock()
		addrs = append(addrs, node.Options().Addr)
		mu.Unlock()
		return nil
	}
	var err error
	if mastersOnly {
		err = c.ForEachMaster(ctx, collect)
	} else {
		err = c.ForEachShard(ctx, collect)
	}
	if err != nil {
		return nil, err
	}
	slices.Sort(addrs)
	return addrs, nil
}

// RedisClusterNode 返回集群中地址为 addr 的节点（主或从）的客户端，复用集群客户端持有的
// 节点连接；addr 不是已知节点时返回 nil。
func RedisClusterNode(ctx context.Context, c *redis.ClusterClient, addr string) (*redis.Client, error) {
	var mu sync.Mutex
	var target *redis.Client
	err := c.ForEachShard(ctx, func(_ context.Context, node *redis.Client) error {
		if node.Options().Addr == addr {
			mu.Lock()
			target = node
			mu.Unlock()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return target, nil
}
