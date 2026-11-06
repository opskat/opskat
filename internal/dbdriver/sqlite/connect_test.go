package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/dbdriver/dbdrivertest"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

func openLocalSession(t *testing.T, cfg *dbdriver.Config) dbdriver.Session {
	t.Helper()
	session, err := driver{}.Open(context.Background(), dbdriver.OpenRequest{Config: cfg})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestSQLitePassesConformance(t *testing.T) {
	d, err := dbregistry.Lookup(asset_entity.DriverSQLite)
	require.NoError(t, err)

	dbdrivertest.RunConformance(t, d, func(t *testing.T) dbdriver.Session {
		session, err := d.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "conformance.db"),
		}})
		require.NoError(t, err)
		return session
	})
}

func TestDSN(t *testing.T) {
	Convey("SQLite DSN", t, func() {
		cfg := &dbdriver.Config{
			Driver: asset_entity.DriverSQLite,
			Path:   "/tmp/test.db",
		}
		So(dsn(cfg), ShouldEqual, "file:/tmp/test.db")
	})

	Convey("SQLite DSN with params", t, func() {
		cfg := &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db",
			Params: "_pragma=busy_timeout(5000)",
		}
		So(dsn(cfg), ShouldEqual, "file:/tmp/x.db?_pragma=busy_timeout(5000)")
	})

	Convey("SQLite 只读写进 DSN，对每条连接生效", t, func() {
		cfg := &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: "/tmp/x.db",
			Params: "_pragma=busy_timeout(5000)", ReadOnly: true,
		}
		So(dsn(cfg), ShouldEqual, "file:/tmp/x.db?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	})
}

func TestOpenLocal(t *testing.T) {
	ctx := context.Background()

	Convey("SQLite 本地文件直连", t, func() {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		session := openLocalSession(t, &dbdriver.Config{Driver: asset_entity.DriverSQLite, Path: dbPath})

		_, execErr := session.Execute(ctx, "CREATE TABLE t (id INTEGER)")
		So(execErr, ShouldBeNil)
		_, err := os.Stat(dbPath)
		So(err, ShouldBeNil)
	})

	Convey("SQLite ReadOnly 用 PRAGMA query_only", t, func() {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		// 先建表
		rw := openLocalSession(t, &dbdriver.Config{Driver: asset_entity.DriverSQLite, Path: dbPath})
		_, err := rw.Execute(ctx, "CREATE TABLE t (id INTEGER)")
		So(err, ShouldBeNil)
		So(rw.Close(), ShouldBeNil)

		// 再以 ReadOnly 打开
		ro := openLocalSession(t, &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: dbPath, ReadOnly: true,
		})
		_, execErr := ro.Execute(ctx, "INSERT INTO t VALUES (1)")
		So(execErr, ShouldNotBeNil)
		So(execErr.Error(), ShouldContainSubstring, "read")
	})

	Convey("SQLite ReadOnly 对连接池里新建的连接同样生效", t, func() {
		dbPath := filepath.Join(t.TempDir(), "test.db")
		rw := openLocalSession(t, &dbdriver.Config{Driver: asset_entity.DriverSQLite, Path: dbPath})
		_, err := rw.Execute(ctx, "CREATE TABLE t (id INTEGER)")
		So(err, ShouldBeNil)
		So(rw.Close(), ShouldBeNil)

		roDB, err := openLocal(ctx, &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: dbPath, ReadOnly: true,
		})
		So(err, ShouldBeNil)
		defer func() { _ = roDB.Close() }()

		// 占住一条连接，强制下面的写落到连接池新建的另一条连接上。
		// 若只读只设在初次 dial 用过的那条连接上，这里会写成功 → 暴露 bug。
		conn1, err := roDB.Conn(ctx)
		So(err, ShouldBeNil)
		defer func() { _ = conn1.Close() }()

		_, execErr := roDB.ExecContext(ctx, "INSERT INTO t VALUES (1)")
		So(execErr, ShouldNotBeNil)
		So(execErr.Error(), ShouldContainSubstring, "read")
	})
}

func TestExecuteSQLitePragmaQuery(t *testing.T) {
	Convey("Execute treats SQLite PRAGMA as a row-returning query", t, func() {
		session := openLocalSession(t, &dbdriver.Config{
			Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "pragma.db"),
		})
		_, err := session.Execute(context.Background(), `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)`)
		So(err, ShouldBeNil)

		result, err := session.Execute(context.Background(), `PRAGMA table_info("users")`)
		So(err, ShouldBeNil)
		So(result.Rows, ShouldNotBeNil)
		So(result.Rows.Columns, ShouldContain, "name")
		So(len(result.Rows.Rows), ShouldEqual, 2)
	})
}

