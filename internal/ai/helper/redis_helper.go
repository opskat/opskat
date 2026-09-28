package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"

	"github.com/redis/go-redis/v9"
)

// --- Redis 连接缓存 ---

type redisCacheKeyType struct{}

// redisConnKey 区分 Redis 连接：单机 / 哨兵的库号是连接级参数（go-redis 在建连时
// SELECT），不同库必须是不同的客户端；集群只有 db0，按节点执行复用同一个集群客户端。
type redisConnKey struct {
	assetID int64
	db      int
}

// RedisClientCache 在同一次 AI Send 中复用 Redis 连接，按「资产 + 库」区分。
type RedisClientCache = KeyedConnCache[redisConnKey, redis.UniversalClient]

// NewRedisClientCache 创建 Redis 连接缓存
func NewRedisClientCache() *RedisClientCache {
	return NewKeyedConnCache[redisConnKey, redis.UniversalClient]("Redis")
}

// WithRedisCache 将 Redis 缓存注入 context
func WithRedisCache(ctx context.Context, cache *RedisClientCache) context.Context {
	return context.WithValue(ctx, redisCacheKeyType{}, cache)
}

func getRedisCache(ctx context.Context) *RedisClientCache {
	if cache, ok := ctx.Value(redisCacheKeyType{}).(*RedisClientCache); ok {
		return cache
	}
	return nil
}

func redisConnCacheKey(assetID int64, cfg *asset_entity.RedisConfig) redisConnKey {
	if cfg.EffectiveMode() == asset_entity.RedisModeCluster {
		return redisConnKey{assetID: assetID}
	}
	return redisConnKey{assetID: assetID, db: cfg.Database}
}

// ApplyRedisScope 把 scope 落到连接配置上：单机 / 哨兵时 scope 是库号，非空时覆盖
// 资产默认库；集群时 scope 是节点地址，不影响连接，由执行时的路由处理。
func ApplyRedisScope(cfg *asset_entity.RedisConfig, scope string) error {
	if scope == "" || cfg.EffectiveMode() == asset_entity.RedisModeCluster {
		return nil
	}
	dbIndex, err := strconv.Atoi(scope)
	if err != nil {
		return fmt.Errorf("scope must be a redis db number (0-15), got %q", scope)
	}
	cfg.Database = dbIndex
	return nil
}

// --- Executor ---

// ExecRedisOnAsset 是不含权限检查的纯执行入口：权限检查由调用方（统一 exec 工具的
// handleExec、batch_exec 的预检）在调用之前完成，这里只负责连接与执行。
// scope：单机 / 哨兵为库号（空 = 资产默认库）；集群为节点 host:port（见 ExecuteRedisRaw）。
func ExecRedisOnAsset(ctx context.Context, asset *asset_entity.Asset, command, scope string) (string, error) {
	cfg, err := asset.GetRedisConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get Redis config: %w", err)
	}
	if err := ApplyRedisScope(cfg, scope); err != nil {
		return "", err
	}

	client, closer, err := getOrDialRedis(ctx, asset, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to connect to Redis: %w", err)
	}
	if getRedisCache(ctx) == nil {
		if client != nil {
			defer func() {
				if err := client.Close(); err != nil {
					logger.Default().Warn("close Redis connection", zap.Error(err))
				}
			}()
		}
		if closer != nil {
			defer func() {
				if err := closer.Close(); err != nil {
					logger.Default().Warn("close Redis tunnel", zap.Error(err))
				}
			}()
		}
	}

	return ExecuteRedis(ctx, client, command, scope)
}

func getOrDialRedis(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.RedisConfig) (redis.UniversalClient, io.Closer, error) {
	dialFn := func() (redis.UniversalClient, io.Closer, error) {
		password, err := credential_resolver.Default().ResolveRedisPassword(ctx, cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve credentials: %w", err)
		}
		cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		cfg.SentinelPassword, err = credential_resolver.Default().ResolveRedisSentinelPassword(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve credentials: %w", err)
		}
		return connpool.DialRedis(ctx, asset, cfg, password, getSSHPool(ctx))
	}
	if cache := getRedisCache(ctx); cache != nil {
		return cache.GetOrDial(redisConnCacheKey(asset.ID, cfg), dialFn)
	}
	return dialFn()
}

// ExecuteRedis 执行 Redis 命令（按空白切分）并返回 JSON 结果，scope 语义同 ExecuteRedisRaw。
func ExecuteRedis(ctx context.Context, client redis.UniversalClient, command, scope string) (string, error) {
	return ExecuteRedisRaw(ctx, client, strings.Fields(command), scope)
}

// ExecuteRedisRaw 使用预拆分的参数执行 Redis 命令（支持含空格的值）并返回 JSON 结果。
// 单机 / 哨兵：库号已在建连时由 ApplyRedisScope 生效，这里直接执行。
// 集群：按命令路由（见 execClusterArgs），scope 是节点 host:port，结果注明执行节点。
func ExecuteRedisRaw(ctx context.Context, client redis.UniversalClient, args []string, scope string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("redis command is empty")
	}
	if cluster, ok := client.(*redis.ClusterClient); ok {
		return execClusterArgs(ctx, clusterRouter{client: cluster}, args, scope)
	}

	// SELECT 命令在连接池模式下无效，必须通过 scope 指定数据库
	if strings.EqualFold(args[0], "SELECT") {
		return "", fmt.Errorf("SELECT command is not supported due to connection pooling. Use the 'scope' parameter to specify the database number")
	}

	result, err := client.Do(ctx, toRedisArgs(args)...).Result()
	return formatRedisReply(result, err, nil)
}

func toRedisArgs(args []string) []any {
	out := make([]any, len(args))
	for i, p := range args {
		out[i] = p
	}
	return out
}

// formatRedisReply 把一次命令的回复转成 JSON；extra 为附加字段（集群的执行节点等）。
func formatRedisReply(result any, err error, extra map[string]any) (string, error) {
	if errors.Is(err, redis.Nil) {
		result, err = nil, nil
	}
	if err != nil {
		return "", fmt.Errorf("redis command failed: %w", err)
	}
	return formatRedisResult(result, extra)
}

func formatRedisResult(result any, extra map[string]any) (string, error) {
	var out map[string]any
	switch v := result.(type) {
	case string:
		out = map[string]any{"type": "string", "value": v}
	case int64:
		out = map[string]any{"type": "integer", "value": v}
	case []any:
		out = map[string]any{"type": "list", "value": v}
	case map[any]any:
		// Redis hash result
		m := make(map[string]any, len(v))
		for k, val := range v {
			m[fmt.Sprint(k)] = val
		}
		out = map[string]any{"type": "hash", "value": m}
	case nil:
		out = map[string]any{"type": "nil", "value": nil}
	default:
		out = map[string]any{"type": fmt.Sprintf("%T", v), "value": fmt.Sprint(v)}
	}
	for k, v := range extra {
		out[k] = v
	}
	data, err := json.Marshal(out)
	if err != nil {
		logger.Default().Error("marshal redis result", zap.Error(err))
		return "", fmt.Errorf("failed to marshal Redis result: %w", err)
	}
	return string(data), nil
}
