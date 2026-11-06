package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	_ "github.com/glebarez/go-sqlite" // SQLite driver（与 cago bootstrap 同源，避免 sql.Register 重复 panic）
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool/sqlitevfs"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver/sqldb"
)

// Open 打开一条 SQLite 会话：本地文件直接打开，远端文件经传输层给的文件系统打开。
func (driver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	if req.Config.SQLiteSource == sourceRemoteSSH {
		return openRemote(ctx, req)
	}
	db, err := openLocal(ctx, req.Config)
	if err != nil {
		return nil, err
	}
	return sqldb.NewSession(db, dialect{}), nil
}

// SessionPerDatabase：库就是那个文件，一条会话覆盖整个资产；调用方传来的库名只是其中的
// schema 名。远端文件因此在一个资产上只开一条会话、只抢一次文件锁。
func (driver) SessionPerDatabase() bool { return false }

// openLocal 打开本地文件。只读已经由 dsn 用 _pragma=query_only(1) 写进连接串（对每条
// 连接生效），这里不需要再单独设置。
func openLocal(ctx context.Context, cfg *dbdriver.Config) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(cfg))
	if err != nil {
		return nil, err
	}
	if err := sqldb.Establish(ctx, db, nil); err != nil {
		return nil, err
	}
	return db, nil
}

func dsn(cfg *dbdriver.Config) string {
	connStr := "file:" + cfg.Path
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
		connStr += "?" + strings.Join(query, "&")
	}
	return connStr
}

// openRemote 打开一台 SSH 主机上的 SQLite 文件。
func openRemote(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	if req.Transport.RemoteFS == nil {
		return nil, errors.New("远端 SQLite 必须指定 SSH 资产")
	}
	remote, err := req.Transport.RemoteFS(ctx)
	if err != nil {
		return nil, err
	}
	db, vfsCloser, err := sqlitevfs.Open(ctx, vfsRemote{remote}, req.Config.Path, sqlitevfs.Options{
		ReadOnly: req.Config.ReadOnly,
	})
	if err != nil {
		if closeErr := remote.Close(); closeErr != nil {
			logger.Ctx(ctx).Warn("close remote SQLite file system", zap.Error(closeErr))
		}
		return nil, err
	}
	// 关闭顺序：*sql.DB → VFS（注销并释放文件锁）→ 远端文件系统。关闭 WAL 库时 SQLite 会
	// 回调 VFS 删除 -wal/-shm，所以 VFS 与它底下的文件系统都必须活到 db.Close() 返回之后。
	return sqldb.NewSession(db, dialect{}, vfsCloser, remote), nil
}

// vfsRemote 把传输层的远端文件系统接到 sqlitevfs 要的接口上。
type vfsRemote struct {
	fs dbdriver.RemoteFS
}

func (r vfsRemote) OpenFile(name string, flag int) (sqlitevfs.RemoteFile, error) {
	f, err := r.fs.OpenFile(name, flag)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (r vfsRemote) Remove(name string) error { return r.fs.Remove(name) }

func (r vfsRemote) Stat(name string) (os.FileInfo, error) { return r.fs.Stat(name) }
