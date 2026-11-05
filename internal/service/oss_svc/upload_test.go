package oss_svc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/pkg/transfer"
)

// multipartStub 是一个只会做分片上传的 S3 桩:记录 UploadPart 的最大同时在途数,
// 每个分片人为延迟 partDelay,让"串行"与"并行"在在途数上分得开。
type multipartStub struct {
	partDelay time.Duration

	inFlight    atomic.Int32
	maxInFlight atomic.Int32
	parts       atomic.Int32
	singlePuts  atomic.Int32
}

func (s *multipartStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `<InitiateMultipartUploadResult><Bucket>bkt</Bucket><Key>big.bin</Key><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`)
	case r.Method == http.MethodPut && q.Has("partNumber"):
		n := s.inFlight.Add(1)
		for {
			m := s.maxInFlight.Load()
			if n <= m || s.maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(s.partDelay)
		s.inFlight.Add(-1)
		s.parts.Add(1)
		w.Header().Set("ETag", `"etag-`+q.Get("partNumber")+`"`)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprint(w, `<CompleteMultipartUploadResult><Bucket>bkt</Bucket><Key>big.bin</Key><ETag>"done"</ETag></CompleteMultipartUploadResult>`)
	case r.Method == http.MethodPut:
		_, _ = io.Copy(io.Discard, r.Body)
		s.singlePuts.Add(1)
		w.Header().Set("ETag", `"single"`)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusBadRequest)
	}
}

func newStubMinioClient(t *testing.T, h http.Handler, partSizeMB int) Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	require.NoError(t, err)
	mc, err := minio.New(endpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4("access", "secret", ""),
		Secure: false,
		Region: "us-east-1",
	})
	require.NoError(t, err)
	return newMinioAdapter(mc, partSizeMB)
}

// recordProgress 收集进度事件;minio 的分片 worker 会并发读源,回调需要加锁收集。
func recordProgress() (func(transfer.Progress), func() []transfer.Progress) {
	var mu sync.Mutex
	var events []transfer.Progress
	return func(p transfer.Progress) {
			mu.Lock()
			events = append(events, p)
			mu.Unlock()
		}, func() []transfer.Progress {
			mu.Lock()
			defer mu.Unlock()
			return events
		}
}

func assertExactFinalProgress(t *testing.T, events []transfer.Progress, name string, size int64) {
	t.Helper()
	require.NotEmpty(t, events)
	last := events[len(events)-1]
	assert.Equal(t, transfer.StatusProgress, last.Status)
	assert.Equal(t, name, last.CurrentFile)
	assert.Equal(t, 1, last.FilesCompleted)
	assert.Equal(t, 1, last.FilesTotal)
	assert.EqualValues(t, size, last.BytesDone)
	assert.EqualValues(t, size, last.BytesTotal)
}

// 大于分片大小的文件要走 minio 的 ReadAt 并行分片(多个 UploadPart 同时在途),
// 并且进度的最后一条 progress 是精确总量。
func TestUploadFileWithSendsPartsConcurrentlyAndReportsExactTotals(t *testing.T) {
	const partSize = 5 << 20 // minio 允许的最小分片
	const size = 4*partSize + 123
	local := filepath.Join(t.TempDir(), "big.bin")
	require.NoError(t, os.WriteFile(local, make([]byte, size), 0o600))

	stub := &multipartStub{partDelay: 200 * time.Millisecond}
	c := newStubMinioClient(t, stub, 5)

	onProgress, events := recordProgress()
	err := uploadFileWith(context.Background(), c, &UploadFileRequest{
		Bucket: "bkt", Key: "big.bin", LocalPath: local, ContentType: "application/octet-stream", TransferID: "oss-up",
		OnProgress: onProgress,
	})
	require.NoError(t, err)

	t.Logf("UploadPart max in flight = %d (parts = %d)", stub.maxInFlight.Load(), stub.parts.Load())
	assert.EqualValues(t, 5, stub.parts.Load())
	assert.Greater(t, stub.maxInFlight.Load(), int32(1), "分片应并行上传")
	assertExactFinalProgress(t, events(), "big.bin", size)
}

// 不超过分片大小的文件仍是一次 PUT,进度同样以精确总量收尾。
func TestUploadFileWithSmallFileIsSinglePutWithExactTotals(t *testing.T) {
	const size = 1000
	local := filepath.Join(t.TempDir(), "small.txt")
	require.NoError(t, os.WriteFile(local, make([]byte, size), 0o600))

	stub := &multipartStub{}
	c := newStubMinioClient(t, stub, 5)

	onProgress, events := recordProgress()
	err := uploadFileWith(context.Background(), c, &UploadFileRequest{
		Bucket: "bkt", Key: "small.txt", LocalPath: local, ContentType: "text/plain", TransferID: "oss-up",
		OnProgress: onProgress,
	})
	require.NoError(t, err)

	assert.EqualValues(t, 1, stub.singlePuts.Load())
	assert.EqualValues(t, 0, stub.parts.Load())
	assertExactFinalProgress(t, events(), "small.txt", size)
}
