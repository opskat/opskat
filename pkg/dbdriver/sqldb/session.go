// Package sqldb 是建在 database/sql 之上的驱动共用的会话实现：语句怎么执行、结果怎么
// 取成结构化的值、连接怎么确认可用与关闭。方言相关的部分（分页与计数语句）由各驱动
// 经 Dialect 给出。
//
// 它属于驱动一侧：只有驱动包引用它来实现 dbdriver.Session，共享代码不引用它。
package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// Dialect 是分页执行里随方言变化的部分。传入的 query 已去掉末尾分号。
type Dialect interface {
	// CountSQL 返回统计 query 总行数的语句。
	CountSQL(query string) string
	// PagedSQL 返回只取 query 第 offset 行起 limit 行的语句。
	PagedSQL(query string, offset, limit int) string
}

// LimitOffset 是用子查询加 LIMIT / OFFSET 分页的方言。
type LimitOffset struct{}

func (LimitOffset) CountSQL(query string) string {
	return fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS _t", query)
}

func (LimitOffset) PagedSQL(query string, offset, limit int) string {
	return fmt.Sprintf("SELECT * FROM (%s) AS _t LIMIT %d OFFSET %d", query, limit, offset)
}

// Session 用一个 *sql.DB 实现 dbdriver.Session。
type Session struct {
	db      *sql.DB
	dialect Dialect
	release []io.Closer
}

var _ dbdriver.Session = (*Session)(nil)

// NewSession 把已确认可用的 db 包成会话。release 是 db 所依赖、要在它之后释放的东西
// （远端文件系统之类），Close 时按给出的顺序在 db 关闭之后逐个关闭。
func NewSession(db *sql.DB, dialect Dialect, release ...io.Closer) *Session {
	return &Session{db: db, dialect: dialect, release: release}
}

// Establish 确认刚打开的 db 可用：先 ping，再做驱动给的连接级只读设置（readOnly 为 nil
// 即不需要）。ping 必须在前，连通性检查才受 ctx 的超时保护。任何一步失败都会关闭 db。
func Establish(ctx context.Context, db *sql.DB, readOnly func(context.Context, *sql.DB) error) error {
	if err := db.PingContext(ctx); err != nil {
		closeAfterFailure(ctx, db)
		return fmt.Errorf("数据库连接失败: %w", err)
	}
	if readOnly == nil {
		return nil
	}
	if err := readOnly(ctx, db); err != nil {
		closeAfterFailure(ctx, db)
		return fmt.Errorf("设置只读模式失败: %w", err)
	}
	return nil
}

func closeAfterFailure(ctx context.Context, db *sql.DB) {
	if err := db.Close(); err != nil {
		logger.Ctx(ctx).Warn("close db", zap.Error(err))
	}
}

// Execute 原样执行一条语句：返回结果集的语句按查询执行，其余给出受影响行数。
func (s *Session) Execute(ctx context.Context, statement string) (*dbdriver.Result, error) {
	if !isQueryStatement(normalize(statement)) {
		result, err := s.db.ExecContext(ctx, statement)
		if err != nil {
			return nil, fmt.Errorf("SQL execution failed: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			// 有的驱动对某些语句给不出受影响行数；语句本身已经执行成功。
			logger.Ctx(ctx).Warn("get rows affected", zap.Error(err))
		}
		return &dbdriver.Result{AffectedRows: affected}, nil
	}
	rowSet, err := s.query(ctx, statement)
	if err != nil {
		return nil, err
	}
	return &dbdriver.Result{Rows: rowSet}, nil
}

// ExecutePaged 对 SELECT / WITH 先按方言计数、再按方言取一页；其余语句照 Execute 执行。
func (s *Session) ExecutePaged(ctx context.Context, statement string, page dbdriver.Page) (*dbdriver.Result, error) {
	if !isPageableQuery(normalize(statement)) {
		return s.Execute(ctx, statement)
	}
	query := stripTrailingSemicolon(statement)

	var total int64
	if err := s.db.QueryRowContext(ctx, s.dialect.CountSQL(query)).Scan(&total); err != nil {
		return nil, fmt.Errorf("count query failed: %w", err)
	}
	rowSet, err := s.query(ctx, s.dialect.PagedSQL(query, page.Index*page.Size, page.Size))
	if err != nil {
		return nil, err
	}
	rowSet.Total = &total
	return &dbdriver.Result{Rows: rowSet}, nil
}

// Close 先关闭 db，再按顺序释放它所依赖的东西；每一步都执行，失败一并返回。
func (s *Session) Close() error {
	errs := make([]error, 0, 1+len(s.release))
	errs = append(errs, s.db.Close())
	for _, r := range s.release {
		errs = append(errs, r.Close())
	}
	return errors.Join(errs...)
}

func (s *Session) query(ctx context.Context, statement string) (rowSet *dbdriver.RowSet, err error) {
	rows, err := s.db.QueryContext(ctx, statement)
	if err != nil {
		return nil, fmt.Errorf("SQL query failed: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			logger.Ctx(ctx).Warn("close SQL rows", zap.Error(closeErr))
		}
	}()
	return scanRows(rows)
}

func scanRows(rows *sql.Rows) (*dbdriver.RowSet, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	rowSet := &dbdriver.RowSet{Columns: columns}
	for rows.Next() {
		values, err := scanRow(rows, len(columns))
		if err != nil {
			return nil, err
		}
		rowSet.Rows = append(rowSet.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rowSet, nil
}

// scanRow 取出游标当前行；[]byte 转成字符串，值才能直接序列化成 JSON。
func scanRow(rows *sql.Rows, width int) ([]any, error) {
	values := make([]any, width)
	ptrs := make([]any, width)
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	for i, val := range values {
		if b, ok := val.([]byte); ok {
			values[i] = string(b)
		}
	}
	return values, nil
}

func normalize(statement string) string {
	return strings.TrimSpace(strings.ToUpper(statement))
}

// isQueryStatement 判断（已转大写、去空白的）语句是否返回结果集。
func isQueryStatement(upper string) bool {
	return strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "DESCRIBE") ||
		strings.HasPrefix(upper, "DESC ") ||
		strings.HasPrefix(upper, "EXPLAIN") ||
		strings.HasPrefix(upper, "PRAGMA") ||
		strings.HasPrefix(upper, "WITH") // CTE
}

// isPageableQuery 判断语句能否包成子查询来分页：只有 SELECT / WITH。
func isPageableQuery(upper string) bool {
	return strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")
}

// stripTrailingSemicolon 去掉末尾分号，避免包成子查询后出错。
func stripTrailingSemicolon(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ";")
}

// ParseParams 把配置里 "k=v&k2=v2" 形式的额外连接参数拆成键值；同名键取第一个值。
// 解析不了的参数串视为没有参数。
func ParseParams(params string) map[string]string {
	m := make(map[string]string)
	values, err := url.ParseQuery(params)
	if err != nil {
		return m
	}
	for k, v := range values {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}
