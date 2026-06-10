package zmodem_svc

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/pkg/transfer"
)

// collector 是线程安全的进度收集器，供并发测试使用。
type collector struct {
	mu sync.Mutex
	ps []transfer.Progress
}

func (c *collector) emit(p transfer.Progress) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ps = append(c.ps, p)
}

func (c *collector) statuses(transferID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, p := range c.ps {
		if p.TransferID == transferID {
			out = append(out, p.Status)
		}
	}
	return out
}

func TestBeginAppendFinish(t *testing.T) {
	c := &collector{}
	b := New(c.emit)
	dst := filepath.Join(t.TempDir(), "out.bin")

	if err := b.BeginDownload("t1", dst, 6); err != nil {
		t.Fatalf("BeginDownload: %v", err)
	}
	if err := b.AppendChunk("t1", []byte("abc")); err != nil {
		t.Fatalf("AppendChunk 1: %v", err)
	}
	if err := b.AppendChunk("t1", []byte("def")); err != nil {
		t.Fatalf("AppendChunk 2: %v", err)
	}
	if err := b.FinishDownload("t1"); err != nil {
		t.Fatalf("FinishDownload: %v", err)
	}

	got, err := os.ReadFile(dst) //nolint:gosec // 测试临时文件
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "abcdef" {
		t.Fatalf("file content = %q, want abcdef", got)
	}
	if st := c.statuses("t1"); !slices.Contains(st, "progress") || st[len(st)-1] != "done" {
		t.Fatalf("statuses = %v, want progress... done", st)
	}
}

func TestAbortDownloadRemovesPartialFile(t *testing.T) {
	c := &collector{}
	b := New(c.emit)
	dst := filepath.Join(t.TempDir(), "partial.bin")

	if err := b.BeginDownload("t2", dst, 100); err != nil {
		t.Fatalf("BeginDownload: %v", err)
	}
	if err := b.AppendChunk("t2", []byte("half")); err != nil {
		t.Fatalf("AppendChunk: %v", err)
	}
	if err := b.AbortDownload("t2"); err != nil {
		t.Fatalf("AbortDownload: %v", err)
	}

	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("partial file should be removed, stat err = %v", err)
	}
	if st := c.statuses("t2"); !slices.Contains(st, transfer.StatusCancelled) {
		t.Fatalf("statuses = %v, want %s", st, transfer.StatusCancelled)
	}
}

func TestOpenReadChunkEOF(t *testing.T) {
	c := &collector{}
	b := New(c.emit)
	src := filepath.Join(t.TempDir(), "src.bin")
	want := bytes.Repeat([]byte("xyz"), 1000) // 3000 bytes
	if err := os.WriteFile(src, want, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	size, _, err := b.OpenUpload("u1", src)
	if err != nil {
		t.Fatalf("OpenUpload: %v", err)
	}
	if size != int64(len(want)) {
		t.Fatalf("size = %d, want %d", size, len(want))
	}

	var got []byte
	for {
		data, eof, err := b.ReadChunk("u1", 512)
		if err != nil {
			t.Fatalf("ReadChunk: %v", err)
		}
		got = append(got, data...)
		if eof {
			break
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("read back %d bytes, want %d (equal=%v)", len(got), len(want), bytes.Equal(got, want))
	}
	if err := b.FinishUpload("u1"); err != nil {
		t.Fatalf("FinishUpload: %v", err)
	}
	if st := c.statuses("u1"); st[len(st)-1] != "done" {
		t.Fatalf("statuses = %v, want ...done", st)
	}
}

func TestConcurrentTransfers(t *testing.T) {
	c := &collector{}
	b := New(c.emit)
	dir := t.TempDir()

	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := filepath.Base(filepath.Join(dir, string(rune('a'+i)))) // 唯一 transferID
			dst := filepath.Join(dir, id+".bin")
			payload := bytes.Repeat([]byte{byte('A' + i)}, 5000)
			if err := b.BeginDownload(id, dst, int64(len(payload))); err != nil {
				t.Errorf("BeginDownload %s: %v", id, err)
				return
			}
			for off := 0; off < len(payload); off += 1000 {
				if err := b.AppendChunk(id, payload[off:off+1000]); err != nil {
					t.Errorf("AppendChunk %s: %v", id, err)
					return
				}
			}
			if err := b.FinishDownload(id); err != nil {
				t.Errorf("FinishDownload %s: %v", id, err)
				return
			}
			got, err := os.ReadFile(dst) //nolint:gosec // 测试临时文件
			if err != nil {
				t.Errorf("ReadFile %s: %v", id, err)
				return
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("file %s content mismatch", id)
			}
		}(i)
	}
	wg.Wait()
}

func TestIdempotentClose(t *testing.T) {
	c := &collector{}
	b := New(c.emit)
	dst := filepath.Join(t.TempDir(), "idem.bin")

	if err := b.BeginDownload("d", dst, 1); err != nil {
		t.Fatalf("BeginDownload: %v", err)
	}
	if err := b.FinishDownload("d"); err != nil {
		t.Fatalf("FinishDownload 1: %v", err)
	}
	if err := b.FinishDownload("d"); err != nil {
		t.Fatalf("FinishDownload 2 (idempotent): %v", err)
	}
	if err := b.AbortDownload("d"); err != nil {
		t.Fatalf("AbortDownload (idempotent): %v", err)
	}

	src := filepath.Join(t.TempDir(), "idem-src.bin")
	if err := os.WriteFile(src, []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := b.OpenUpload("u", src); err != nil {
		t.Fatalf("OpenUpload: %v", err)
	}
	if err := b.FinishUpload("u"); err != nil {
		t.Fatalf("FinishUpload 1: %v", err)
	}
	if err := b.FinishUpload("u"); err != nil {
		t.Fatalf("FinishUpload 2 (idempotent): %v", err)
	}
	if err := b.AbortUpload("u"); err != nil {
		t.Fatalf("AbortUpload (idempotent): %v", err)
	}
}
