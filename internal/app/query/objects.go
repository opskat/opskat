package query

import (
	"context"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/query_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// defaultQueryTimeout 数据库元数据浏览 / SQL 查询在资产未配置 QueryTimeoutSeconds 时的默认超时。
const defaultQueryTimeout = 30 * time.Second

// queryTimeout 归一化资产的查询超时：配置了正值即用之，否则回退默认值。
func queryTimeout(cfg *asset_entity.DatabaseConfig) time.Duration {
	if cfg.QueryTimeoutSeconds > 0 {
		return time.Duration(cfg.QueryTimeoutSeconds) * time.Second
	}
	return defaultQueryTimeout
}

// withObjectSession resolves a database asset, dials (or reuses) the panel session and runs fn
// on it. database 非空时覆盖资产配置的库；fn 拿到的 database 是最终生效的库名。
// 超时取自资产的 QueryTimeoutSeconds（未配置回退 defaultQueryTimeout）。
func (q *Query) withObjectSession(assetID int64, database string, fn func(ctx context.Context, session dbdriver.Session, database string) error) error {
	return q.withPanelSession(assetID, database, queryTimeout, fn)
}

// withPanelSession 同 withObjectSession，超时由 timeout 按资产配置给出（导出整张表这类长操作
// 不受查询超时限制）。
func (q *Query) withPanelSession(assetID int64, database string, timeout func(*asset_entity.DatabaseConfig) time.Duration, fn func(ctx context.Context, session dbdriver.Session, database string) error) error {
	panel, err := q.acquirePanelSession(assetID, database, timeout)
	if err != nil {
		return err
	}
	return panel.finish(fn(panel.ctx, panel.session, panel.database))
}

// panelSession 是一次占用中的面板会话：解析好的资产、带时限的 ctx，以及用完要调的 finish。
// 一次调用内用完的操作经 withPanelSession；要跨多次调用持有会话的（导出游标）自己调
// acquirePanelSession，并负责 finish。
type panelSession struct {
	ctx      context.Context
	cancel   context.CancelFunc
	session  dbdriver.Session
	database string
	cleanup  func() error
}

// finish 以操作的结果 opErr 结束这次占用：释放连接并撤销 ctx，释放失败与 opErr 一并返回。
func (p *panelSession) finish(opErr error) error {
	defer p.cancel()
	return finishPanelDBOperation(opErr, p.cleanup)
}

// acquirePanelSession 解析数据库资产，拨号（或复用）面板会话。database 非空时覆盖资产配置的
// 库；返回的 database 是最终生效的库名。
func (q *Query) acquirePanelSession(assetID int64, database string, timeout func(*asset_entity.DatabaseConfig) time.Duration) (*panelSession, error) {
	ctx0 := i18n.Ctx(q.ctx, q.lang.Lang())
	asset, err := asset_svc.Asset().Get(ctx0, assetID)
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
	password, err := credential_resolver.Default().ResolveDatabasePassword(ctx0, cfg)
	if err != nil {
		return nil, fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx0, timeout(cfg))
	session, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	return &panelSession{ctx: ctx, cancel: cancel, session: session, database: cfg.Database, cleanup: cleanup}, nil
}

// nameList 是交给前端的名字列表：驱动一个名字也没给时是空列表（序列化成 []，不是 null），
// 前端直接遍历它。
func nameList(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
}

// ListDatabases returns the databases the asset's driver offers for browsing.
func (q *Query) ListDatabases(assetID int64) ([]string, error) {
	var result []string
	err := q.withObjectSession(assetID, "", func(ctx context.Context, session dbdriver.Session, _ string) error {
		names, err := session.ListDatabases(ctx)
		if err != nil {
			return err
		}
		result = nameList(names)
		return nil
	})
	return result, err
}

// ListTables returns the tables and views of a database (schema-qualified names where the driver groups by schema).
func (q *Query) ListTables(assetID int64, database string) ([]string, error) {
	var result []string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		names, err := session.ListTables(ctx, database)
		if err != nil {
			return err
		}
		result = nameList(names)
		return nil
	})
	return result, err
}

// GetTableMetadata returns columns / indexes / foreign keys for a table.
func (q *Query) GetTableMetadata(assetID int64, database, table string) (*query_svc.TableMetadata, error) {
	var result *query_svc.TableMetadata
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		meta, err := session.TableMetadata(ctx, database, table)
		if err != nil {
			return err
		}
		result = query_svc.NewTableMetadata(meta)
		return nil
	})
	return result, err
}

// ListDatabaseObjects returns views / procedures / functions / triggers.
func (q *Query) ListDatabaseObjects(assetID int64, database string) (*query_svc.DatabaseObjects, error) {
	var result *query_svc.DatabaseObjects
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		objs, err := session.ListObjects(ctx, database)
		if err != nil {
			return err
		}
		result = query_svc.NewDatabaseObjects(objs)
		return nil
	})
	return result, err
}

// GetObjectSource returns the read-only definition of a view / routine / trigger.
// table is only used where ObjectMeta.Table is set (PostgreSQL triggers).
func (q *Query) GetObjectSource(assetID int64, database, objType, name, table string) (string, error) {
	var result string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		src, err := session.ObjectSource(ctx, database, objType, name, table)
		result = src
		return err
	})
	return result, err
}

// GetTableDDL returns the CREATE statement of a table (empty when the table does not exist).
// 驱动没有声明查看建表语句时返回 dbdriver.ErrUnsupported。
func (q *Query) GetTableDDL(assetID int64, database, table string) (string, error) {
	var result string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		ddl, err := session.TableDDL(ctx, database, table)
		result = ddl
		return err
	})
	return result, err
}

