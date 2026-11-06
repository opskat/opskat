package postgresql

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Open 打开一条 PostgreSQL 会话。
func (driver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	connStr := dsn(req.Config, req.Password)
	var db *sql.DB
	if req.Transport.Dial != nil {
		db = sql.OpenDB(newDialConnector(connStr, req.Transport.Dial))
	} else {
		var err error
		db, err = sql.Open("pgx", connStr)
		if err != nil {
			return nil, err
		}
	}
	var readOnly func(context.Context, *sql.DB) error
	if req.Config.ReadOnly {
		readOnly = setReadOnly
	}
	if err := sqldb.Establish(ctx, db, readOnly); err != nil {
		return nil, err
	}
	return newSession(db), nil
}

// SessionPerDatabase：库名写进连接串，一条会话只连着一个库。
func (driver) SessionPerDatabase() bool { return true }

func newSession(db *sql.DB) dbdriver.Session {
	return sqldb.NewSession(db, dialect{})
}

func dsn(cfg *dbdriver.Config, password string) string {
	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		url.QueryEscape(cfg.Username), url.QueryEscape(password),
		cfg.Host, cfg.Port, url.PathEscape(cfg.Database), sslMode)
	if cfg.Params != "" {
		connStr += "&" + cfg.Params
	}
	return connStr
}

func setReadOnly(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, "SET default_transaction_read_only = on")
	return err
}

// dialConnector 通过自定义底层拨号(SSH 隧道 / SOCKS5 代理 / 代理链 / .local)连接 PostgreSQL 的 driver.Connector
type dialConnector struct {
	dsn  string
	dial dbdriver.DialFunc
}

func newDialConnector(dsn string, dial dbdriver.DialFunc) sqldriver.Connector {
	return &dialConnector{dsn: dsn, dial: dial}
}

func (c *dialConnector) Connect(ctx context.Context) (sqldriver.Conn, error) {
	connConfig, err := pgx.ParseConfig(c.dsn)
	if err != nil {
		return nil, err
	}
	// pgx 默认在 DialFunc 之前用系统解析器做本地 DNS 解析,而目标主机名可能只在
	// 隧道/代理的远端可解析,直连时也要交给 dial 统一解析,这里透传主机名。
	connConfig.LookupFunc = func(ctx context.Context, host string) ([]string, error) {
		return []string{host}, nil
	}
	connConfig.DialFunc = pgconn.DialFunc(c.dial)
	return stdlib.GetConnector(*connConfig).Connect(ctx)
}

func (c *dialConnector) Driver() sqldriver.Driver {
	return stdlib.GetDefaultDriver()
}
