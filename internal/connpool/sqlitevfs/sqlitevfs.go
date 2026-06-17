package sqlitevfs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ncruces/go-sqlite3"
	_ "github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/vfs"
)

const (
	JournalModeDelete = "DELETE"
	JournalModeWAL    = "WAL"
)

type Options struct {
	ReadOnly         bool
	JournalMode      string
	ExclusiveLocking bool
}

type RemoteFS interface {
	OpenFile(name string, flag int) (RemoteFile, error)
	Remove(name string) error
	Stat(name string) (os.FileInfo, error)
}

type RemoteFile interface {
	io.Closer
	io.ReaderAt
	io.WriterAt
	Truncate(size int64) error
	Sync() error
	Stat() (os.FileInfo, error)
}

var vfsSeq atomic.Int64

func Open(ctx context.Context, remote RemoteFS, remotePath string, opts Options) (*sql.DB, io.Closer, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if remote == nil {
		return nil, nil, errors.New("remote SQLite VFS requires remote filesystem")
	}
	if remotePath == "" || !path.IsAbs(remotePath) {
		return nil, nil, errors.New("remote SQLite path must be absolute")
	}
	if strings.EqualFold(opts.JournalMode, JournalModeWAL) && !opts.ExclusiveLocking {
		return nil, nil, errors.New("remote SQLite WAL requires exclusive locking")
	}

	var lock *remoteLock
	var err error
	if !opts.ReadOnly {
		lock, err = acquireLock(remote, remotePath+".opskat.lock")
		if err != nil {
			return nil, nil, err
		}
	}

	name := "opskat_sqlite_remote_" + strconv.FormatInt(vfsSeq.Add(1), 10)
	remoteVFS := newRemoteVFS(remote, path.Dir(remotePath), path.Base(remotePath))
	vfs.Register(name, remoteVFS)

	dsn := "file:" + path.Base(remotePath) + "?vfs=" + name
	if opts.ReadOnly {
		dsn += "&mode=ro"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		vfs.Unregister(name)
		if lock != nil {
			_ = lock.Close()
		}
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)

	if opts.ExclusiveLocking {
		if _, err = db.ExecContext(ctx, "PRAGMA locking_mode=EXCLUSIVE"); err != nil {
			return nil, nil, cleanupOpenError(db, name, lock, fmt.Errorf("set locking mode: %w", err))
		}
	}

	journalMode := opts.JournalMode
	if journalMode == "" {
		journalMode = JournalModeDelete
	}
	var actualJournal string
	if err = db.QueryRowContext(ctx, "PRAGMA journal_mode="+journalMode).Scan(&actualJournal); err != nil {
		return nil, nil, cleanupOpenError(db, name, lock, fmt.Errorf("set journal mode: %w", err))
	}
	if !strings.EqualFold(actualJournal, journalMode) {
		return nil, nil, cleanupOpenError(db, name, lock, fmt.Errorf("remote SQLite journal mode %s requested, got %s", journalMode, actualJournal))
	}
	if _, err = db.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return nil, nil, cleanupOpenError(db, name, lock, fmt.Errorf("set synchronous: %w", err))
	}
	if opts.ReadOnly {
		if _, err = db.ExecContext(ctx, "PRAGMA query_only=1"); err != nil {
			return nil, nil, cleanupOpenError(db, name, lock, fmt.Errorf("set query_only: %w", err))
		}
	}
	if err = db.PingContext(ctx); err != nil {
		return nil, nil, cleanupOpenError(db, name, lock, fmt.Errorf("remote SQLite ping: %w", err))
	}

	return db, &closer{vfsName: name, lock: lock}, nil
}

func cleanupOpenError(db *sql.DB, vfsName string, lock *remoteLock, err error) error {
	_ = db.Close()
	vfs.Unregister(vfsName)
	if lock != nil {
		_ = lock.Close()
	}
	return err
}

type closer struct {
	once    sync.Once
	vfsName string
	lock    *remoteLock
}

func (c *closer) Close() error {
	var err error
	c.once.Do(func() {
		vfs.Unregister(c.vfsName)
		if c.lock != nil {
			err = c.lock.Close()
		}
	})
	return err
}

type remoteLock struct {
	remote RemoteFS
	path   string
	file   RemoteFile
}

func acquireLock(remote RemoteFS, lockPath string) (*remoteLock, error) {
	f, err := remote.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return nil, fmt.Errorf("acquire remote SQLite lock: %w", err)
	}
	return &remoteLock{remote: remote, path: lockPath, file: f}, nil
}

