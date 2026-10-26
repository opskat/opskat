package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/connpool"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/query_svc"
	"github.com/opskat/opskat/internal/service/redis_svc"
	"github.com/opskat/opskat/internal/service/testreg"
	"github.com/opskat/opskat/pkg/dbdriver"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// --- panel 连接缓存助手 ---

// getOrDialPanelDB 从面板缓存取数据库会话,所有驱动(含远端 SQLite VFS)统一缓存。会话由资产
// 配置的驱动打开并持有,这里拿不到也不需要它底下的连接。
// 远端 SQLite 在会话内只抢一次 .opskat.lock,由缓存的空闲驱逐/关闭统一释放;其会话底下的
// 连接数上限为 1,让并发面板操作排队复用同一条连接,而非各自重连抢锁、并发时硬失败。
//
// 返回的 cleanup 结束这次占用,操作用完会话后必须调用:在那之前缓存不会把会话当空闲的收走
// (一次操作可以比空闲时限长,见 panelConnCache.Hold)。
func (q *Query) getOrDialPanelDB(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.DatabaseConfig, password string) (dbdriver.Session, func() error, error) {
	key, err := panelDBCacheKey(asset.ID, cfg)
	if err != nil {
		return nil, nil, err
	}
	// 会话自己释放它占用的一切,没有单独的隧道 closer 要交给缓存。
	session, _, err := q.dbPanelCache.GetOrDial(asset.ID, key, func() (dbdriver.Session, io.Closer, error) {
		cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		session, err := connpool.OpenDatabase(ctx, asset, cfg, password, q.pool)
		return session, nil, err
	})
	if err != nil {
		return nil, nil, err
	}
	release := q.dbPanelCache.Hold(key)
	return session, func() error { release(); return nil }, nil
}

// panelDBCacheKey 返回面板缓存里这条会话的键。粒度由驱动声明：一条会话只连一个库的驱动
// 按「资产 + 库」缓存，一条会话覆盖整个资产的驱动（SQLite）按资产缓存。驱动未注册时返回
// 点名该驱动的 dbregistry.ErrDriverUnavailable。
func panelDBCacheKey(assetID int64, cfg *asset_entity.DatabaseConfig) (string, error) {
	driver, err := dbregistry.Lookup(cfg.Driver)
	if err != nil {
		return "", err
	}
	if !driver.SessionPerDatabase() {
		return fmt.Sprintf("%d", assetID), nil
	}
	return fmt.Sprintf("%d:%s", assetID, cfg.Database), nil
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
	session, err := connpool.OpenDatabase(ctx, testAsset, &cfg, password, q.pool)
	if err != nil {
		return err
	}
	if err := session.Close(); err != nil {
		logger.Ctx(ctx).Warn("close database session failed", zap.Error(err))
	}
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

	session, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接数据库失败: %w", err)
	}
	result, err := helper.ExecuteSQL(ctx, session, sqlText)
	if err := finishPanelDBOperation(err, cleanup); err != nil {
		return "", err
	}
	return result, nil
}

// tableImportTimeout 是导入整批执行的时限：整批在一次调用里执行完，不受资产的查询超时限制。
const tableImportTimeout = 30 * time.Minute

// ExecuteTableImport 由资产的驱动在会话的一条连接上执行导入的整批语句（复制模式、出错不继续、
// 忽略外键时由前端调用），返回新增 / 更新 / 删除 / 错误计数。语句里有用户的数据，不进日志。
func (q *Query) ExecuteTableImport(assetID int64, database string, request query_svc.TableImportBatchRequest) (*query_svc.TableImportBatchResult, error) {
	log := logger.Ctx(i18n.Ctx(q.ctx, q.lang.Lang())).With(
		zap.Int64("assetID", assetID), zap.String("database", database), zap.String("table", request.Table),
		zap.String("mode", request.Mode), zap.Int("statements", len(request.Statements)),
		zap.Bool("continueOnError", request.ContinueOnError), zap.Bool("disableForeignKeyChecks", request.DisableForeignKeyChecks))
	log.Info("table import batch start")
	start := time.Now()
	var result *query_svc.TableImportBatchResult
	err := q.withPanelSession(assetID, database, func(*asset_entity.DatabaseConfig) time.Duration { return tableImportTimeout },
		func(ctx context.Context, session dbdriver.Session, database string) error {
			var err error
			result, err = query_svc.RunTableImportBatch(ctx, session, database, request)
			return err
		})
	if err != nil {
		log.Error("table import batch failed", zap.Duration("elapsed", time.Since(start)), zap.Error(err))
		return nil, err
	}
	log.Info("table import batch done", zap.Duration("elapsed", time.Since(start)),
		zap.Int("processed", result.Processed), zap.Int("errors", result.Error), zap.Bool("rolledBack", result.RolledBack))
	return result, nil
}

