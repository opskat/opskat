package oss_svc_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/pkg/transfer"
	oss_svc "github.com/opskat/opskat/internal/service/oss_svc"
	"github.com/opskat/opskat/internal/service/oss_svc/mock_ossclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func body(s string) (io.ReadCloser, int64, error) {
	return io.NopCloser(strings.NewReader(s)), int64(len(s)), nil
}

func readLocal(t *testing.T, elems ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(elems...)) //nolint:gosec // 测试自己的临时目录
	require.NoError(t, err)
	return string(data)
}

// 文件夹落在 <保存目录>/<前缀末段>/ 下并保留层级:跨页的对象、深层子文件夹、
// 只有目录标记的空文件夹都要到位,已存在的同名文件被覆盖。
func TestDownloadPrefixWithMirrorsFolderIntoDestDir(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "data/logs/", 200, "").Return(&oss_svc.ListObjectsPage{
		Items: []oss_svc.ObjectItem{
			{Key: "data/logs/", IsPrefix: true},
			{Key: "data/logs/a.txt", Size: 5},
			{Key: "data/logs/empty/", IsPrefix: true},
			{Key: "data/logs/sub/", IsPrefix: true},
		},
		IsTruncated: true, NextContinuationToken: "page-2",
	}, nil)
	c.EXPECT().ListObjects(gomock.Any(), "b", "data/logs/", 200, "page-2").Return(page(
		oss_svc.ObjectItem{Key: "data/logs/z.txt", Size: 3},
	), nil)
	c.EXPECT().ListObjects(gomock.Any(), "b", "data/logs/empty/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "data/logs/empty/", IsPrefix: true},
	), nil)
	c.EXPECT().ListObjects(gomock.Any(), "b", "data/logs/sub/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "data/logs/sub/deep.bin", Size: 4},
	), nil)
	c.EXPECT().GetObject(gomock.Any(), "b", "data/logs/a.txt").Return(body("alpha"))
	c.EXPECT().GetObject(gomock.Any(), "b", "data/logs/z.txt").Return(body("zed"))
	c.EXPECT().GetObject(gomock.Any(), "b", "data/logs/sub/deep.bin").Return(body("deep"))

	dest := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dest, "logs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "logs", "a.txt"), []byte("stale and longer"), 0o644))

	var events []transfer.Progress
	err := oss_svc.DownloadPrefixWith(context.Background(), c, &oss_svc.DownloadPrefixRequest{
		Bucket: "b", Prefix: "data/logs/", DestDir: dest, TransferID: "oss-1",
		OnProgress: func(p transfer.Progress) { events = append(events, p) },
	})
	require.NoError(t, err)

	assert.Equal(t, "alpha", readLocal(t, dest, "logs", "a.txt"))
	assert.Equal(t, "zed", readLocal(t, dest, "logs", "z.txt"))
	assert.Equal(t, "deep", readLocal(t, dest, "logs", "sub", "deep.bin"))
	info, err := os.Stat(filepath.Join(dest, "logs", "empty"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// 总量在扫描阶段一次定下:整次传输是 3 个文件、12 字节,而不是"当前文件"的大小。
	require.NotEmpty(t, events)
	assert.Equal(t, "oss-1", events[0].TransferID)
	assert.Equal(t, 3, events[0].FilesTotal)
	assert.Equal(t, int64(12), events[0].BytesTotal)
}

// 对象 key 是任意字符串。会落到保存目录之外、或与另一个 key 落到同一路径的 key
// 必须在动笔之前就被拒绝:一个字节都不读,保存目录里什么都不留。
func TestDownloadPrefixWithRejectsKeysThatDoNotMapInsideDestDir(t *testing.T) {
	cases := map[string]oss_svc.ObjectItem{
		"parent traversal": {Key: "logs/../", IsPrefix: true},
		"empty segment":    {Key: "logs//", IsPrefix: true},
		"dot segment":      {Key: "logs/./x.txt", Size: 1},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			c := mock_ossclient.NewMockClient(ctrl)
			c.EXPECT().ListObjects(gomock.Any(), "b", "logs/", 200, "").Return(page(
				oss_svc.ObjectItem{Key: "logs/ok.txt", Size: 2}, item,
			), nil)

			dest := t.TempDir()
			err := oss_svc.DownloadPrefixWith(context.Background(), c, &oss_svc.DownloadPrefixRequest{
				Bucket: "b", Prefix: "logs/", DestDir: dest, TransferID: "oss-1",
				OnProgress: func(transfer.Progress) {},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), item.Key)

			left, readErr := os.ReadDir(dest)
			require.NoError(t, readErr)
			assert.Empty(t, left)
		})
	}
}

// 任一对象失败即整次中止并把原因交回;已经落盘的文件保留。
func TestDownloadPrefixWithStopsAtFirstFailedObject(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "logs/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "logs/a.txt", Size: 5},
		oss_svc.ObjectItem{Key: "logs/b.txt", Size: 5},
		oss_svc.ObjectItem{Key: "logs/c.txt", Size: 5},
	), nil)
	denied := errors.New("access denied")
	c.EXPECT().GetObject(gomock.Any(), "b", "logs/a.txt").Return(body("alpha"))
	c.EXPECT().GetObject(gomock.Any(), "b", "logs/b.txt").Return(nil, int64(0), denied)

	dest := t.TempDir()
	err := oss_svc.DownloadPrefixWith(context.Background(), c, &oss_svc.DownloadPrefixRequest{
		Bucket: "b", Prefix: "logs/", DestDir: dest, TransferID: "oss-1",
		OnProgress: func(transfer.Progress) {},
	})
	require.ErrorIs(t, err, denied)
	assert.Contains(t, err.Error(), "logs/b.txt")
	assert.Equal(t, "alpha", readLocal(t, dest, "logs", "a.txt"))
}

// cancelOnRead 在第一次被读时取消 ctx,模拟"用户在某个对象传到一半时点了取消"。
type cancelOnRead struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelOnRead) Read(p []byte) (int, error) {
	r.cancel()
	return r.Reader.Read(p)
}

func TestDownloadPrefixWithStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "logs/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "logs/a.txt", Size: 5},
		oss_svc.ObjectItem{Key: "logs/b.txt", Size: 5},
	), nil)
	c.EXPECT().GetObject(gomock.Any(), "b", "logs/a.txt").Return(
		io.NopCloser(cancelOnRead{Reader: strings.NewReader("alpha"), cancel: cancel}), int64(5), nil)

	err := oss_svc.DownloadPrefixWith(ctx, c, &oss_svc.DownloadPrefixRequest{
		Bucket: "b", Prefix: "logs/", DestDir: t.TempDir(), TransferID: "oss-1",
		OnProgress: func(transfer.Progress) {},
	})
	require.ErrorIs(t, err, context.Canceled)
}