// GetStarterQuery returns the SQL editor's starter statement for a table (its first limit rows),
// written in the asset's dialect by the driver.
func (q *Query) GetStarterQuery(assetID int64, database, table string, limit int) (string, error) {
	var result string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		statement, err := session.StarterQuery(ctx, database, table, limit)
		result = statement
		return err
	})
	return result, err
}

// GetGridEditStatements returns the statements that apply the grid's pending edits — one INSERT per
// new row, one single-row UPDATE per edited row, one single-row DELETE per deleted row — generated
// by the driver in the asset's dialect. Nothing is executed: the frontend previews them and then
// executes exactly these. 驱动没有声明表格编辑时返回 dbdriver.ErrUnsupported。
func (q *Query) GetGridEditStatements(assetID int64, database, table string, request dbdriver.GridEditRequest) (*dbdriver.GridEditStatements, error) {
	var result *dbdriver.GridEditStatements
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		statements, err := session.GridEditStatements(ctx, database, table, request)
		result = statements
		return err
	})
	return result, err
}

// GetCreateTableStatements returns the statement that creates a table, written by the driver in the
// asset's dialect. Nothing is executed: the frontend previews it and then executes exactly it.
// 驱动没有声明建表时返回 dbdriver.ErrUnsupported。
func (q *Query) GetCreateTableStatements(assetID int64, database string, request dbdriver.CreateTableRequest) ([]string, error) {
	var result []string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		statements, err := session.CreateTableStatements(ctx, database, request)
		result = statements
		return err
	})
	return result, err
}

// GetDropTableStatements returns the statement that drops a table. Nothing is executed.
// 驱动没有声明删表时返回 dbdriver.ErrUnsupported。
func (q *Query) GetDropTableStatements(assetID int64, database, table string) ([]string, error) {
	var result []string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		statements, err := session.DropTableStatements(ctx, database, table)
		result = statements
		return err
	})
	return result, err
}

// GetTruncateTableStatements returns the statement that empties a table (DELETE FROM where the
// dialect has no TRUNCATE). Nothing is executed. 驱动没有声明清空表时返回 dbdriver.ErrUnsupported。
func (q *Query) GetTruncateTableStatements(assetID int64, database, table string) ([]string, error) {
	var result []string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		statements, err := session.TruncateTableStatements(ctx, database, table)
		result = statements
		return err
	})
	return result, err
}

// GetCreateDatabaseStatements returns the statement that creates a database with the options the
// driver declares; database is the one the connection is opened on. Nothing is executed.
// 驱动没有声明新建数据库时返回 dbdriver.ErrUnsupported。
func (q *Query) GetCreateDatabaseStatements(assetID int64, database string, request dbdriver.CreateDatabaseRequest) ([]string, error) {
	var result []string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, _ string) error {
		statements, err := session.CreateDatabaseStatements(ctx, request)
		result = statements
		return err
	})
	return result, err
}

// GetAlterTableModel returns the table as the alter table dialog shows it — its name without the
// schema, its comment and its columns — read by the driver. 驱动没有声明改表时返回 dbdriver.ErrUnsupported。
func (q *Query) GetAlterTableModel(assetID int64, database, table string) (*dbdriver.AlterTableModel, error) {
	var result *dbdriver.AlterTableModel
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		model, err := session.AlterTableModel(ctx, database, table)
		result = model
		return err
	})
	return result, err
}

// GetAlterTableStatements returns the statements that alter a table into the requested shape,
// generated by the driver against the table's current definition. Nothing is executed: the frontend
// previews them and then executes exactly them, in order. 驱动没有声明改表时返回 dbdriver.ErrUnsupported。
func (q *Query) GetAlterTableStatements(assetID int64, database, table string, request dbdriver.AlterTableRequest) (*dbdriver.AlterTableStatements, error) {
	var result *dbdriver.AlterTableStatements
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		statements, err := session.AlterTableStatements(ctx, database, table, request)
		result = statements
		return err
	})
	return result, err
}

// GetImportStatements returns the statements that import the mapped, already converted rows of a
// source file into a table in the requested mode, generated by the driver in the asset's dialect
// (upsert / skip / copy forms, literals, identity handling). Nothing is executed: the import
// dialog shows them and then executes exactly these. 驱动没有声明导入时返回 dbdriver.ErrUnsupported。
func (q *Query) GetImportStatements(assetID int64, database, table string, request dbdriver.ImportRequest) ([]string, error) {
	var result []string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		statements, err := session.ImportStatements(ctx, database, table, request)
		result = statements
		return err
	})
	return result, err
}

// GetRowStatements returns the statements that write the given rows into a table — one INSERT,
// or one single-row UPDATE, per row (copy / export as SQL) — generated by the driver in the asset's
// dialect. Nothing is executed. 驱动没有声明导出 / 复制为 SQL 时返回 dbdriver.ErrUnsupported。
func (q *Query) GetRowStatements(assetID int64, database, table string, request dbdriver.RowStatementsRequest) ([]string, error) {
	var result []string
	err := q.withObjectSession(assetID, database, func(ctx context.Context, session dbdriver.Session, database string) error {
		writer, err := session.PrepareRowStatements(ctx, database, table)
		if err != nil {
			return err
		}
		result, err = writer.RowStatements(request)
		return err
	})
	return result, err
}
