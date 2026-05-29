package connpool

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/sshpool"

	"github.com/cago-frame/cago/pkg/logger"
	_ "github.com/glebarez/go-sqlite" // SQLite driver（与 cago bootstrap 同源，避免 sql.Register 重复 panic）
	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // PostgreSQL driver
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"go.uber.org/zap"
)

// DialDatabase 创建数据库连接（直连或通过 SSH 隧道）
// password 为已解析的明文密码，由调用方负责解密
func DialDatabase(ctx context.Context, asset *asset_entity.Asset, cfg *asset_entity.DatabaseConfig, password string, sshPool *sshpool.Pool) (*sql.DB, io.Closer, error) {

	var db *sql.DB
	var tunnel *SSHTunnel
	var err error

	if cfg.Driver == asset_entity.DriverSQLite {
		// SQLite 本地文件,不走隧道。只读已在 buildDSN 里通过 _pragma=query_only(1)
		// 写进 DSN（对每条连接生效），这里无需再单独 setReadOnly。
		db, err = openDirect(cfg, password)
		if err != nil {
			return nil, nil, err
		}
		if pingErr := db.PingContext(ctx); pingErr != nil {
			if cerr := db.Close(); cerr != nil {
				logger.Default().Warn("close db", zap.Error(cerr))
			}
			return nil, nil, fmt.Errorf("数据库连接失败: %w", pingErr)
		}
		return db, nil, nil
	}

	tunnelID := asset.SSHTunnelID
	if tunnelID == 0 {
		tunnelID = cfg.SSHAssetID // backward compat
	}
	if tunnelID > 0 && sshPool != nil {
		tunnel = NewSSHTunnel(tunnelID, cfg.Host, cfg.Port, sshPool)
		db, err = openWithTunnel(cfg, password, tunnel)
	} else {
		db, err = openDirect(cfg, password)
	}
	if err != nil {
		if tunnel != nil {
			if err := tunnel.Close(); err != nil {
				logger.Default().Warn("close ssh tunnel", zap.Error(err))
			}
		}
		return nil, nil, err
	}

	// 测试连接（必须在 setReadOnly 之前，确保连接性检查受 ctx 超时保护）
	if pingErr := db.PingContext(ctx); pingErr != nil {
		if err := db.Close(); err != nil {
			logger.Default().Warn("close db", zap.Error(err))
		}
		if tunnel != nil {
			if err := tunnel.Close(); err != nil {
				logger.Default().Warn("close ssh tunnel", zap.Error(err))
			}
		}
		return nil, nil, fmt.Errorf("数据库连接失败: %w", pingErr)
	}

	// 连接级只读
	if cfg.ReadOnly {
		if roErr := setReadOnly(ctx, db, cfg.Driver); roErr != nil {
			if err := db.Close(); err != nil {
				logger.Default().Warn("close db", zap.Error(err))
			}
			if tunnel != nil {
				if err := tunnel.Close(); err != nil {
					logger.Default().Warn("close ssh tunnel", zap.Error(err))
				}
			}
			return nil, nil, fmt.Errorf("设置只读模式失败: %w", roErr)
		}
	}

	// 直连时 tunnel 为 *SSHTunnel 的 nil，直接返回会变成 typed-nil 接口，
	// 调用方 `if closer != nil` 会误判为真并在 Close() 里 nil deref panic。
	if tunnel == nil {
		return db, nil, nil
	}
	return db, tunnel, nil
}

func openDirect(cfg *asset_entity.DatabaseConfig, password string) (*sql.DB, error) {
	driverName, dsn := buildDSN(cfg, password)
	return sql.Open(driverName, dsn)
}

func openWithTunnel(cfg *asset_entity.DatabaseConfig, password string, tunnel *SSHTunnel) (*sql.DB, error) {
	switch cfg.Driver {
	case asset_entity.DriverMySQL:
		return openMySQLWithTunnel(cfg, password, tunnel)
	case asset_entity.DriverPostgreSQL:
		return openPgWithTunnel(cfg, password, tunnel)
	case asset_entity.DriverMSSQL:
		return openMSSQLWithTunnel(cfg, password, tunnel)
	default:
		return nil, fmt.Errorf("不支持的数据库驱动: %s", cfg.Driver)
	}
}

