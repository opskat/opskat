package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/service/query_svc"
)

// objectConn bundles a live session with the resolved driver/database so the
// object-browser bindings can call into query_svc uniformly.
type objectConn struct {
	conn     *sql.Conn
	driver   string
	database string
}

func (o objectConn) driverType() asset_entity.DatabaseDriver {
	return asset_entity.DatabaseDriver(o.driver)
}

// defaultQueryTimeout 数据库元数据浏览 / SQL 查询在资产未配置 QueryTimeoutSeconds 时的默认超时。
const defaultQueryTimeout = 30 * time.Second

// queryTimeout 归一化资产的查询超时：配置了正值即用之，否则回退默认值。
func queryTimeout(cfg *asset_entity.DatabaseConfig) time.Duration {
	if cfg.QueryTimeoutSeconds > 0 {
		return time.Duration(cfg.QueryTimeoutSeconds) * time.Second
	}
	return defaultQueryTimeout
}

// withDatabaseConn resolves a database asset, dials (or reuses) a panel
// connection and runs fn on a fresh *sql.Conn, handling cleanup ordering.
// 超时取自资产的 QueryTimeoutSeconds（未配置回退 defaultQueryTimeout）。
func (q *Query) withObjectConn(assetID int64, database string, fn func(ctx context.Context, conn objectConn) error) error {
	ctx0 := i18n.Ctx(q.ctx, q.lang.Lang())
	asset, err := asset_svc.Asset().Get(ctx0, assetID)
	if err != nil {
		return fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsDatabase() {
		return fmt.Errorf("资产不是数据库类型")
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return fmt.Errorf("获取数据库配置失败: %w", err)
	}
	if database != "" {
		cfg.Database = database
	}
	password, err := credential_resolver.Default().ResolveDatabasePassword(ctx0, cfg)
	if err != nil {
		return fmt.Errorf("解析凭据失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx0, queryTimeout(cfg))
	defer cancel()

	db, cleanup, err := q.getOrDialPanelDB(ctx, asset, cfg, password)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return finishPanelDBOperation(fmt.Errorf("打开数据库会话失败: %w", err), cleanup)
	}
	opErr := fn(ctx, objectConn{conn: conn, driver: string(cfg.Driver), database: cfg.Database})
	if closeErr := conn.Close(); closeErr != nil && !isExpectedPanelCloseErr(closeErr) {
		opErr = errors.Join(opErr, fmt.Errorf("关闭数据库会话失败: %w", closeErr))
	}
	return finishPanelDBOperation(opErr, cleanup)
}

// GetTableMetadata returns columns / indexes / foreign keys for a table.
func (q *Query) GetTableMetadata(assetID int64, database, table string) (*query_svc.TableMetadata, error) {
	var result *query_svc.TableMetadata
	err := q.withObjectConn(assetID, database, func(ctx context.Context, oc objectConn) error {
		meta, err := query_svc.GetTableMetadata(ctx, oc.conn, oc.driverType(), oc.database, table)
		if err != nil {
			return err
		}
		result = meta
		return nil
	})
	return result, err
}

// ListDatabaseObjects returns views / procedures / functions / triggers.
func (q *Query) ListDatabaseObjects(assetID int64, database string) (*query_svc.DatabaseObjects, error) {
	var result *query_svc.DatabaseObjects
	err := q.withObjectConn(assetID, database, func(ctx context.Context, oc objectConn) error {
		objs, err := query_svc.ListDatabaseObjects(ctx, oc.conn, oc.driverType(), oc.database)
		if err != nil {
			return err
		}
		result = objs
		return nil
	})
	return result, err
}

// GetObjectSource returns the read-only definition of a view / routine / trigger.
// table is only used for PostgreSQL triggers (see query_svc.ObjectMeta.Table).
func (q *Query) GetObjectSource(assetID int64, database, objType, name, table string) (string, error) {
	var result string
	err := q.withObjectConn(assetID, database, func(ctx context.Context, oc objectConn) error {
		src, err := query_svc.GetObjectSource(ctx, oc.conn, oc.driverType(), oc.database, objType, name, table)
		if err != nil {
			return err
		}
		result = src
		return nil
	})
	return result, err
}
