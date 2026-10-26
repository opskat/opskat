package extreg

import (
	"context"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/pkg/dbdriver"
)

// extensionSession 是扩展驱动交给共享代码的会话：每个操作原样转给扩展里的会话，但操作的 ctx
// 挂在驱动的生命周期上，驱动被移除时执行中与排队的操作随之取消；驱动被移除之后，操作的结果
// 一律换成点名该驱动的「驱动不可用」。
type extensionSession struct {
	driver *extensionDriver
	inner  dbdriver.Session
	closed sync.Once
}

// do 跑一个转给扩展的操作。
func do[T any](s *extensionSession, ctx context.Context, op func(context.Context) (T, error)) (T, error) {
	ctx, done := s.driver.operation(ctx)
	defer done()
	if err := s.driver.removed(); err != nil {
		var zero T
		return zero, err
	}
	v, err := op(ctx)
	if gone := s.driver.removed(); gone != nil {
		return v, gone
	}
	return v, err
}

// closeInner 关闭扩展里的会话，只关一次：会话可能先被驱动的移除关掉，再被持有者 Close。
func (s *extensionSession) closeInner() {
	s.closed.Do(func() {
		if err := s.inner.Close(); err != nil {
			logger.Default().Warn("close extension database driver session",
				zap.String("extension", s.driver.ext), zap.String("driver", string(s.driver.desc.ID)), zap.Error(err))
		}
	})
}

func (s *extensionSession) Close() error {
	s.driver.mu.Lock()
	delete(s.driver.sessions, s)
	s.driver.mu.Unlock()
	s.closeInner()
	return nil
}

func (s *extensionSession) Execute(ctx context.Context, statement string) (*dbdriver.Result, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.Result, error) { return s.inner.Execute(ctx, statement) })
}

func (s *extensionSession) ExecutePaged(ctx context.Context, statement string, page dbdriver.Page) (*dbdriver.Result, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.Result, error) {
		return s.inner.ExecutePaged(ctx, statement, page)
	})
}

func (s *extensionSession) ListDatabases(ctx context.Context) ([]string, error) {
	return do(s, ctx, s.inner.ListDatabases)
}

func (s *extensionSession) ListTables(ctx context.Context, database string) ([]string, error) {
	return do(s, ctx, func(ctx context.Context) ([]string, error) { return s.inner.ListTables(ctx, database) })
}

func (s *extensionSession) TableMetadata(ctx context.Context, database, table string) (*dbdriver.TableMetadata, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.TableMetadata, error) {
		return s.inner.TableMetadata(ctx, database, table)
	})
}

func (s *extensionSession) ListObjects(ctx context.Context, database string) (*dbdriver.DatabaseObjects, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.DatabaseObjects, error) {
		return s.inner.ListObjects(ctx, database)
	})
}

func (s *extensionSession) ObjectSource(ctx context.Context, database, objType, name, table string) (string, error) {
	return do(s, ctx, func(ctx context.Context) (string, error) {
		return s.inner.ObjectSource(ctx, database, objType, name, table)
	})
}

func (s *extensionSession) OpenTable(ctx context.Context, database, table string, pageSize int) (*dbdriver.OpenedTable, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.OpenedTable, error) {
		return s.inner.OpenTable(ctx, database, table, pageSize)
	})
}

func (s *extensionSession) TableRows(ctx context.Context, database, table string, query dbdriver.TableQuery, page dbdriver.Page) (*dbdriver.RowSet, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.RowSet, error) {
		return s.inner.TableRows(ctx, database, table, query, page)
	})
}

func (s *extensionSession) CountTableRows(ctx context.Context, database, table string, filters []dbdriver.FilterItem) (int64, error) {
	return do(s, ctx, func(ctx context.Context) (int64, error) {
		return s.inner.CountTableRows(ctx, database, table, filters)
	})
}

// StreamTableRows 的游标跨多次调用读取，它的 ctx 活到游标关闭为止。
func (s *extensionSession) StreamTableRows(ctx context.Context, database, table string, query dbdriver.TableQuery) (dbdriver.RowStream, error) {
	ctx, done := s.driver.operation(ctx)
	if err := s.driver.removed(); err != nil {
		done()
		return nil, err
	}
	rows, err := s.inner.StreamTableRows(ctx, database, table, query)
	if gone := s.driver.removed(); gone != nil {
		if rows != nil {
			_ = rows.Close()
		}
		done()
		return nil, gone
	}
	if err != nil {
		done()
		return nil, err
	}
	return &extensionRowStream{RowStream: rows, driver: s.driver, done: done}, nil
}

