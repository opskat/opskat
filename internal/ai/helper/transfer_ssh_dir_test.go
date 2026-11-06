package helper

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/sftpio/sftpiotest"
)

// preparedSFTP 返回一个注入了单资产 SFTP cache 的 ctx：PrepareTransferDirs 与 Write 走的是
// 生产代码的 ExecuteWithSFTP 路径，连到一个服务真实文件系统的进程内服务端。
func preparedSFTP(t *testing.T) (context.Context, *asset_entity.Asset) {
	t.Helper()
	client := newTestSFTP(t)
	cache := newSFTPClientCache(func(context.Context, int64) (*sftp.Client, io.Closer, error) {
		return client, nil, nil
	})
	t.Cleanup(func() { _ = cache.Close() })
	return WithSFTPClientCache(context.Background(), cache), &asset_entity.Asset{ID: 9, Type: asset_entity.AssetTypeSSH}
}

// prepareAndWrite 按 cp 多源的顺序走一遍：范围进 ctx，目录一次建好，再逐条写入。
func prepareAndWrite(t *testing.T, scope string, paths []string) (writeErrs []error, prepErr error) {
	t.Helper()
	ctx, asset := preparedSFTP(t)
	ctx = WithTransferWriteScope(ctx, scope)
	ctx, prepErr = PrepareTransferDirs(ctx, sshTransfer, asset, paths)
	if prepErr != nil {
		return nil, prepErr
	}
	for _, p := range paths {
		writeErrs = append(writeErrs, sshTransfer.Write(ctx, asset, p, strings.NewReader("x"), 1))
	}
	return writeErrs, nil
}

func TestSFTPPrepareTransferDirsCreatesTreeAndWritesLand(t *testing.T) {
	scope := filepath.Join(t.TempDir(), "new-scope")
	remote := sftpiotest.RemotePath(scope)
	paths := []string{remote + "/a/b/1.txt", remote + "/a/b/2.txt", remote + "/a/3.txt", remote + "/4.txt"}

	writeErrs, prepErr := prepareAndWrite(t, remote, paths)

	if prepErr != nil {
		t.Fatalf("PrepareTransferDirs: %v", prepErr)
	}
	for i, err := range writeErrs {
		if err != nil {
			t.Fatalf("Write(%s): %v", paths[i], err)
		}
	}
	for _, rel := range []string{"a/b/1.txt", "a/b/2.txt", "a/3.txt", "4.txt"} {
		if got, err := os.ReadFile(filepath.Join(scope, filepath.FromSlash(rel))); err != nil || string(got) != "x" { //nolint:gosec // 测试临时目录
			t.Errorf("%s = %q, %v", rel, got, err)
		}
	}
}

// 目录只核验一次，但一个是符号链接的目录分量照样拒绝，一个字节都不写到链接指向的地方。
func TestSFTPPrepareTransferDirsRejectsSymlinkedDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	remote := sftpiotest.RemotePath(root)

	_, prepErr := prepareAndWrite(t, remote, []string{remote + "/ok.txt", remote + "/escape/deep/x.txt"})

	if prepErr == nil || !strings.Contains(prepErr.Error(), "symbolic link") {
		t.Fatalf("PrepareTransferDirs error = %v, want a symbolic link rejection", prepErr)
	}
	if _, err := os.Stat(filepath.Join(outside, "deep")); !os.IsNotExist(err) {
		t.Errorf("directory created through the symlink: %v", err)
	}
}

func TestSFTPPrepareTransferDirsRejectsSymlinkedScope(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "scope")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	remote := sftpiotest.RemotePath(link)

	_, prepErr := prepareAndWrite(t, remote, []string{remote + "/sub/x.txt"})

	if prepErr == nil || !strings.Contains(prepErr.Error(), "symbolic link") {
		t.Fatalf("PrepareTransferDirs error = %v, want a symbolic link rejection", prepErr)
	}
}

// 目录核验过了，文件名本身是一条符号链接时写入仍然被拒，链接目标不被改写。
func TestSFTPPreparedWriteRejectsSymlinkedFileName(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "authorized_keys")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "d", "keys")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	remote := sftpiotest.RemotePath(root)
	paths := []string{remote + "/d/ok.txt", remote + "/d/keys"}

	writeErrs, prepErr := prepareAndWrite(t, remote, paths)

	if prepErr != nil {
		t.Fatalf("PrepareTransferDirs: %v", prepErr)
	}
	if writeErrs[0] != nil {
		t.Errorf("Write(ok.txt): %v", writeErrs[0])
	}
	if writeErrs[1] == nil || !strings.Contains(writeErrs[1].Error(), "symbolic link") {
		t.Errorf("Write(keys) error = %v, want a symbolic link rejection", writeErrs[1])
	}
	if got, _ := os.ReadFile(target); string(got) != "original" { //nolint:gosec // 测试临时目录
		t.Errorf("symlink target rewritten to %q", got)
	}
}

func TestSFTPPrepareTransferDirsRejectsPathsOutsideScope(t *testing.T) {
	root := t.TempDir()
	scope := sftpiotest.RemotePath(filepath.Join(root, "scope"))
	sibling := sftpiotest.RemotePath(filepath.Join(root, "scope-other", "x.txt"))

	_, prepErr := prepareAndWrite(t, scope, []string{scope + "/a.txt", sibling})

	if prepErr == nil || !strings.Contains(prepErr.Error(), "escapes approved scope") {
		t.Fatalf("PrepareTransferDirs error = %v, want an escapes-approved-scope rejection", prepErr)
	}
	if _, err := os.Stat(filepath.Join(root, "scope-other")); !os.IsNotExist(err) {
		t.Errorf("directory created outside the approved scope: %v", err)
	}
}

// 没有这项能力的适配器原样交回 ctx，不报错——本地与对象存储端照旧逐条写。
func TestPrepareTransferDirsIsOptional(t *testing.T) {
	ctx := context.Background()
	got, err := PrepareTransferDirs(ctx, localTransfer, nil, []string{"/x/a"})
	if err != nil || got != ctx {
		t.Fatalf("PrepareTransferDirs(local) = %v, %v; want the same ctx and no error", got, err)
	}
}
