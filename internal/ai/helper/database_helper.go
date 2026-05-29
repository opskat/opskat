package helper

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/sshpool"
)

// --- Database 连接缓存 ---

type dbCacheKeyType struct{}

// DatabaseClientCache 在同一次 AI Send 中复用数据库连接
type DatabaseClientCache = ConnCache[*sql.DB]

// NewDatabaseClientCache 创建数据库连接缓存
func NewDatabaseClientCache() *DatabaseClientCache {
	return NewConnCache[*sql.DB]("database")
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

// --- Handler ---

func HandleExecSQL(ctx context.Context, args map[string]any) (string, error) {
	assetID := aictx.ArgInt64(args, "asset_id")
	sqlText := aictx.ArgString(args, "sql")
	if assetID == 0 || sqlText == "" {
		return "", fmt.Errorf("missing required parameters: asset_id, sql")
	}

	// 权限检查
	if checker := permission.GetPolicyChecker(ctx); checker != nil {
		result := checker.CheckForAsset(ctx, assetID, asset_entity.AssetTypeDatabase, sqlText)
		aictx.RecordDecision(ctx, result)
		if result.Decision != aictx.Allow {
			return result.Message, nil
		}
	}

	asset, err := asset_svc.Asset().Get(ctx, assetID)
	if err != nil {
		return "", fmt.Errorf("asset not found: %w", err)
	}
	if !asset.IsDatabase() {
		return "", fmt.Errorf("asset is not database type")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get database config: %w", err)
	}

	// 覆盖默认数据库
	if dbOverride := aictx.ArgString(args, "database"); dbOverride != "" {
		cfg.Database = dbOverride
	}

	db, closer, err := getOrDialDatabase(ctx, asset, cfg)
	if err != nil {
		return "", fmt.Errorf("failed to connect to database: %w", err)
	}
	// 如果不是缓存连接，使用后关闭
	if getDatabaseCache(ctx) == nil {
		if db != nil {
			defer func() {
				if err := db.Close(); err != nil {
					logger.Default().Warn("close database connection", zap.Error(err))
				}
			}()
		}
		if closer != nil {
			defer func() {
				if err := closer.Close(); err != nil {
					logger.Default().Warn("close database tunnel", zap.Error(err))
				}
			}()
		}
	}

	return ExecuteSQL(ctx, db, sqlText)
}

func getOrDialDatabase(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.DatabaseConfig) (*sql.DB, io.Closer, error) {
	dialFn := func() (*sql.DB, io.Closer, error) {
		password, err := credential_resolver.Default().ResolveDatabasePassword(ctx, cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve credentials: %w", err)
		}
		return connpool.DialDatabase(ctx, asset, cfg, password, getSSHPool(ctx))
	}
	if cache := getDatabaseCache(ctx); cache != nil {
		return cache.GetOrDial(asset.ID, dialFn)
	}
	return dialFn()
}

// ExecuteSQL 执行 SQL 并返回 JSON 结果
func ExecuteSQL(ctx context.Context, db *sql.DB, sqlText string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToUpper(sqlText))
	if isQueryStatement(trimmed) {
		rows, err := db.QueryContext(ctx, sqlText)
		if err != nil {
			return "", fmt.Errorf("SQL query failed: %w", err)
		}
		defer func() {
			if err := rows.Close(); err != nil {
				logger.Default().Warn("close SQL rows", zap.Error(err))
			}
		}()
		return formatRowsJSON(rows)
	}

	result, err := db.ExecContext(ctx, sqlText)
	if err != nil {
		return "", fmt.Errorf("SQL execution failed: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		logger.Default().Warn("get rows affected", zap.Error(err))
	}
	return fmt.Sprintf(`{"affected_rows":%d}`, affected), nil
}

// isPageableQuery 判断是否可以用子查询包装分页（仅 SELECT / WITH）
func isPageableQuery(upper string) bool {
	return strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")
}

// stripTrailingSemicolon 去掉末尾分号，避免子查询包装出错
func stripTrailingSemicolon(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ";")
}

// ExecuteSQLPaged 对 SELECT/WITH 语句进行子查询包装分页，其他语句走 ExecuteSQL
func ExecuteSQLPaged(ctx context.Context, db *sql.DB, sqlText string, page, pageSize int, driver asset_entity.DatabaseDriver) (string, error) {
	trimmed := strings.TrimSpace(strings.ToUpper(sqlText))

	// 非查询语句或不可分页的查询（SHOW/DESCRIBE/EXPLAIN）走原逻辑
	if !isQueryStatement(trimmed) || !isPageableQuery(trimmed) {
		return ExecuteSQL(ctx, db, sqlText)
	}

	cleanSQL := stripTrailingSemicolon(sqlText)
	offset := page * pageSize

	countSourceSQL := cleanSQL
	if driver == asset_entity.DriverMSSQL {
		countSourceSQL = stripTopLevelOrderBy(cleanSQL)
	}

	// 1. 获取总行数
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS _t", countSourceSQL) //nolint:gosec // SQL is user-provided and intentionally executed
	var totalCount int
	if err := db.QueryRowContext(ctx, countSQL).Scan(&totalCount); err != nil {
		return "", fmt.Errorf("count query failed: %w", err)
	}

	// 2. 分页查询：MSSQL 无 LIMIT，用 OFFSET/FETCH（需 ORDER BY，用 (SELECT NULL) 占位）
	pagedSQL := pagedQuerySQL(cleanSQL, offset, pageSize, driver)
	rows, err := db.QueryContext(ctx, pagedSQL)
	if err != nil {
		return "", fmt.Errorf("SQL query failed: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			logger.Default().Warn("close SQL rows", zap.Error(err))
		}
	}()

	return formatRowsPagedJSON(rows, totalCount)
}

