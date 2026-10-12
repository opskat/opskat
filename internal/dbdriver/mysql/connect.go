package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"sync/atomic"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/sqldb"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// Open 打开一条 MySQL 会话。
func (driver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	db, err := sql.Open("mysql", dsn(req.Config, req.Password, req.Transport.Dial))
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

// dsn 生成连接串。dial 非 nil 时用它建立底层连接，否则用 go-sql-driver 默认的 TCP 拨号。
func dsn(cfg *asset_entity.DatabaseConfig, password string, dial dbdriver.DialFunc) string {
	c := gomysql.NewConfig()
	c.User = cfg.Username
	c.Passwd = password
	c.Net = "tcp"
	if dial != nil {
		c.Net = registerDialer(dial)
	}
	c.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	c.DBName = cfg.Database
	if cfg.TLS {
		c.TLSConfig = "skip-verify"
	}
	if cfg.Params != "" {
		c.Params = sqldb.ParseParams(cfg.Params)
	}
	return c.FormatDSN()
}

// dialerSeq 为自定义 dialer 生成唯一注册名。
// gomysql.RegisterDialContext 的注册表是全局的且同名覆盖,若名字复用,
// 后注册者会劫持先前连接池的重拨,因此每次 open 都用独立的名字。
var dialerSeq atomic.Int64

func registerDialer(dial dbdriver.DialFunc) string {
	name := fmt.Sprintf("opskat-dialer-%d", dialerSeq.Add(1))
	gomysql.RegisterDialContext(name, func(ctx context.Context, addr string) (net.Conn, error) {
		return dial(ctx, "tcp", addr)
	})
	return name
}

func setReadOnly(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY")
	return err
}
