package helper

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/opskat/opskat/internal/connpool"
)

// RedisNodeRequiredError 表示集群模式下一条不带 key 的命令没有指定有效节点：scope 缺省、
// 不是集群中的节点，或是一个库号。Masters 为当前主节点地址，供调用方（AI、opsctl、
// 桌面控制台）提示用户从中选择。用 errors.As 识别。
type RedisNodeRequiredError struct {
	Command string
	Scope   string
	Masters []string
}

func (e *RedisNodeRequiredError) Error() string {
	masters := strings.Join(e.Masters, ", ")
	if e.Scope == "" {
		return fmt.Sprintf("redis cluster: %s has no key, so it must run on a specific node — pass scope as one of the master addresses: %s", e.Command, masters)
	}
	return fmt.Sprintf("redis cluster: scope %q is not a node of this cluster (a cluster has only db0; scope is a node host:port) — use one of the master addresses: %s", e.Scope, masters)
}

// redisClusterRouter 是集群路由所需的最小能力，生产实现为 clusterRouter。
type redisClusterRouter interface {
	// CommandKeys 返回命令中的 key（由 Redis 的命令元数据判定），无 key 时返回空。
	CommandKeys(ctx context.Context, args []string) ([]string, error)
	// DoByKey 按 args[keyPos] 所在 slot 执行，返回服务该 slot 的主节点地址。
	DoByKey(ctx context.Context, args []string, keyPos int) (any, string, error)
	// Masters 返回当前主节点地址（有序）。
	Masters(ctx context.Context) ([]string, error)
	// DoOnNode 在地址为 addr 的节点（主或从）上执行；节点不属于集群时 found=false。
	DoOnNode(ctx context.Context, addr string, args []string) (result any, found bool, err error)
}

// 集群执行结果中 route 字段的取值：结果由哪条规则决定了执行节点。
const (
	redisRouteKey   = "key"   // 按 key 的 slot 路由
	redisRouteScope = "scope" // scope 指定的节点
	redisRouteAny   = "any"   // 与节点无关的命令，由任一可用节点执行
)

// isNodeIndependentRedisCommand 判断命令结果与执行节点无关（任一节点执行都等价）。
// CLUSTER 只有读取全局拓扑 / 计算 slot 的子命令与节点无关；其余子命令只作用于收到它的
// 节点（RESET、FORGET、FAILOVER）或只在 slot 所在节点有数据（COUNTKEYSINSLOT），须指定节点。
func isNodeIndependentRedisCommand(args []string) bool {
	switch strings.ToUpper(args[0]) {
	case "PING", "ECHO", "TIME", "COMMAND":
		return true
	case "CLUSTER":
		if len(args) < 2 {
			return false
		}
		switch strings.ToUpper(args[1]) {
		case "INFO", "NODES", "SLOTS", "SHARDS", "KEYSLOT":
			return true
		}
	}
	return false
}

// execClusterArgs 按集群路由规则执行一条命令：
//  1. SELECT 一律拒绝（集群只有 db0）；
//  2. 与节点无关的命令（见 isNodeIndependentRedisCommand）：scope 给了就在该节点执行，否则由任一可用主节点执行；
//  3. 命令含 key：按 slot 路由，忽略 scope；
//  4. 其余无 key 命令：必须用 scope 指定集群中的节点，否则返回 *RedisNodeRequiredError。
//
// 结果都带 node（实际执行节点）与 route（路由依据）。
func execClusterArgs(ctx context.Context, r redisClusterRouter, args []string, scope string) (string, error) {
	name := args[0]
	if strings.EqualFold(name, "SELECT") {
		return "", fmt.Errorf("SELECT is not supported: a Redis cluster has only db0")
	}

	if isNodeIndependentRedisCommand(args) {
		if scope != "" {
			return execOnScopedNode(ctx, r, args, scope)
		}
		return execOnAnyMaster(ctx, r, args)
	}

	keys, err := r.CommandKeys(ctx, args)
	if err != nil {
		return "", fmt.Errorf("redis command failed: %w", err)
	}
	if len(keys) > 0 {
		keyPos := slices.Index(args[1:], keys[0]) + 1
		result, addr, err := r.DoByKey(ctx, args, keyPos)
		return formatRedisReply(result, err, clusterReplyExtra(addr, redisRouteKey))
	}
	return execOnScopedNode(ctx, r, args, scope)
}

