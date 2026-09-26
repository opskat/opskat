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

// NewRedisClusterNodeClient 为集群宣告的任意节点地址创建独立客户端（调用方负责 Close），
// 沿用集群客户端的拨号器（地址映射 / 隧道 / 代理 / TLS）、认证与超时。
// 用于 CLUSTER NODES 中存在、但不在客户端拓扑（CLUSTER SLOTS）里的节点，如失去 slot 的故障主节点、
// 宕机的从节点；已知节点应经 RedisClusterNode 复用集群客户端的连接。不做网络 I/O。
func NewRedisClusterNodeClient(c *redis.ClusterClient, addr string) *redis.Client {
	opt := c.Options()
	return redis.NewClient(&redis.Options{
		Addr:                       addr,
		ClientName:                 opt.ClientName,
		Dialer:                     opt.Dialer,
		OnConnect:                  opt.OnConnect,
		Protocol:                   opt.Protocol,
		Username:                   opt.Username,
		Password:                   opt.Password,
		CredentialsProvider:        opt.CredentialsProvider,
		CredentialsProviderContext: opt.CredentialsProviderContext,
		MaxRetries:                 opt.MaxRetries,
		MinRetryBackoff:            opt.MinRetryBackoff,
		MaxRetryBackoff:            opt.MaxRetryBackoff,
		DialTimeout:                opt.DialTimeout,
		ReadTimeout:                opt.ReadTimeout,
		WriteTimeout:               opt.WriteTimeout,
		ContextTimeoutEnabled:      opt.ContextTimeoutEnabled,
		DisableIdentity:            opt.DisableIdentity,
		IdentitySuffix:             opt.IdentitySuffix,
		TLSConfig:                  opt.TLSConfig,
		UnstableResp3:              opt.UnstableResp3,
	})
}
