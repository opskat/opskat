package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"github.com/opskat/opskat/internal/service/redis_svc"
	"github.com/opskat/opskat/internal/service/testreg"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// --- panel 连接缓存助手 ---

// getOrDialPanelDB 从面板缓存取 *sql.DB,所有数据库类型(含远端 SQLite VFS)统一缓存。
// 远端 SQLite 在会话内只抢一次 .opskat.lock,由缓存的空闲驱逐/关闭统一释放;其 *sql.DB
// 的 MaxOpenConns=1 让并发面板操作排队复用同一条连接,而非各自重连抢锁、并发时硬失败。
func (q *Query) getOrDialPanelDB(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.DatabaseConfig, password string) (*sql.DB, func() error, error) {
	key := panelDBCacheKey(asset.ID, cfg)
	db, _, err := q.dbPanelCache.GetOrDial(asset.ID, key, func() (*sql.DB, io.Closer, error) {
		cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		return connpool.DialDatabase(ctx, asset, cfg, password, q.pool)
	})
	if err != nil {
		return nil, nil, err
	}
	return db, func() error { return nil }, nil
}

func panelDBCacheKey(assetID int64, cfg *asset_entity.DatabaseConfig) string {
	if cfg.Driver == asset_entity.DriverSQLite {
		return fmt.Sprintf("%d", assetID)
	}
	return fmt.Sprintf("%d:%s", assetID, cfg.Database)
}

func finishPanelDBOperation(opErr error, cleanup func() error) error {
	if cleanup == nil {
		return opErr
	}
	if cleanupErr := cleanup(); cleanupErr != nil {
		return errors.Join(opErr, fmt.Errorf("释放数据库连接失败: %w", cleanupErr))
	}
	return opErr
}

// getOrDialPanelRedis 从面板缓存取 Redis 客户端。
func (q *Query) getOrDialPanelRedis(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.RedisConfig, password string) (redis.UniversalClient, error) {
	key := fmt.Sprintf("%d:%d", asset.ID, cfg.Database)
	client, _, err := q.redisPanelCache.GetOrDial(asset.ID, key, func() (redis.UniversalClient, io.Closer, error) {
		cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		var err error
		cfg.SentinelPassword, err = credential_resolver.Default().ResolveRedisSentinelPassword(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("解析凭据失败: %w", err)
		}
		return connpool.DialRedis(ctx, asset, cfg, password, q.pool)
	})
	if err != nil {
		return nil, err
	}
	return client, nil
}

// getOrDialPanelMongo 从面板缓存取 mongo 客户端。
func (q *Query) getOrDialPanelMongo(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.MongoDBConfig, password string) (*connpool.MongoClientCloser, error) {
	key := fmt.Sprintf("%d:legacy=%t", asset.ID, cfg.LegacyCompat)
	wrapped, _, err := q.mongoPanelCache.GetOrDial(asset.ID, key, func() (*connpool.MongoClientCloser, io.Closer, error) {
		cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		return connpool.DialMongoDB(ctx, asset, cfg, password, q.pool)
	})
	if err != nil {
		return nil, err
	}
	return wrapped, nil
}

// testDatabaseConnection 测试一份未保存的数据库配置；经 conntest 注册表由
// System.TestAssetConnection 分发，信封（超时/取消/i18n ctx）由调用方统一施加。
func (q *Query) testDatabaseConnection(ctx context.Context, configJSON string, plainPassword string) error {
	var cfg asset_entity.DatabaseConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}

	password := plainPassword
	if password == "" {
		var err error
		password, err = credential_resolver.Default().ResolveDatabasePassword(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
	}

	testAsset := &asset_entity.Asset{}
	cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
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

// testRedisConnection 测试一份未保存的 Redis 配置；经 conntest 注册表由
// System.TestAssetConnection 分发，信封（超时/取消/i18n ctx）由调用方统一施加。
func (q *Query) testRedisConnection(ctx context.Context, configJSON string, plainPassword string) error {
	cfg, password, err := redisTestConfig(ctx, configJSON, plainPassword, "")
	if err != nil {
		return err
	}
	client, tunnel, err := connpool.DialRedis(ctx, &asset_entity.Asset{}, cfg, password, q.pool)
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

// RedisProbe 探测一份未保存的 Redis 配置（资产表单「测试连接」与自动识别）：部署模式、
// 集群状态与不可直连节点、哨兵监控组与其他哨兵。信封同 System.TestAssetConnection：
// i18n ctx + 10s 超时，testID 配合 System.CancelTest 中断。
// plainPassword / plainSentinelPassword 为表单中的明文密码；为空时解析 configJSON 中已保存的
// （加密）值。数据节点连接或认证失败返回错误。
func (q *Query) RedisProbe(testID, configJSON, plainPassword, plainSentinelPassword string) (redis_svc.RedisProbeResult, error) {
	parent, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), 10*time.Second)
	defer cancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()
	cfg, password, err := redisTestConfig(ctx, configJSON, plainPassword, plainSentinelPassword)
	if err != nil {
		return redis_svc.RedisProbeResult{}, err
	}
	return redis_svc.New(q.pool).Probe(ctx, cfg, password)
}

