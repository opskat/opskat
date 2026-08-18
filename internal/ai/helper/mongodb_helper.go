package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
)

// --- MongoDB 连接缓存 ---

type mongoDBCacheKeyType struct{}

// MongoDBClientCache 在同一次 AI Send 中复用 MongoDB 连接
type MongoDBClientCache = ConnCache[*connpool.MongoClientCloser]

// NewMongoDBClientCache 创建 MongoDB 连接缓存
func NewMongoDBClientCache() *MongoDBClientCache {
	return NewConnCache[*connpool.MongoClientCloser]("MongoDB")
}

// WithMongoDBCache 将 MongoDB 缓存注入 context
func WithMongoDBCache(ctx context.Context, cache *MongoDBClientCache) context.Context {
	return context.WithValue(ctx, mongoDBCacheKeyType{}, cache)
}

func getMongoDBCache(ctx context.Context) *MongoDBClientCache {
	if cache, ok := ctx.Value(mongoDBCacheKeyType{}).(*MongoDBClientCache); ok {
		return cache
	}
	return nil
}

func getOrDialMongoDB(ctx context.Context, asset *asset_entity.Asset) (*connpool.MongoClientCloser, io.Closer, error) {
	dialFn := func() (*connpool.MongoClientCloser, io.Closer, error) {
		cfg, err := asset.GetMongoDBConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get MongoDB config: %w", err)
		}
		password, err := credential_resolver.Default().ResolveMongoDBPassword(ctx, cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve credentials: %w", err)
		}
		cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		return connpool.DialMongoDB(ctx, asset, cfg, password, getSSHPool(ctx))
	}
	if cache := getMongoDBCache(ctx); cache != nil {
		return cache.GetOrDial(asset.ID, dialFn)
	}
	return dialFn()
}

// ListMongoDatabases 列出指定 MongoDB 资产的所有数据库名称。
// v1/v2 按连接模式自动分流，逻辑（含错误信息）由两个驱动共享。
func ListMongoDatabases(ctx context.Context, client *connpool.MongoClientCloser) ([]string, error) {
	return executorFor(client).ListDatabases(ctx)
}

// ListMongoCollections 列出指定 MongoDB 资产指定数据库的所有集合名称。
func ListMongoCollections(ctx context.Context, client *connpool.MongoClientCloser, database string) ([]string, error) {
	return executorFor(client).ListCollections(ctx, database)
}

// --- 辅助函数 ---

// parseQueryMap 将 query JSON 字符串解析为 map[string]json.RawMessage
func parseQueryMap(query string) (map[string]json.RawMessage, error) {
	if query == "" || query == "{}" {
		return make(map[string]json.RawMessage), nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(query), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// marshalResult 将结果 map 序列化为 JSON 字符串
func marshalResult(result map[string]any) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		logger.Default().Error("marshal MongoDB result", zap.Error(err))
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(data), nil
}
