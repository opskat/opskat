package connpool

import (
	"context"
	"database/sql/driver"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// pgDialConnector 通过自定义底层拨号(SSH 隧道 / SOCKS5 代理)连接 PostgreSQL 的 driver.Connector
type pgDialConnector struct {
	dsn  string
	dial dialContextFunc
}

func newPgDialConnector(dsn string, dial dialContextFunc) driver.Connector {
	return &pgDialConnector{dsn: dsn, dial: dial}
}

func (c *pgDialConnector) Connect(ctx context.Context) (driver.Conn, error) {
	connConfig, err := pgx.ParseConfig(c.dsn)
	if err != nil {
		return nil, err
	}
	// pgx 默认在 DialFunc 之前做本地 DNS 解析,而目标主机名可能只在
	// 隧道/代理的远端可解析,这里透传主机名,解析交给远端。
	connConfig.LookupFunc = func(ctx context.Context, host string) ([]string, error) {
		return []string{host}, nil
	}
	connConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return c.dial(ctx, addr)
	}
	return stdlib.GetConnector(*connConfig).Connect(ctx)
}

func (c *pgDialConnector) Driver() driver.Driver {
	return stdlib.GetDefaultDriver()
}
