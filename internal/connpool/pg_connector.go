package connpool

import (
	"context"
	"database/sql/driver"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// pgDialConnector 通过自定义底层拨号(直连 / SSH 隧道 / SOCKS5 代理)连接 PostgreSQL 的 driver.Connector
type pgDialConnector struct {
	dsn  string
	dial networkDialFunc
}

func newPgDialConnector(dsn string, dial networkDialFunc) driver.Connector {
	return &pgDialConnector{dsn: dsn, dial: dial}
}

func (c *pgDialConnector) Connect(ctx context.Context) (driver.Conn, error) {
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

func (c *pgDialConnector) Driver() driver.Driver {
	return stdlib.GetDefaultDriver()
}
