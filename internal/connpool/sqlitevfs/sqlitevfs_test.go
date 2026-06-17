package sqlitevfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestOpenRemoteSQLiteRollbackReadWrite(t *testing.T) {
	Convey("remote SQLite VFS reads and writes without CGO", t, func() {
		remote := localRemote{root: t.TempDir()}

		db, closer, err := Open(context.Background(), remote, "/data/app.db", Options{})
		So(err, ShouldBeNil)
		defer func() { _ = closer.Close() }()
		defer func() { _ = db.Close() }()

		_, err = db.Exec("CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL)")
		So(err, ShouldBeNil)
		_, err = db.Exec("INSERT INTO kv(k, v) VALUES ('alpha', 'one')")
		So(err, ShouldBeNil)
		_, err = db.Exec("UPDATE kv SET v = 'two' WHERE k = 'alpha'")
		So(err, ShouldBeNil)

		var got string
		err = db.QueryRow("SELECT v FROM kv WHERE k = 'alpha'").Scan(&got)
		So(err, ShouldBeNil)
		So(got, ShouldEqual, "two")
		err = db.QueryRow("PRAGMA integrity_check").Scan(&got)
		So(err, ShouldBeNil)
		So(got, ShouldEqual, "ok")

		So(db.Close(), ShouldBeNil)
		So(closer.Close(), ShouldBeNil)

		_, err = os.Stat(filepath.Join(remote.root, "data", "app.db"))
		So(err, ShouldBeNil)
		_, err = os.Stat(filepath.Join(remote.root, "data", "app.db.opskat.lock"))
		So(errors.Is(err, os.ErrNotExist), ShouldBeTrue)
	})
}

func TestOpenRemoteSQLiteReadOnly(t *testing.T) {
	Convey("read-only remote SQLite refuses writes", t, func() {
		remote := localRemote{root: t.TempDir()}

		db, closer, err := Open(context.Background(), remote, "/data/app.db", Options{})
		So(err, ShouldBeNil)
		_, err = db.Exec("CREATE TABLE t (id INTEGER)")
		So(err, ShouldBeNil)
		So(db.Close(), ShouldBeNil)
		So(closer.Close(), ShouldBeNil)

		roDB, roCloser, err := Open(context.Background(), remote, "/data/app.db", Options{ReadOnly: true})
		So(err, ShouldBeNil)
		defer func() { _ = roCloser.Close() }()
		defer func() { _ = roDB.Close() }()

		_, err = roDB.Exec("INSERT INTO t VALUES (1)")
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "readonly")
		_, err = os.Stat(filepath.Join(remote.root, "data", "app.db.opskat.lock"))
		So(errors.Is(err, os.ErrNotExist), ShouldBeTrue)
	})
}

func TestOpenRemoteSQLiteLockFileBlocksSecondWriter(t *testing.T) {
	Convey("remote lock file blocks a second writer", t, func() {
		remote := localRemote{root: t.TempDir()}

		db, closer, err := Open(context.Background(), remote, "/data/app.db", Options{})
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		defer func() { _ = closer.Close() }()

		_, secondCloser, err := Open(context.Background(), remote, "/data/app.db", Options{})
		So(err, ShouldNotBeNil)
		So(secondCloser, ShouldBeNil)
		So(err.Error(), ShouldContainSubstring, "lock")
	})
}

func TestOpenRemoteSQLiteRejectsWALWithoutExclusive(t *testing.T) {
	Convey("WAL is rejected unless exclusive locking is explicit", t, func() {
		remote := localRemote{root: t.TempDir()}

		_, closer, err := Open(context.Background(), remote, "/data/app.db", Options{JournalMode: JournalModeWAL})
		So(err, ShouldNotBeNil)
		So(closer, ShouldBeNil)
		So(err.Error(), ShouldContainSubstring, "WAL")
	})
}

func TestOpenRemoteSQLiteCleansLockOnOpenError(t *testing.T) {
	Convey("open failure after acquiring the remote lock removes the lock file", t, func() {
		remote := localRemote{root: t.TempDir()}

		db, closer, err := Open(context.Background(), remote, "/data/app.db", Options{JournalMode: "not_a_journal_mode"})
		So(err, ShouldNotBeNil)
		So(db, ShouldBeNil)
		So(closer, ShouldBeNil)

		_, statErr := os.Stat(filepath.Join(remote.root, "data", "app.db.opskat.lock"))
		So(errors.Is(statErr, os.ErrNotExist), ShouldBeTrue)
	})
}

type localRemote struct {
	root string
}

func (r localRemote) OpenFile(name string, flag int) (RemoteFile, error) {
	full, err := r.fullPath(name)
	if err != nil {
		return nil, err
	}
	if flag&os.O_CREATE != 0 {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(full, flag, 0o600) //nolint:gosec // Test helper constrains all requested paths under t.TempDir.
}

func (r localRemote) Remove(name string) error {
	full, err := r.fullPath(name)
	if err != nil {
		return err
	}
	return os.Remove(full)
}

func (r localRemote) Stat(name string) (os.FileInfo, error) {
	full, err := r.fullPath(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(full)
}

func (r localRemote) fullPath(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) {
		clean = clean[1:]
	}
	full := filepath.Join(r.root, clean)
	rel, err := filepath.Rel(r.root, full)
	if err != nil {
		return "", err
	}
	if rel == ".." || len(rel) > 3 && rel[:3] == "../" {
		return "", os.ErrPermission
	}
	return full, nil
}

var _ RemoteFS = localRemote{}
var _ RemoteFile = (*os.File)(nil)
var _ io.Closer = (*os.File)(nil)