// defaultTablePageSize 是打开表时没有给出每页行数（<= 0）时用的行数。
const defaultTablePageSize = 1000

// withTableSession 在资产的驱动会话上运行一次表浏览（打开表 / 取一页 / 计数），并记录它的
// 开始、结束与失败。筛选条件里有用户的数据，不进日志。
func (q *Query) withTableSession(op string, assetID int64, database, table string, fn func(ctx context.Context, session dbdriver.Session, database string) error) error {
	log := logger.Ctx(i18n.Ctx(q.ctx, q.lang.Lang())).With(
		zap.String("op", op), zap.Int64("assetID", assetID), zap.String("database", database), zap.String("table", table))
	log.Debug("table browse start")
	start := time.Now()
	if err := q.withObjectSession(assetID, database, fn); err != nil {
		log.Error("table browse failed", zap.Duration("elapsed", time.Since(start)), zap.Error(err))
		return err
	}
	log.Debug("table browse done", zap.Duration("elapsed", time.Since(start)))
	return nil
}

// OpenTable 一次性返回打开数据表所需的首屏数据：列、类型、列约束、主键、总行数与第一页。
// 由资产的驱动在自己的会话上完成。
func (q *Query) OpenTable(assetID int64, database, table string, pageSize int) (string, error) {
	if pageSize <= 0 {
		pageSize = defaultTablePageSize
	}
	var payload []byte
	err := q.withTableSession("open", assetID, database, table, func(ctx context.Context, session dbdriver.Session, database string) error {
		opened, err := session.OpenTable(ctx, database, table, pageSize)
		if err != nil {
			return err
		}
		payload, err = json.Marshal(query_svc.NewOpenTableResult(opened, pageSize))
		if err != nil {
			return fmt.Errorf("序列化结果失败: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

// QueryTableRows 按结构化的筛选条件与排序项取表里的一页。请求里没有 SQL：怎么把条件变成
// 查询由资产的驱动决定。
func (q *Query) QueryTableRows(assetID int64, database, table string, request query_svc.TableRowsRequest) (*query_svc.TableRows, error) {
	var result *query_svc.TableRows
	err := q.withTableSession("rows", assetID, database, table, func(ctx context.Context, session dbdriver.Session, database string) error {
		rowSet, err := session.TableRows(ctx, database, table,
			dbdriver.TableQuery{Filters: request.Filters, Sorts: request.Sorts},
			dbdriver.Page{Index: request.Page, Size: request.PageSize})
		if err != nil {
			return err
		}
		result = query_svc.NewTableRows(rowSet)
		return nil
	})
	return result, err
}

// CountTableRows 返回表里符合筛选条件的行数；没有条件时是表的总行数。
func (q *Query) CountTableRows(assetID int64, database, table string, filters []dbdriver.FilterItem) (int64, error) {
	var total int64
	err := q.withTableSession("count", assetID, database, table, func(ctx context.Context, session dbdriver.Session, database string) error {
		count, err := session.CountTableRows(ctx, database, table, filters)
		total = count
		return err
	})
	return total, err
}

// ExecuteSQLPaged 在指定数据库资产上执行分页 SQL 查询；怎么分页与计数由资产的驱动决定
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

	session, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return "", fmt.Errorf("连接数据库失败: %w", err)
	}
	result, err := helper.ExecuteSQLPaged(ctx, session, sqlText, page, pageSize)
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