// pagedQuerySQL 把已去分号的查询包成分页子查询。MSSQL 不支持 LIMIT/OFFSET，
// 必须用 OFFSET ... ROWS FETCH NEXT ... ROWS ONLY，且该语法要求 ORDER BY，
// 这里用 ORDER BY (SELECT NULL) 占位（不强加排序键）。其他 driver 保持 LIMIT/OFFSET。
func pagedQuerySQL(cleanSQL string, offset, pageSize int, driver asset_entity.DatabaseDriver) string {
	if driver == asset_entity.DriverMSSQL {
		if hasTopLevelOrderBy(cleanSQL) {
			return fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", cleanSQL, offset, pageSize)
		}
		return fmt.Sprintf(
			"SELECT * FROM (%s) AS _t ORDER BY (SELECT NULL) OFFSET %d ROWS FETCH NEXT %d ROWS ONLY",
			cleanSQL, offset, pageSize)
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS _t LIMIT %d OFFSET %d", cleanSQL, pageSize, offset)
}

func stripTopLevelOrderBy(sqlText string) string {
	if idx := topLevelOrderByIndex(sqlText); idx >= 0 {
		return strings.TrimSpace(sqlText[:idx])
	}
	return sqlText
}

func hasTopLevelOrderBy(sqlText string) bool {
	return topLevelOrderByIndex(sqlText) >= 0
}

func topLevelOrderByIndex(sqlText string) int {
	upper := strings.ToUpper(sqlText)
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	inBracketQuote := false

	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]
		if inSingleQuote {
			if ch == '\'' {
				if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
					i++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			if ch == '"' {
				if i+1 < len(sqlText) && sqlText[i+1] == '"' {
					i++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if inBracketQuote {
			if ch == ']' {
				if i+1 < len(sqlText) && sqlText[i+1] == ']' {
					i++
					continue
				}
				inBracketQuote = false
			}
			continue
		}

		switch ch {
		case '\'':
			inSingleQuote = true
		case '"':
			inDoubleQuote = true
		case '[':
			inBracketQuote = true
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && strings.HasPrefix(upper[i:], "ORDER") && isSQLWordBoundary(sqlText, i-1) {
				afterOrder := i + len("ORDER")
				if afterOrder < len(sqlText) && isSQLSpace(sqlText[afterOrder]) {
					j := afterOrder
					for j < len(sqlText) && isSQLSpace(sqlText[j]) {
						j++
					}
					if strings.HasPrefix(upper[j:], "BY") && isSQLWordBoundary(sqlText, j+len("BY")) {
						return i
					}
				}
			}
		}
	}
	return -1
}

func isSQLWordBoundary(sqlText string, idx int) bool {
	if idx < 0 || idx >= len(sqlText) {
		return true
	}
	ch := sqlText[idx]
	return ch != '_' && (ch < '0' || ch > '9') && (ch < 'A' || ch > 'Z') && (ch < 'a' || ch > 'z')
}

func isSQLSpace(ch byte) bool {
	return ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t' || ch == '\f'
}

func formatRowsPagedJSON(rows *sql.Rows, totalCount int) (string, error) {
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}

	var resultRows []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			row[col] = val
		}
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	data, err := json.Marshal(map[string]any{
		"columns":     columns,
		"rows":        resultRows,
		"count":       len(resultRows),
		"total_count": totalCount,
	})
	if err != nil {
		logger.Default().Error("marshal query result", zap.Error(err))
		return "", fmt.Errorf("failed to marshal query result: %w", err)
	}
	return string(data), nil
}

func isQueryStatement(upper string) bool {
	return strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC ") ||
		strings.HasPrefix(upper, "EXPLAIN") ||
		strings.HasPrefix(upper, "PRAGMA") ||
		strings.HasPrefix(upper, "WITH") // CTE
}

func formatRowsJSON(rows *sql.Rows) (string, error) {
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}

	var resultRows []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			val := values[i]
			// 将 []byte 转为 string
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			row[col] = val
		}
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	data, err := json.Marshal(map[string]any{
		"columns": columns,
		"rows":    resultRows,
		"count":   len(resultRows),
	})
	if err != nil {
		logger.Default().Error("marshal query result", zap.Error(err))
		return "", fmt.Errorf("failed to marshal query result: %w", err)
	}
	return string(data), nil
}