func openMySQLWithTunnel(cfg *asset_entity.DatabaseConfig, password string, tunnel *SSHTunnel) (*sql.DB, error) {
	dialer := fmt.Sprintf("ssh-tunnel-%d", cfg.SSHAssetID)
	mysql.RegisterDialContext(dialer, func(ctx context.Context, addr string) (net.Conn, error) {
		return tunnel.Dial(ctx)
	})
	mysqlCfg := mysql.NewConfig()
	mysqlCfg.User = cfg.Username
	mysqlCfg.Passwd = password
	mysqlCfg.Net = dialer
	mysqlCfg.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	mysqlCfg.DBName = cfg.Database
	if cfg.TLS {
		mysqlCfg.TLSConfig = "skip-verify"
	}
	if cfg.Params != "" {
		mysqlCfg.Params = parseParams(cfg.Params)
	}
	return sql.Open("mysql", mysqlCfg.FormatDSN())
}

func openPgWithTunnel(cfg *asset_entity.DatabaseConfig, password string, tunnel *SSHTunnel) (*sql.DB, error) {
	// pgx 支持通过 DialFunc 自定义连接方式
	_, dsn := buildDSN(cfg, password)
	// 对于隧道模式，使用 pgx 的 connector API
	db := sql.OpenDB(newPgTunnelConnector(dsn, tunnel))
	return db, nil
}

func openMSSQLWithTunnel(cfg *asset_entity.DatabaseConfig, password string, tunnel *SSHTunnel) (*sql.DB, error) {
	_, dsn := buildDSN(cfg, password)
	msdsnCfg, err := msdsn.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse mssql dsn: %w", err)
	}
	connector := mssql.NewConnectorConfig(msdsnCfg)
	connector.Dialer = mssqlDialerFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		return tunnel.Dial(ctx)
	})
	return sql.OpenDB(connector), nil
}

type mssqlDialerFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func (f mssqlDialerFunc) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return f(ctx, network, addr)
}

func buildDSN(cfg *asset_entity.DatabaseConfig, password string) (driverName string, dsn string) {
	switch cfg.Driver {
	case asset_entity.DriverMySQL:
		mysqlCfg := mysql.NewConfig()
		mysqlCfg.User = cfg.Username
		mysqlCfg.Passwd = password
		mysqlCfg.Net = "tcp"
		mysqlCfg.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
		mysqlCfg.DBName = cfg.Database
		if cfg.TLS {
			mysqlCfg.TLSConfig = "skip-verify"
		}
		if cfg.Params != "" {
			mysqlCfg.Params = parseParams(cfg.Params)
		}
		return "mysql", mysqlCfg.FormatDSN()
	case asset_entity.DriverPostgreSQL:
		sslMode := cfg.SSLMode
		if sslMode == "" {
			sslMode = "disable"
		}
		dsn = fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
			url.QueryEscape(cfg.Username), url.QueryEscape(password),
			cfg.Host, cfg.Port, url.PathEscape(cfg.Database), sslMode)
		if cfg.Params != "" {
			dsn += "&" + cfg.Params
		}
		return "pgx", dsn
	case asset_entity.DriverMSSQL:
		q := url.Values{}
		if cfg.Database != "" {
			q.Set("database", cfg.Database)
		}
		if cfg.TLS {
			q.Set("encrypt", "true")
			q.Set("trustservercertificate", "true")
		} else {
			q.Set("encrypt", "disable")
		}
		if cfg.Params != "" {
			for k, v := range parseParams(cfg.Params) {
				q.Set(k, v)
			}
		}
		u := &url.URL{
			Scheme:   "sqlserver",
			User:     url.UserPassword(cfg.Username, password),
			Host:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			RawQuery: q.Encode(),
		}
		return "sqlserver", u.String()
	case asset_entity.DriverSQLite:
		dsn := "file:" + cfg.Path
		var query []string
		if cfg.Params != "" {
			query = append(query, cfg.Params)
		}
		// 只读用 _pragma 写进 DSN，保证连接池里"每一条"新建连接都带 query_only，
		// 而不是只在初次 dial 用过的那一条上设 PRAGMA（那样池里别的连接仍可写）。
		if cfg.ReadOnly {
			query = append(query, "_pragma=query_only(1)")
		}
		if len(query) > 0 {
			dsn += "?" + strings.Join(query, "&")
		}
		return "sqlite", dsn
	default:
		return "", ""
	}
}

func setReadOnly(ctx context.Context, db *sql.DB, driver asset_entity.DatabaseDriver) error {
	switch driver {
	case asset_entity.DriverMySQL:
		_, err := db.ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY")
		return err
	case asset_entity.DriverPostgreSQL:
		_, err := db.ExecContext(ctx, "SET default_transaction_read_only = on")
		return err
	case asset_entity.DriverMSSQL:
		logger.Ctx(ctx).Info("MSSQL connection-level read-only not supported, relying on policy")
		return nil
	}
	return nil
}

func parseParams(params string) map[string]string {
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
