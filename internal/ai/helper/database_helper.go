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
	"github.com/opskat/opskat/internal/sshpool"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// --- Database 连接缓存 ---

type dbCacheKeyType struct{}

// DatabaseClientCache 在同一次 AI Send 中复用数据库会话
type DatabaseClientCache = ConnCache[dbdriver.Session]

// NewDatabaseClientCache 创建数据库会话缓存
func NewDatabaseClientCache() *DatabaseClientCache {
	return NewConnCache[dbdriver.Session]("database")
}

// WithDatabaseCache 将数据库缓存注入 context
func WithDatabaseCache(ctx context.Context, cache *DatabaseClientCache) context.Context {
	return context.WithValue(ctx, dbCacheKeyType{}, cache)
}

func getDatabaseCache(ctx context.Context) *DatabaseClientCache {
	if cache, ok := ctx.Value(dbCacheKeyType{}).(*DatabaseClientCache); ok {
		return cache
	}
	return nil
}

// --- SSH Pool context ---

type sshPoolKeyType struct{}

// WithSSHPool 将 SSH 连接池注入 context（供 connpool 隧道使用）
func WithSSHPool(ctx context.Context, pool *sshpool.Pool) context.Context {
	return context.WithValue(ctx, sshPoolKeyType{}, pool)
}

func getSSHPool(ctx context.Context) *sshpool.Pool {
	if pool, ok := ctx.Value(sshPoolKeyType{}).(*sshpool.Pool); ok {
		return pool
	}
	return nil
}

// --- Executor ---

// ExecSQLOnAsset 是不含权限检查的纯执行入口：权限检查由调用方（统一 exec 工具的
// handleExec、batch_exec 的预检）在调用之前完成，这里只负责连接与执行——两者都经资产
// 配置的驱动。scope 非空时覆盖资产默认配置的数据库名。
func ExecSQLOnAsset(ctx context.Context, asset *asset_entity.Asset, sqlText, scope string) (string, error) {
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get database config: %w", err)
	}

	// 覆盖默认数据库
	if scope != "" {
		cfg.Database = scope
	}

	session, err := getOrOpenDatabase(ctx, asset, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to connect to database: %w", err)
	}
	// 如果不是缓存会话，使用后关闭
	if getDatabaseCache(ctx) == nil {
		defer func() {
			if err := session.Close(); err != nil {
				logger.Ctx(ctx).Warn("close database session", zap.Int64("assetID", asset.ID), zap.Error(err))
			}
		}()
	}

	return ExecuteSQL(ctx, session, sqlText)
}

func getOrOpenDatabase(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.DatabaseConfig) (dbdriver.Session, error) {
	open := func() (dbdriver.Session, error) {
		password, err := credential_resolver.Default().ResolveDatabasePassword(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve credentials: %w", err)
		}
		cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		return connpool.OpenDatabase(ctx, asset, cfg, password, getSSHPool(ctx))
	}
	cache := getDatabaseCache(ctx)
	if cache == nil {
		return open()
	}
	// 会话自己释放它占用的一切，没有单独的隧道 closer 要交给缓存。
	session, _, err := cache.GetOrDial(asset.ID, func() (dbdriver.Session, io.Closer, error) {
		session, err := open()
		return session, nil, err
	})
	return session, err
}

// ExecuteSQL 在会话上执行 SQL 并返回 JSON 结果。执行失败时原样返回驱动的错误。
func ExecuteSQL(ctx context.Context, session dbdriver.Session, sqlText string) (string, error) {
	result, err := session.Execute(ctx, sqlText)
	if err != nil {
		return "", err
	}
	return formatSQLResult(ctx, result)
}

// ExecuteSQLPaged 在会话上分页执行 SQL 并返回 JSON 结果。page 从 0 起。怎么分页、怎么
// 计数由驱动决定；驱动没有分页的语句（非查询等）结果里不带 total_count。
func ExecuteSQLPaged(ctx context.Context, session dbdriver.Session, sqlText string, page, pageSize int) (string, error) {
	result, err := session.ExecutePaged(ctx, sqlText, dbdriver.Page{Index: page, Size: pageSize})
	if err != nil {
		return "", err
	}
	return formatSQLResult(ctx, result)
}

// formatSQLResult 把驱动给的结构化结果排成对外的 JSON：
//
//	结果集   {"columns":[...],"rows":[{列名:值,...},...],"count":N}，分页时多一个 "total_count"
//	无结果集 {"affected_rows":N}
func formatSQLResult(ctx context.Context, result *dbdriver.Result) (string, error) {
	if result.Rows == nil {
		return fmt.Sprintf(`{"affected_rows":%d}`, result.AffectedRows), nil
	}
	rowSet := result.Rows
	var rows []map[string]any
	for _, values := range rowSet.Rows {
		row := make(map[string]any, len(rowSet.Columns))
		for i, col := range rowSet.Columns {
			row[col] = values[i]
		}
		rows = append(rows, row)
	}
	payload := map[string]any{
		"columns": rowSet.Columns,
		"rows":    rows,
		"count":   len(rows),
	}
	if rowSet.Total != nil {
		payload["total_count"] = *rowSet.Total
	}
	data, err := json.Marshal(payload)
	if err != nil {
		logger.Ctx(ctx).Error("marshal query result", zap.Error(err))
		return "", fmt.Errorf("failed to marshal query result: %w", err)
	}
	return string(data), nil
}
