package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// ForeignKeyChecks 是方言里「怎么在一条连接上暂时关闭外键检查」。实现它的方言才能在整批执行时
// 关闭外键检查；没实现的，Session.ExecuteBatch 以 dbdriver.ErrUnsupported 拒绝。驱动描述的
// CanDisableForeignKeyChecks 与它一起声明。
type ForeignKeyChecks interface {
	// ForeignKeyChecksSQL 在批的那条连接上（事务之外）给出关闭外键检查的语句，与把它恢复成关闭之前
	// 那样的语句：检查是连接级开关的方言先查这条连接现在的状态，本来就没在检查时两条都返回空串
	// （不关闭、也不「恢复」成检查）。database / table 是批写入的表，含义同 dbdriver.BatchRequest
	// （按表停用约束的方言用它）。
	ForeignKeyChecksSQL(ctx context.Context, conn *sql.Conn, database, table string) (disable, restore string, err error)
}

// restoreTimeout 是恢复外键检查的时限。恢复不随批的 ctx 取消：批超时或被取消时也要恢复。
const restoreTimeout = 30 * time.Second

// execer 是连接与事务共有的执行语句的方法。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ExecuteBatch 在会话的一条独立连接上执行整批语句：关闭外键检查（若要求）→ 开始事务（若
// Atomic）→ 逐条执行 → 提交或回滚 → 恢复外键检查。
func (s *Session) ExecuteBatch(ctx context.Context, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	var checks ForeignKeyChecks
	if req.DisableForeignKeyChecks {
		var ok bool
		if checks, ok = s.dialect.(ForeignKeyChecks); !ok {
			return nil, fmt.Errorf("%w: this driver cannot disable foreign key checks", dbdriver.ErrUnsupported)
		}
		if req.Table == "" {
			return nil, errors.New("disabling foreign key checks needs the table the batch writes into")
		}
	}
	return onConn(ctx, s, func(conn *sql.Conn) (result *dbdriver.BatchResult, err error) {
		var disable, restore string
		if checks != nil {
			var queryErr error
			if disable, restore, queryErr = checks.ForeignKeyChecksSQL(ctx, conn, req.Database, req.Table); queryErr != nil {
				return nil, fmt.Errorf("query foreign key checks: %w", queryErr)
			}
		}
		if disable != "" {
			if _, err := conn.ExecContext(ctx, disable); err != nil {
				return nil, fmt.Errorf("disable foreign key checks: %w", err)
			}
		}
		if restore != "" {
			defer func() {
				restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
				defer cancel()
				if _, restoreErr := conn.ExecContext(restoreCtx, restore); restoreErr != nil {
					err = errors.Join(err, fmt.Errorf("restore foreign key checks: %w", restoreErr))
				}
			}()
		}
		if !req.Atomic {
			return runStatements(ctx, conn, req)
		}
		return runInTransaction(ctx, conn, req)
	})
}

// runInTransaction 在一个事务里执行整批：全部成功才提交，有一条失败就回滚。
func runInTransaction(ctx context.Context, conn *sql.Conn, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	result, err := runStatements(ctx, tx, req)
	if err != nil || result.Failed() {
		// ctx 被取消时 database/sql 已经自己回滚了事务，再回滚得到的是 ErrTxDone。
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return result, errors.Join(err, fmt.Errorf("rollback transaction: %w", rbErr))
		}
		result.RolledBack = true
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit transaction: %w", err)
	}
	return result, nil
}

// runStatements 按次序执行整批语句，跳过空白语句。Atomic 或不继续时停在第一条失败的语句；
// ctx 被取消时停下并返回它的错误，不再逐条撞上已经取消的 ctx。
func runStatements(ctx context.Context, exec execer, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	result := &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{}}
	for index, statement := range req.Statements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		res, err := exec.ExecContext(ctx, statement)
		if err != nil {
			result.Executed = append(result.Executed, dbdriver.BatchStatement{Index: index, Error: err.Error()})
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, fmt.Errorf("batch interrupted: %w", ctxErr)
			}
			if req.Atomic || !req.ContinueOnError {
				break
			}
			continue
		}
		affected, err := res.RowsAffected()
		if err != nil {
			// 有的驱动对某些语句给不出受影响行数；语句本身已经执行成功。
			logger.Ctx(ctx).Warn("get rows affected", zap.Error(err))
		}
		result.Executed = append(result.Executed, dbdriver.BatchStatement{Index: index, AffectedRows: affected})
	}
	return result, nil
}
