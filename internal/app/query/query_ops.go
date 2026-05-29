package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/query_svc"
	"github.com/opskat/opskat/internal/service/testreg"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// --- panel 连接缓存助手 ---

// getOrDialPanelDB 从面板缓存取 *sql.DB,key 按 (assetID, cfg.Database)。
func (q *Query) getOrDialPanelDB(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.DatabaseConfig, password string) (*sql.DB, error) {
	key := fmt.Sprintf("%d:%s", asset.ID, cfg.Database)
	db, _, err := q.dbPanelCache.GetOrDial(key, func() (*sql.DB, io.Closer, error) {
		return connpool.DialDatabase(ctx, asset, cfg, password, q.pool)
	})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// getOrDialPanelRedis 从面板缓存取 *redis.Client。
func (q *Query) getOrDialPanelRedis(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.RedisConfig, password string) (*redis.Client, error) {
	key := fmt.Sprintf("%d:%d", asset.ID, cfg.Database)
	client, _, err := q.redisPanelCache.GetOrDial(key, func() (*redis.Client, io.Closer, error) {
		return connpool.DialRedis(ctx, asset, cfg, password, q.pool)
	})
	if err != nil {
		return nil, err
	}
	return client, nil
}

// getOrDialPanelMongo 从面板缓存取 mongo 客户端。
func (q *Query) getOrDialPanelMongo(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.MongoDBConfig, password string) (*connpool.MongoClientCloser, error) {
	key := fmt.Sprintf("%d", asset.ID)
	wrapped, _, err := q.mongoPanelCache.GetOrDial(key, func() (*connpool.MongoClientCloser, io.Closer, error) {
		client, closer, derr := connpool.DialMongoDB(ctx, asset, cfg, password, q.pool)
		if derr != nil {
			return nil, nil, derr
		}
		return &connpool.MongoClientCloser{Client: client}, closer, nil
	})
	if err != nil {
		return nil, err
	}
	return wrapped, nil
}

// TestDatabaseConnection 测试数据库连接
func (q *Query) TestDatabaseConnection(testID string, configJSON string, plainPassword string) error {
	var cfg asset_entity.DatabaseConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}

	parent, parentCancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 10*time.Second)
	defer parentCancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()

	password := plainPassword
	if password == "" {
		var err error
		password, err = credential_resolver.Default().ResolveDatabasePassword(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
	}

	testAsset := &asset_entity.Asset{}
	db, tunnel, err := connpool.DialDatabase(ctx, testAsset, &cfg, password, q.pool)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Default().Warn("close db failed", zap.Error(err))
		}
		if tunnel != nil {
			if err := tunnel.Close(); err != nil {
				logger.Default().Warn("close tunnel failed", zap.Error(err))
			}
		}
	}()
	return nil
}

// TestRedisConnection 测试 Redis 连接
func (q *Query) TestRedisConnection(testID string, configJSON string, plainPassword string) error {
	var cfg asset_entity.RedisConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}

	parent, parentCancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 10*time.Second)
	defer parentCancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()

	password := plainPassword
	if password == "" {
		var err error
		password, err = credential_resolver.Default().ResolveRedisPassword(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
	}

	testAsset := &asset_entity.Asset{}
	client, tunnel, err := connpool.DialRedis(ctx, testAsset, &cfg, password, q.pool)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Close(); err != nil {
			logger.Default().Warn("close redis client failed", zap.Error(err))
		}
		if tunnel != nil {
			if err := tunnel.Close(); err != nil {
				logger.Default().Warn("close tunnel failed", zap.Error(err))
			}
		}
	}()
	return nil
}

// ExecuteSQL 在指定数据库资产上执行 SQL 查询
func (q *Query) ExecuteSQL(assetID int64, sqlText string, database string) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return "", fmt.Errorf("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return "", fmt.Errorf("获取数据库配置失败: %w", err)
	}
	if database != "" {
		cfg.Database = database
	}
	password, err := credential_resolver.Default().ResolveDatabasePassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return "", fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 30*time.Second)
	defer cancel()

	db, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接数据库失败: %w", err)
	}

	return helper.ExecuteSQL(ctx, db, sqlText)
}

// ExecuteTableImport executes a prepared table import batch on one database session.
func (q *Query) ExecuteTableImport(
	assetID int64,
	database string,
	request query_svc.TableImportBatchRequest,
) (*query_svc.TableImportBatchResult, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return nil, fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return nil, fmt.Errorf("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return nil, fmt.Errorf("获取数据库配置失败: %w", err)
	}
	if database != "" {
		cfg.Database = database
	}
	password, err := credential_resolver.Default().ResolveDatabasePassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return nil, fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 30*time.Minute)
	defer cancel()

	db, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("打开数据库会话失败: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close db session failed", zap.Error(err))
		}
	}()

	return query_svc.RunTableImportBatch(ctx, query_svc.NewSQLSession(conn), cfg.Driver, request)
}

// OpenTable 一次性返回打开数据表所需的首屏数据。
func (q *Query) OpenTable(assetID int64, database, table string, pageSize int) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return "", fmt.Errorf("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return "", fmt.Errorf("获取数据库配置失败: %w", err)
	}
	if database != "" {
		cfg.Database = database
	}
	password, err := credential_resolver.Default().ResolveDatabasePassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return "", fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 30*time.Second)
	defer cancel()

	db, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接数据库失败: %w", err)
	}

	result, err := query_svc.OpenTable(ctx, db, cfg.Driver, cfg.Database, table, pageSize)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(payload), nil
}