func execOnScopedNode(ctx context.Context, r redisClusterRouter, args []string, scope string) (string, error) {
	if scope != "" {
		result, found, err := r.DoOnNode(ctx, scope, args)
		if !found && err != nil {
			return "", fmt.Errorf("redis command failed: %w", err)
		}
		if found {
			return formatRedisReply(result, err, clusterReplyExtra(scope, redisRouteScope))
		}
	}
	masters, err := r.Masters(ctx)
	if err != nil {
		return "", fmt.Errorf("list redis cluster masters: %w", err)
	}
	return "", &RedisNodeRequiredError{Command: strings.ToUpper(args[0]), Scope: scope, Masters: masters}
}

// execOnAnyMaster 依次尝试各主节点，直到某个节点给出回复（含 Redis 返回的错误）；
// 只有连接层失败才换下一个节点。
func execOnAnyMaster(ctx context.Context, r redisClusterRouter, args []string) (string, error) {
	masters, err := r.Masters(ctx)
	if err != nil {
		return "", fmt.Errorf("list redis cluster masters: %w", err)
	}
	var errs []error
	for _, addr := range masters {
		result, found, err := r.DoOnNode(ctx, addr, args)
		if !found && err != nil {
			return "", fmt.Errorf("redis command failed: %w", err)
		}
		if !found { // 取主节点列表后拓扑刚变化
			continue
		}
		if err != nil && !isRedisReply(err) {
			errs = append(errs, fmt.Errorf("%s: %w", addr, err))
			continue
		}
		return formatRedisReply(result, err, clusterReplyExtra(addr, redisRouteAny))
	}
	return "", fmt.Errorf("redis command failed: no reachable cluster node: %w", errors.Join(errs...))
}

// isRedisReply 判断 err 是服务端的回复（含 nil 回复），而不是连接层失败。
func isRedisReply(err error) bool {
	var redisErr redis.Error
	return errors.Is(err, redis.Nil) || errors.As(err, &redisErr)
}

func clusterReplyExtra(addr, route string) map[string]any {
	return map[string]any{"node": addr, "route": route}
}

// clusterRouter 基于 go-redis ClusterClient 实现 redisClusterRouter，
// 按节点执行复用集群客户端持有的节点连接。
type clusterRouter struct {
	client *redis.ClusterClient
}

// errRedisNoKeyArguments 是 COMMAND GETKEYS 对无 key 命令的回复。
const errRedisNoKeyArguments = "The command has no key arguments"

func (c clusterRouter) CommandKeys(ctx context.Context, args []string) ([]string, error) {
	keys, err := c.client.Do(ctx, append([]any{"COMMAND", "GETKEYS"}, toRedisArgs(args)...)...).StringSlice()
	if err != nil {
		if strings.Contains(err.Error(), errRedisNoKeyArguments) {
			return nil, nil
		}
		return nil, err
	}
	return keys, nil
}

func (c clusterRouter) DoByKey(ctx context.Context, args []string, keyPos int) (any, string, error) {
	cmd := redis.NewCmd(ctx, toRedisArgs(args)...)
	if keyPos <= math.MaxInt8 {
		cmd.SetFirstKeyPos(int8(keyPos))
	} // 否则由 go-redis 按默认位置选节点，再跟随 MOVED 重定向到正确节点
	_ = c.client.Process(ctx, cmd) // 错误随 cmd.Result() 返回
	result, cmdErr := cmd.Result()
	master, err := c.client.MasterForKey(ctx, args[keyPos])
	if err != nil {
		if cmdErr != nil {
			return nil, "", cmdErr
		}
		return nil, "", fmt.Errorf("resolve node for key: %w", err)
	}
	return result, master.Options().Addr, cmdErr
}

func (c clusterRouter) Masters(ctx context.Context) ([]string, error) {
	return connpool.RedisClusterNodeAddrs(ctx, c.client, true)
}

func (c clusterRouter) DoOnNode(ctx context.Context, addr string, args []string) (any, bool, error) {
	node, err := connpool.RedisClusterNode(ctx, c.client, addr)
	if err != nil {
		return nil, false, err
	}
	if node == nil {
		return nil, false, nil
	}
	result, err := node.Do(ctx, toRedisArgs(args)...).Result()
	return result, true, err
}