func TestExecutePagedPagesARealDatabase(t *testing.T) {
	ctx := context.Background()
	session := openLocalSession(t, &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, Path: filepath.Join(t.TempDir(), "paged.db"),
	})
	_, err := session.Execute(ctx, "CREATE TABLE t (id INTEGER)")
	require.NoError(t, err)
	_, err = session.Execute(ctx, "INSERT INTO t VALUES (1), (2), (3), (4), (5)")
	require.NoError(t, err)

	res, err := session.ExecutePaged(ctx, "SELECT id FROM t ORDER BY id;", dbdriver.Page{Index: 1, Size: 2})
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(3)}, {int64(4)}}, res.Rows.Rows)
	require.NotNil(t, res.Rows.Total)
	assert.Equal(t, int64(5), *res.Rows.Total)
}

// localRemote 用一个本地目录冒充远端主机的文件系统，并记下自己有没有被归还。
type localRemote struct {
	root   string
	closed bool
	// beforeClose 在归还前调用，用来观察归还发生时数据库的状态。
	beforeClose func()
}

func (r *localRemote) OpenFile(name string, flag int) (dbdriver.RemoteFile, error) {
	f, err := os.OpenFile(r.fullPath(name), flag, 0o600)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (r *localRemote) Remove(name string) error { return os.Remove(r.fullPath(name)) }

func (r *localRemote) Stat(name string) (os.FileInfo, error) { return os.Stat(r.fullPath(name)) }

func (r *localRemote) Close() error {
	if r.beforeClose != nil {
		r.beforeClose()
	}
	r.closed = true
	return nil
}

func (r *localRemote) fullPath(name string) string {
	return filepath.Join(r.root, filepath.FromSlash(strings.TrimPrefix(name, "/")))
}

func remoteRequest(cfg *dbdriver.Config, remote *localRemote) dbdriver.OpenRequest {
	return dbdriver.OpenRequest{
		Config: cfg,
		Transport: dbdriver.Transport{RemoteFS: func(context.Context) (dbdriver.RemoteFS, error) {
			return remote, nil
		}},
	}
}

// 远端 SQLite 经传输层给的文件系统打开；关闭会话时先关数据库，再归还文件系统。
func TestOpenRemoteUsesTheTransportFileSystem(t *testing.T) {
	ctx := context.Background()
	remote := &localRemote{root: t.TempDir()}
	cfg := &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, SQLiteSource: sourceRemoteSSH, Path: "/app.db",
	}

	session, err := driver{}.Open(ctx, remoteRequest(cfg, remote))
	require.NoError(t, err)
	_, err = session.Execute(ctx, "CREATE TABLE t (id INTEGER)")
	require.NoError(t, err)
	_, err = session.Execute(ctx, "INSERT INTO t VALUES (7)")
	require.NoError(t, err)
	res, err := session.Execute(ctx, "SELECT id FROM t")
	require.NoError(t, err)
	assert.Equal(t, [][]any{{int64(7)}}, res.Rows.Rows)

	_, statErr := os.Stat(filepath.Join(remote.root, "app.db"))
	require.NoError(t, statErr, "the database file lives on the remote file system")
	_, statErr = os.Stat(filepath.Join(remote.root, "app.db.opskat.lock"))
	require.NoError(t, statErr, "a writable remote session holds the lock file")

	var lockHeldAtRelease bool
	remote.beforeClose = func() {
		_, lockErr := os.Stat(filepath.Join(remote.root, "app.db.opskat.lock"))
		lockHeldAtRelease = lockErr == nil
	}
	require.NoError(t, session.Close())
	assert.True(t, remote.closed, "closing the session returns the file system")
	assert.False(t, lockHeldAtRelease, "the database must be closed (lock released) before the file system is returned")
}

func TestOpenRemoteReturnsTheFileSystemWhenTheDatabaseCannotBeOpened(t *testing.T) {
	remote := &localRemote{root: t.TempDir()}
	// 只读打开一个不存在的远端文件：打不开。
	cfg := &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, SQLiteSource: sourceRemoteSSH,
		Path: "/missing.db", ReadOnly: true,
	}

	_, err := driver{}.Open(context.Background(), remoteRequest(cfg, remote))
	require.Error(t, err)
	assert.True(t, remote.closed)
}

func TestOpenRemoteFailures(t *testing.T) {
	cfg := &dbdriver.Config{
		Driver: asset_entity.DriverSQLite, SQLiteSource: sourceRemoteSSH,
		Path: "/tmp/remote.db",
	}

	Convey("远端 SQLite 没有 SSH 资产时失败", t, func() {
		session, err := driver{}.Open(context.Background(), dbdriver.OpenRequest{Config: cfg})
		So(session, ShouldBeNil)
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "SSH")
	})

	Convey("远端文件系统打不开时返回它的错误", t, func() {
		unreachable := errors.New("ssh: handshake failed")
		session, err := driver{}.Open(context.Background(), dbdriver.OpenRequest{
			Config: cfg,
			Transport: dbdriver.Transport{RemoteFS: func(context.Context) (dbdriver.RemoteFS, error) {
				return nil, unreachable
			}},
		})
		So(session, ShouldBeNil)
		So(errors.Is(err, unreachable), ShouldBeTrue)
	})
}
