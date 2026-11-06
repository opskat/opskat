//go:build !windows

package sftp_svc

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/sftpio/sftpiotest"
)

// 命名管道（FIFO）只在 Unix 上有，syscall.Mkfifo 在 Windows 上不存在。

func TestUploadRejectsNamedPipeTargetInsteadOfReplacingIt(t *testing.T) {
	// SFTP 的写请求带绝对偏移量，服务端对管道做 pwrite 会返回 illegal seek ——
	// 命名管道根本无法经 SFTP 写入。必须明确报错：既不能把管道 rename 成普通文件，
	// 也不能因为上游 ReadFrom 吞掉写错误而静默报告成功。
	service, session, root := newTestService(t)
	local := filepath.Join(t.TempDir(), "payload")
	require.NoError(t, os.WriteFile(local, []byte("hello"), 0o644))

	fifo := filepath.Join(root, "pipe")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	err := service.Upload(context.Background(), "t1", session, local, sftpiotest.RemotePath(fifo), discardProgress)

	require.Error(t, err, "SFTP 无法写入命名管道，不能报告成功")
	assert.Contains(t, err.Error(), "命名管道")
	info, err := os.Lstat(fifo)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeNamedPipe, "命名管道必须原样保留，不能被换成普通文件")
}