// redisTestConfig 解析表单提交的未保存 Redis 配置，返回代理密码与哨兵密码已为明文的配置
// 及明文数据节点密码。明文参数为空时解析配置中已保存的值（托管凭据 / 密文）。
func redisTestConfig(ctx context.Context, configJSON, plainPassword, plainSentinelPassword string) (*asset_entity.RedisConfig, string, error) {
	var cfg asset_entity.RedisConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, "", fmt.Errorf("配置解析失败: %w", err)
	}

	password := plainPassword
	if password == "" {
		var err error
		password, err = credential_resolver.Default().ResolveRedisPassword(ctx, &cfg)
		if err != nil {
			return nil, "", fmt.Errorf("连接失败: %w", err)
		}
	}
	sentinelPassword := plainSentinelPassword
	if sentinelPassword == "" {
		var err error
		sentinelPassword, err = credential_resolver.Default().ResolveRedisSentinelPassword(&cfg)
		if err != nil {
			return nil, "", fmt.Errorf("连接失败: %w", err)
		}
	}
	cfg.SentinelPassword = sentinelPassword
	cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
	return &cfg, password, nil
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

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), queryTimeout(cfg))
	defer cancel()

	db, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接数据库失败: %w", err)
	}
	result, err := helper.ExecuteSQL(ctx, db, sqlText)
	if err := finishPanelDBOperation(err, cleanup); err != nil {
		return "", err
	}
	return result, nil
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

	db, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		if cleanupErr := finishPanelDBOperation(nil, cleanup); cleanupErr != nil {
			return nil, errors.Join(fmt.Errorf("打开数据库会话失败: %w", err), cleanupErr)
		}
		return nil, fmt.Errorf("打开数据库会话失败: %w", err)
	}

	result, err := query_svc.RunTableImportBatch(ctx, query_svc.NewSQLSession(conn), cfg.Driver, request)
	if closeErr := conn.Close(); closeErr != nil && !isExpectedPanelCloseErr(closeErr) {
		logger.Default().Warn("close db session failed", zap.Error(closeErr))
		err = errors.Join(err, fmt.Errorf("关闭数据库会话失败: %w", closeErr))
	}
	if err := finishPanelDBOperation(err, cleanup); err != nil {
		return nil, err
	}
	return result, nil
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

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), queryTimeout(cfg))
	defer cancel()

	db, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接数据库失败: %w", err)
	}
	result, opErr := query_svc.OpenTable(ctx, db, cfg.Driver, cfg.Database, table, pageSize)
	var payload []byte
	if opErr == nil {
		payload, err = json.Marshal(result)
		if err != nil {
			opErr = fmt.Errorf("序列化结果失败: %w", err)
		}
	}
	if err := finishPanelDBOperation(opErr, cleanup); err != nil {
		return "", err
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

	ctx, cancel := context.WithTimeout(i18n.Ctx(q.ctx, q.lang.Lang()), queryTimeout(cfg))
	defer cancel()

	db, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接数据库失败: %w", err)
	}
	result, err := helper.ExecuteSQLPaged(ctx, db, sqlText, page, pageSize, cfg.Driver)
	if err := finishPanelDBOperation(err, cleanup); err != nil {
		return "", err
	}
	return result, nil
}

// ExecuteRedis 在指定 Redis 资产上执行命令。scope：单机 / 哨兵为库号（空 = 资产默认库），
// 集群为节点 host:port（不带 key 的命令发往该节点，见 helper.ExecuteRedisRaw）。
func (q *Query) ExecuteRedis(assetID int64, command string, scope string) (string, error) {
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
	if err := helper.ApplyRedisScope(cfg, scope); err != nil {
		return "", err
	}
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

	return helper.ExecuteRedis(ctx, client, command, scope)
}

// testMongoConnection 测试一份未保存的 MongoDB 配置；经 conntest 注册表由
// System.TestAssetConnection 分发，信封（超时/取消/i18n ctx）由调用方统一施加。
func (q *Query) testMongoConnection(ctx context.Context, configJSON string, plainPassword string) error {
	var cfg asset_entity.MongoDBConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}

	password := plainPassword
	if password == "" {
		var err error
		password, err = credential_resolver.Default().ResolveMongoDBPassword(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("连接失败: %w", err)
		}
	}

	testAsset := &asset_entity.Asset{}
	cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
	mongoCloser, tunnel, err := connpool.DialMongoDB(ctx, testAsset, &cfg, password, q.pool)
	if err != nil {
		return err
	}
	defer func() {
		if err := mongoCloser.Close(); err != nil {
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

	return helper.ExecuteMongoDB(ctx, wrapped, database, collection, operation, query)
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

	names, err := helper.ListMongoDatabases(ctx, wrapped)
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

	names, err := helper.ListMongoCollections(ctx, wrapped, database)
	if err != nil {
		return "", err
	}
	result, err := json.Marshal(names)
	if err != nil {
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(result), nil
}

// ExecuteRedisArgs 使用预拆分的参数执行 Redis 命令（支持含空格的值），scope 语义同 ExecuteRedis。
func (q *Query) ExecuteRedisArgs(assetID int64, args []string, scope string) (string, error) {
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
	if err := helper.ApplyRedisScope(cfg, scope); err != nil {
		return "", err
	}
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

	return helper.ExecuteRedisRaw(ctx, client, args, scope)
}
