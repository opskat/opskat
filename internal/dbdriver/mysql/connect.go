package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"sync/atomic"

	gomysql "github.com/go-sql-driver/mysql"

	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Open 打开一条 MySQL 会话。
func (driver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	network := "tcp"
	var release []io.Closer
	if req.Transport.Dial != nil {
		registration := registerDialer(req.Transport.Dial)
		network = string(registration)
		release = append(release, registration)
	}
	db, err := open(ctx, req, network)
	if err != nil {
		// 没开成的会话不会再被 Close：它登记的拨号函数在这里摘掉。
		for _, r := range release {
			_ = r.Close()
		}
		return nil, err
	}
	return sqldb.NewSession(db, dialect{}, release...), nil
}

// open 打开连接并确认可用；失败时 db 已经关闭。
func open(ctx context.Context, req dbdriver.OpenRequest, network string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn(req.Config, req.Password, network))
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
	return db, nil
}

// SessionPerDatabase：库名写进连接串，一条会话只连着一个库。
func (driver) SessionPerDatabase() bool { return true }

func newSession(db *sql.DB) dbdriver.Session {
	return sqldb.NewSession(db, dialect{})
}

// dsn 生成连接串。network 是 "tcp"（go-sql-driver 默认的拨号），或 registerDialer 登记的名字。
func dsn(cfg *dbdriver.Config, password, network string) string {
	c := gomysql.NewConfig()
	c.User = cfg.Username
	c.Passwd = password
	c.Net = network
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

// dialerRegistration 是登记在 go-sql-driver 全局表里的一个拨号函数的名字。拨号函数连着
// SSH 连接池 / 代理，Close 把它从全局表里摘掉：会话结束后不再留着。
type dialerRegistration string

func (r dialerRegistration) Close() error {
	gomysql.DeregisterDialContext(string(r))
	return nil
}

func registerDialer(dial dbdriver.DialFunc) dialerRegistration {
	name := fmt.Sprintf("opskat-dialer-%d", dialerSeq.Add(1))
	gomysql.RegisterDialContext(name, func(ctx context.Context, addr string) (net.Conn, error) {
		return dial(ctx, "tcp", addr)
	})
	return dialerRegistration(name)
}

func setReadOnly(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY")
	return err
}
