package sftp_svc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/opskat/opskat/internal/pkg/sftpio/sftpiotest"
	"github.com/opskat/opskat/internal/pkg/transfer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 快速完成的传输里，最后几条 progress 会落在 100ms 节流窗口内被丢弃；
// 返回前前端收到的最后一条 progress 必须带精确的整次总量，否则 done 之后仍显示"55/60"。
func assertLastProgressIsExact(t *testing.T, got []transfer.Progress, files int, bytes int64) {
	t.Helper()
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, transfer.StatusProgress, last.Status)
	assert.Equal(t, files, last.FilesCompleted)
	assert.Equal(t, files, last.FilesTotal)
	assert.Equal(t, bytes, last.BytesDone)
	assert.Equal(t, bytes, last.BytesTotal)
}

func TestUploadDirLastProgressCarriesExactTotals(t *testing.T) {
	service, session, root := newTestService(t)
	localDir := filepath.Join(t.TempDir(), "src")
	want := writeDirTree(t, localDir, 30)

	onProgress, got := collectProgress()
	require.NoError(t, service.UploadDir(context.Background(), "t1", session, localDir, sftpiotest.RemotePath(filepath.Join(root, "dst")), onProgress))
	assertLastProgressIsExact(t, *got, len(want), treeBytes(want))
}

func TestDownloadDirLastProgressCarriesExactTotals(t *testing.T) {
	service, session, root := newTestService(t)
	want := writeDirTree(t, filepath.Join(root, "remote"), 30)

	onProgress, got := collectProgress()
	require.NoError(t, service.DownloadDir(context.Background(), "t1", session, sftpiotest.RemotePath(filepath.Join(root, "remote")), filepath.Join(t.TempDir(), "dst"), onProgress))
	assertLastProgressIsExact(t, *got, len(want), treeBytes(want))
}