// ExecuteSQLPaged 在指定数据库资产上执行分页 SQL 查询（SELECT/WITH 子查询包装）
func (q *Query) ExecuteSQLPaged(assetID int64, sqlText string, database string, page int, pageSize int) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return "", fmt.Errorf("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return "", fmt.Errorf("获取数据库配置失败: %w", err)
	}
	if database != "" {
		cfg.Database = database
	}
	password, err := credential_resolver.Default().ResolveDatabasePassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return "", fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 30*time.Second)
	defer cancel()

	db, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接数据库失败: %w", err)
	}

	return helper.ExecuteSQLPaged(ctx, db, sqlText, page, pageSize, cfg.Driver)
}

// ExecuteRedis 在指定 Redis 资产上执行命令
func (q *Query) ExecuteRedis(assetID int64, command string, db int) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsRedis() {
		return "", fmt.Errorf("资产不是 Redis 类型")
	}
	cfg, err := asset.GetRedisConfig()
	if err != nil {
		return "", fmt.Errorf("获取 Redis 配置失败: %w", err)
	}
	cfg.Database = db
	password, err := credential_resolver.Default().ResolveRedisPassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return "", fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 30*time.Second)
	defer cancel()

	client, err := q.getOrDialPanelRedis(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接 Redis 失败: %w", err)
	}

	return helper.ExecuteRedis(ctx, client, command)
}

// TestMongoDBConnection 测试 MongoDB 连接
func (q *Query) TestMongoDBConnection(testID string, configJSON string, plainPassword string) error {
	var cfg asset_entity.MongoDBConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}

	parent, parentCancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 10*time.Second)
	defer parentCancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()

	password := plainPassword
	if password == "" {
		var err error
		password, err = credential_resolver.Default().ResolveMongoDBPassword(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
	}

	testAsset := &asset_entity.Asset{}
	client, tunnel, err := connpool.DialMongoDB(ctx, testAsset, &cfg, password, q.pool)
	if err != nil {
		return err
	}
	defer func() {
		if err := client.Disconnect(context.Background()); err != nil {
			logger.Default().Warn("disconnect mongodb client failed", zap.Error(err))
		}
		if tunnel != nil {
			if err := tunnel.Close(); err != nil {
				logger.Default().Warn("close tunnel failed", zap.Error(err))
			}
		}
	}()
	return nil
}

// ExecuteMongo 在指定 MongoDB 资产上执行操作
func (q *Query) ExecuteMongo(assetID int64, operation, database, collection, query string) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsMongoDB() {
		return "", fmt.Errorf("资产不是 MongoDB 类型")
	}
	cfg, err := asset.GetMongoDBConfig()
	if err != nil {
		return "", fmt.Errorf("获取 MongoDB 配置失败: %w", err)
	}
	password, err := credential_resolver.Default().ResolveMongoDBPassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return "", fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 30*time.Second)
	defer cancel()

	wrapped, err := q.getOrDialPanelMongo(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接 MongoDB 失败: %w", err)
	}

	return helper.ExecuteMongoDB(ctx, wrapped.Client, database, collection, operation, query)
}

// ListMongoDatabases 列出指定 MongoDB 资产的所有数据库
func (q *Query) ListMongoDatabases(assetID int64) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsMongoDB() {
		return "", fmt.Errorf("资产不是 MongoDB 类型")
	}
	cfg, err := asset.GetMongoDBConfig()
	if err != nil {
		return "", fmt.Errorf("获取 MongoDB 配置失败: %w", err)
	}
	password, err := credential_resolver.Default().ResolveMongoDBPassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return "", fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 10*time.Second)
	defer cancel()

	wrapped, err := q.getOrDialPanelMongo(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接 MongoDB 失败: %w", err)
	}

	names, err := helper.ListMongoDatabases(ctx, wrapped.Client)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(names)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(result), nil
}

// ListMongoCollections 列出指定 MongoDB 资产中某个数据库的所有集合
func (q *Query) ListMongoCollections(assetID int64, database string) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsMongoDB() {
		return "", fmt.Errorf("资产不是 MongoDB 类型")
	}
	cfg, err := asset.GetMongoDBConfig()
	if err != nil {
		return "", fmt.Errorf("获取 MongoDB 配置失败: %w", err)
	}
	password, err := credential_resolver.Default().ResolveMongoDBPassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return "", fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 10*time.Second)
	defer cancel()

	wrapped, err := q.getOrDialPanelMongo(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接 MongoDB 失败: %w", err)
	}

	names, err := helper.ListMongoCollections(ctx, wrapped.Client, database)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(names)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(result), nil
}

// ExecuteRedisArgs 使用预拆分的参数执行 Redis 命令（支持含空格的值）
func (q *Query) ExecuteRedisArgs(assetID int64, args []string, db int) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(q.ctx, q.lang.Lang()), assetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsRedis() {
		return "", fmt.Errorf("资产不是 Redis 类型")
	}
	cfg, err := asset.GetRedisConfig()
	if err != nil {
		return "", fmt.Errorf("获取 Redis 配置失败: %w", err)
	}
	cfg.Database = db
	password, err := credential_resolver.Default().ResolveRedisPassword(i18n.Ctx(q.ctx, q.lang.Lang()), cfg)
	if err != nil {
		return "", fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 30*time.Second)
	defer cancel()

	client, err := q.getOrDialPanelRedis(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接 Redis 失败: %w", err)
	}

	return helper.ExecuteRedisRaw(ctx, client, args)
}
