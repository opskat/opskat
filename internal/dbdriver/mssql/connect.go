package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	"github.com/cago-frame/cago/pkg/logger"
	mssqldb "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Open 打开一条 SQL Server 会话。
func (driver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	db, err := open(dsn(req.Config, req.Password), req.Transport.Dial)
	if err != nil {
		return nil, err
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

func open(connStr string, dial dbdriver.DialFunc) (*sql.DB, error) {
	if dial == nil {
		return sql.Open("sqlserver", connStr)
	}
	cfg, err := msdsn.Parse(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse mssql dsn: %w", err)
	}
	connector := mssqldb.NewConnectorConfig(cfg)
	connector.Dialer = dial
	return sql.OpenDB(connector), nil
}

func dsn(cfg *dbdriver.Config, password string) string {
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
		for k, v := range sqldb.ParseParams(cfg.Params) {
			q.Set(k, v)
		}
	}
	u := &url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(cfg.Username, password),
		Host:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		RawQuery: q.Encode(),
	}
	return u.String()
}

// setReadOnly：SQL Server 没有连接级只读可设，只读靠权限策略拦截写语句。
func setReadOnly(ctx context.Context, _ *sql.DB) error {
	logger.Ctx(ctx).Info("MSSQL connection-level read-only not supported, relying on policy")
	return nil
}
