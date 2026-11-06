package oss_svc_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// closeOnEOF 在源读到 EOF 时关闭 done:此时这个对象的字节已全部写进本地文件。
type closeOnEOF struct {
	io.Reader
	done chan struct{}
}

func (r closeOnEOF) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		close(r.done)
	}
	return n, err
}

// blockUntilCanceled 是一个"传不完"的对象:报告已开始,然后一直等到 ctx 被取消。
// 迟迟不取消说明 worker 没被叫停,测试直接失败而不是挂住。
func blockUntilCanceled(t *testing.T, started chan<- struct{}) func(context.Context, string, string) (io.ReadCloser, int64, error) {
	return func(ctx context.Context, _, _ string) (io.ReadCloser, int64, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(5 * time.Second):
			t.Error("在途对象没有被取消")
			return nil, 0, errors.New("not canceled")
		}
	}
}

// 任一对象失败即整次中止:返回的是这个失败而不是其余对象随之而来的取消,
// 还在传的对象被叫停,已经落盘的文件保留。
func TestDownloadPrefixWithStopsAtFirstFailedObject(t *testing.T) {
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	c.EXPECT().ListObjects(gomock.Any(), "b", "logs/", 200, "").Return(page(
		oss_svc.ObjectItem{Key: "logs/a.txt", Size: 5},
		oss_svc.ObjectItem{Key: "logs/b.txt", Size: 5},
		oss_svc.ObjectItem{Key: "logs/c.txt", Size: 5},
	), nil)
	denied := errors.New("access denied")
	aWritten := make(chan struct{})
	cStarted := make(chan struct{}, 1)
	c.EXPECT().GetObject(gomock.Any(), "b", "logs/a.txt").Return(
		io.NopCloser(closeOnEOF{Reader: strings.NewReader("alpha"), done: aWritten}), int64(5), nil)
	c.EXPECT().GetObject(gomock.Any(), "b", "logs/c.txt").DoAndReturn(blockUntilCanceled(t, cStarted))
	c.EXPECT().GetObject(gomock.Any(), "b", "logs/b.txt").DoAndReturn(
		func(context.Context, string, string) (io.ReadCloser, int64, error) {
			<-aWritten
			<-cStarted
			return nil, 0, denied
		})

	dest := t.TempDir()
	err := oss_svc.DownloadPrefixWith(context.Background(), c, &oss_svc.DownloadPrefixRequest{
		Bucket: "b", Prefix: "logs/", DestDir: dest, TransferID: "oss-1",
		OnProgress: func(transfer.Progress) {},
	})
	require.ErrorIs(t, err, denied)
	assert.Contains(t, err.Error(), "logs/b.txt")
	assert.Equal(t, "alpha", readLocal(t, dest, "logs", "a.txt"))
}

// 用户取消时所有在途对象一起停下,返回取消。
func TestDownloadPrefixWithStopsAllWorkersWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 8
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	items := make([]oss_svc.ObjectItem, 0, n)
	for i := range n {
		items = append(items, oss_svc.ObjectItem{Key: fmt.Sprintf("logs/f%d.txt", i), Size: 1})
	}
	c.EXPECT().ListObjects(gomock.Any(), "b", "logs/", 200, "").Return(page(items...), nil)
	started := make(chan struct{}, n)
	c.EXPECT().GetObject(gomock.Any(), "b", gomock.Any()).Times(n).DoAndReturn(blockUntilCanceled(t, started))
	go func() {
		for range n {
			<-started
		}
		cancel()
	}()

	err := oss_svc.DownloadPrefixWith(ctx, c, &oss_svc.DownloadPrefixRequest{
		Bucket: "b", Prefix: "logs/", DestDir: t.TempDir(), TransferID: "oss-1",
		OnProgress: func(transfer.Progress) {},
	})
	require.ErrorIs(t, err, context.Canceled)
}

// 每个对象的请求带固定延迟时,文件夹下载要让多个对象同时在传(上限 8 路),
// 总耗时远小于逐个串行;Tracker 的最后一条 progress 是精确的文件数与字节数。
func TestDownloadPrefixWithDownloadsObjectsConcurrently(t *testing.T) {
	const n = 16
	const latency = 100 * time.Millisecond
	ctrl := gomock.NewController(t)
	c := mock_ossclient.NewMockClient(ctrl)
	items := make([]oss_svc.ObjectItem, 0, n)
	for i := range n {
		items = append(items, oss_svc.ObjectItem{Key: fmt.Sprintf("logs/f%02d.txt", i), Size: 3})
	}
	c.EXPECT().ListObjects(gomock.Any(), "b", "logs/", 200, "").Return(page(items...), nil)
	var inFlight, maxInFlight atomic.Int32
	c.EXPECT().GetObject(gomock.Any(), "b", gomock.Any()).Times(n).DoAndReturn(
		func(context.Context, string, string) (io.ReadCloser, int64, error) {
			cur := inFlight.Add(1)
			defer inFlight.Add(-1)
			for m := maxInFlight.Load(); cur > m && !maxInFlight.CompareAndSwap(m, cur); m = maxInFlight.Load() {
			}
			time.Sleep(latency)
			return body("abc")
		})

	var mu sync.Mutex
	var events []transfer.Progress
	dest := t.TempDir()
	start := time.Now()
	err := oss_svc.DownloadPrefixWith(context.Background(), c, &oss_svc.DownloadPrefixRequest{
		Bucket: "b", Prefix: "logs/", DestDir: dest, TransferID: "oss-1",
		OnProgress: func(p transfer.Progress) {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		},
	})
	elapsed := time.Since(start)
	require.NoError(t, err)

	t.Logf("%d objects x %v latency: elapsed %v, max in flight %d", n, latency, elapsed, maxInFlight.Load())
	assert.Greater(t, maxInFlight.Load(), int32(1), "对象应并发下载")
	assert.LessOrEqual(t, maxInFlight.Load(), int32(8), "并发上限是 8 路")
	assert.Less(t, elapsed, n*latency/2)
	for _, it := range items {
		assert.Equal(t, "abc", readLocal(t, dest, filepath.FromSlash(it.Key)))
	}
	require.NotEmpty(t, events)
	last := events[len(events)-1]
	assert.Equal(t, n, last.FilesCompleted)
	assert.EqualValues(t, 3*n, last.BytesDone)
	assert.EqualValues(t, 3*n, last.BytesTotal)
}