func (l *remoteLock) Close() error {
	err := l.file.Close()
	if rmErr := l.remote.Remove(l.path); err == nil && rmErr != nil && !isNotExist(rmErr) {
		err = rmErr
	}
	return err
}

type remoteVFS struct {
	remote    RemoteFS
	remoteDir string
	dbName    string

	mu     sync.Mutex
	locks  map[string]vfs.LockLevel
	tempID int
}

func newRemoteVFS(remote RemoteFS, remoteDir, dbName string) *remoteVFS {
	return &remoteVFS{
		remote:    remote,
		remoteDir: remoteDir,
		dbName:    dbName,
		locks:     make(map[string]vfs.LockLevel),
	}
}

func (v *remoteVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	remotePath := v.resolve(name)
	if name == "" {
		v.mu.Lock()
		v.tempID++
		remotePath = path.Join(v.remoteDir, fmt.Sprintf(".opskat-tmp-%d", v.tempID))
		v.mu.Unlock()
		flags |= vfs.OPEN_CREATE | vfs.OPEN_DELETEONCLOSE
	}

	fileFlags := 0
	switch {
	case flags&vfs.OPEN_READWRITE != 0:
		fileFlags |= os.O_RDWR
	case flags&vfs.OPEN_READONLY != 0:
		fileFlags |= os.O_RDONLY
	default:
		fileFlags |= os.O_RDWR
	}
	if flags&vfs.OPEN_CREATE != 0 {
		fileFlags |= os.O_CREATE
	}
	if flags&vfs.OPEN_EXCLUSIVE != 0 {
		fileFlags |= os.O_EXCL
	}

	f, err := v.remote.OpenFile(remotePath, fileFlags)
	if err != nil {
		return nil, 0, sqlite3.CANTOPEN
	}
	return &remoteFile{vfs: v, file: f, remotePath: remotePath, deleteOnClose: flags&vfs.OPEN_DELETEONCLOSE != 0}, flags, nil
}

func (v *remoteVFS) Delete(name string, _ bool) error {
	err := v.remote.Remove(v.resolve(name))
	if isNotExist(err) {
		return sqlite3.IOERR_DELETE_NOENT
	}
	return err
}

func (v *remoteVFS) Access(name string, _ vfs.AccessFlag) (bool, error) {
	_, err := v.remote.Stat(v.resolve(name))
	if isNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (v *remoteVFS) FullPathname(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	return "/" + path.Base(name), nil
}

func (v *remoteVFS) resolve(name string) string {
	clean := path.Clean(strings.TrimPrefix(name, "/"))
	if clean == "." || clean == "" {
		clean = v.dbName
	}
	return path.Join(v.remoteDir, path.Base(clean))
}

type remoteFile struct {
	vfs           *remoteVFS
	file          RemoteFile
	remotePath    string
	deleteOnClose bool
}

func (f *remoteFile) Close() error {
	err := f.file.Close()
	if f.deleteOnClose {
		if rmErr := f.vfs.remote.Remove(f.remotePath); err == nil && rmErr != nil && !isNotExist(rmErr) {
			err = rmErr
		}
	}
	return err
}

func (f *remoteFile) ReadAt(p []byte, off int64) (int, error) {
	return f.file.ReadAt(p, off)
}

func (f *remoteFile) WriteAt(p []byte, off int64) (int, error) {
	return f.file.WriteAt(p, off)
}

func (f *remoteFile) Truncate(size int64) error {
	return f.file.Truncate(size)
}

func (f *remoteFile) Sync(vfs.SyncFlag) error {
	return f.file.Sync()
}

func (f *remoteFile) Size() (int64, error) {
	fi, err := f.file.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func (f *remoteFile) Lock(lock vfs.LockLevel) error {
	f.vfs.mu.Lock()
	defer f.vfs.mu.Unlock()
	if lock > f.vfs.locks[f.remotePath] {
		f.vfs.locks[f.remotePath] = lock
	}
	return nil
}

func (f *remoteFile) Unlock(lock vfs.LockLevel) error {
	f.vfs.mu.Lock()
	defer f.vfs.mu.Unlock()
	if lock == vfs.LOCK_NONE {
		delete(f.vfs.locks, f.remotePath)
		return nil
	}
	f.vfs.locks[f.remotePath] = lock
	return nil
}

func (f *remoteFile) CheckReservedLock() (bool, error) {
	f.vfs.mu.Lock()
	defer f.vfs.mu.Unlock()
	return f.vfs.locks[f.remotePath] >= vfs.LOCK_RESERVED, nil
}

func (f *remoteFile) SectorSize() int {
	return 4096
}

func (f *remoteFile) DeviceCharacteristics() vfs.DeviceCharacteristic {
	return 0
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such file")
}
