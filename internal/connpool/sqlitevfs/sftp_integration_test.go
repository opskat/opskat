package sqlitevfs

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

func TestSFTPRemoteSQLiteIntegration(t *testing.T) {
	target := os.Getenv("OPSKAT_SQLITEVFS_SFTP_TEST_TARGET")
	if target == "" {
		t.Skip("set OPSKAT_SQLITEVFS_SFTP_TEST_TARGET=user@host[:port] to run the SFTP integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sftpSession, err := openTestSFTP(ctx, target)
	if err != nil {
		t.Fatalf("open sftp: %v", err)
	}
	defer func() {
		if err := sftpSession.Close(); err != nil {
			t.Errorf("close sftp session: %v", err)
		}
	}()
	sftpClient := sftpSession.client

	remoteDir := "/tmp/opskat-sqlitevfs-it-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	remotePath := path.Join(remoteDir, "remote.db")
	if err := sftpClient.MkdirAll(remoteDir); err != nil {
		t.Fatalf("mkdir remote dir: %v", err)
	}
	defer cleanupSFTPTestDir(sftpClient, remoteDir)

	db, closer, err := Open(ctx, sftpTestRemoteFS{client: sftpClient}, remotePath, Options{})
	if err != nil {
		t.Fatalf("open remote sqlite: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT NOT NULL)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO kv(k, v) VALUES ('alpha', 'one'), ('beta', 'two')"); err != nil {
		t.Fatalf("insert rows: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE kv SET v = 'three' WHERE k = 'beta'"); err != nil {
		t.Fatalf("update row: %v", err)
	}
	var got string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&got); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if got != "ok" {
		t.Fatalf("integrity_check = %q, want ok", got)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("close vfs: %v", err)
	}

	roDB, roCloser, err := Open(ctx, sftpTestRemoteFS{client: sftpClient}, remotePath, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("reopen readonly: %v", err)
	}
	defer func() {
		if err := roCloser.Close(); err != nil {
			t.Errorf("close readonly vfs: %v", err)
		}
	}()
	defer func() {
		if err := roDB.Close(); err != nil {
			t.Errorf("close readonly db: %v", err)
		}
	}()

	if err := roDB.QueryRowContext(ctx, "SELECT v FROM kv WHERE k = 'beta'").Scan(&got); err != nil {
		t.Fatalf("query readonly row: %v", err)
	}
	if got != "three" {
		t.Fatalf("beta = %q, want three", got)
	}
	if _, err := roDB.ExecContext(ctx, "INSERT INTO kv(k, v) VALUES ('gamma', 'four')"); err == nil {
		t.Fatal("readonly remote sqlite accepted a write")
	}
}

type sftpTestRemoteFS struct {
	client *sftp.Client
}

func (fs sftpTestRemoteFS) OpenFile(name string, flag int) (RemoteFile, error) {
	return fs.client.OpenFile(name, flag)
}

func (fs sftpTestRemoteFS) Remove(name string) error {
	return fs.client.Remove(name)
}

func (fs sftpTestRemoteFS) Stat(name string) (os.FileInfo, error) {
	return fs.client.Stat(name)
}

type testSFTPSession struct {
	client *sftp.Client
	cmd    *exec.Cmd
	stdin  io.WriteCloser
}

func openTestSFTP(ctx context.Context, target string) (*testSFTPSession, error) {
	//nolint:gosec // Integration test target is supplied explicitly by the test environment.
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", target, "-s", "sftp")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	client, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return nil, err
	}
	return &testSFTPSession{client: client, cmd: cmd, stdin: stdin}, nil
}

func (s *testSFTPSession) Close() error {
	err := s.client.Close()
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if waitErr := s.cmd.Wait(); err == nil && waitErr != nil {
		err = waitErr
	}
	return err
}

func cleanupSFTPTestDir(client *sftp.Client, dir string) {
	for _, name := range []string{"remote.db", "remote.db-journal", "remote.db-wal", "remote.db-shm", "remote.db.opskat.lock"} {
		_ = client.Remove(path.Join(dir, name))
	}
	_ = client.RemoveDirectory(dir)
}