func (s *extensionSession) TableDDL(ctx context.Context, database, table string) (string, error) {
	return do(s, ctx, func(ctx context.Context) (string, error) { return s.inner.TableDDL(ctx, database, table) })
}

func (s *extensionSession) StarterQuery(ctx context.Context, database, table string, limit int) (string, error) {
	return do(s, ctx, func(ctx context.Context) (string, error) {
		return s.inner.StarterQuery(ctx, database, table, limit)
	})
}

func (s *extensionSession) PrepareRowStatements(ctx context.Context, database, table string) (dbdriver.RowStatementWriter, error) {
	w, err := do(s, ctx, func(ctx context.Context) (dbdriver.RowStatementWriter, error) {
		return s.inner.PrepareRowStatements(ctx, database, table)
	})
	if err != nil {
		return nil, err
	}
	return &extensionRowWriter{inner: w, driver: s.driver}, nil
}

func (s *extensionSession) GridEditStatements(ctx context.Context, database, table string, req dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.GridEditStatements, error) {
		return s.inner.GridEditStatements(ctx, database, table, req)
	})
}

func (s *extensionSession) CreateTableStatements(ctx context.Context, database string, req dbdriver.CreateTableRequest) ([]string, error) {
	return do(s, ctx, func(ctx context.Context) ([]string, error) {
		return s.inner.CreateTableStatements(ctx, database, req)
	})
}

func (s *extensionSession) DropTableStatements(ctx context.Context, database, table string) ([]string, error) {
	return do(s, ctx, func(ctx context.Context) ([]string, error) {
		return s.inner.DropTableStatements(ctx, database, table)
	})
}

func (s *extensionSession) TruncateTableStatements(ctx context.Context, database, table string) ([]string, error) {
	return do(s, ctx, func(ctx context.Context) ([]string, error) {
		return s.inner.TruncateTableStatements(ctx, database, table)
	})
}

func (s *extensionSession) CreateDatabaseStatements(ctx context.Context, req dbdriver.CreateDatabaseRequest) ([]string, error) {
	return do(s, ctx, func(ctx context.Context) ([]string, error) {
		return s.inner.CreateDatabaseStatements(ctx, req)
	})
}

func (s *extensionSession) AlterTableModel(ctx context.Context, database, table string) (*dbdriver.AlterTableModel, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.AlterTableModel, error) {
		return s.inner.AlterTableModel(ctx, database, table)
	})
}

func (s *extensionSession) AlterTableStatements(ctx context.Context, database, table string, req dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.AlterTableStatements, error) {
		return s.inner.AlterTableStatements(ctx, database, table, req)
	})
}

func (s *extensionSession) ImportStatements(ctx context.Context, database, table string, req dbdriver.ImportRequest) ([]string, error) {
	return do(s, ctx, func(ctx context.Context) ([]string, error) {
		return s.inner.ImportStatements(ctx, database, table, req)
	})
}

func (s *extensionSession) ExecuteBatch(ctx context.Context, req dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	return do(s, ctx, func(ctx context.Context) (*dbdriver.BatchResult, error) { return s.inner.ExecuteBatch(ctx, req) })
}

// extensionRowStream 是扩展驱动的游标：驱动被移除后读取停止，Err 报驱动不可用。
type extensionRowStream struct {
	dbdriver.RowStream
	driver *extensionDriver
	done   func()
	closed sync.Once
}

func (r *extensionRowStream) Next() bool {
	return r.driver.removed() == nil && r.RowStream.Next()
}

func (r *extensionRowStream) Err() error {
	if gone := r.driver.removed(); gone != nil {
		return gone
	}
	return r.RowStream.Err()
}

func (r *extensionRowStream) Close() error {
	err := r.RowStream.Close()
	r.closed.Do(r.done)
	return err
}

// extensionRowWriter 是扩展驱动备好的行语句生成器：它在扩展里生成，驱动被移除后报驱动不可用。
type extensionRowWriter struct {
	inner  dbdriver.RowStatementWriter
	driver *extensionDriver
}

func (w *extensionRowWriter) RowStatements(req dbdriver.RowStatementsRequest) ([]string, error) {
	if err := w.driver.removed(); err != nil {
		return nil, err
	}
	stmts, err := w.inner.RowStatements(req)
	if gone := w.driver.removed(); gone != nil {
		return nil, gone
	}
	return stmts, err
}
