package transfer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateIDUniqueAndPrefixed(t *testing.T) {
	a := GenerateID("sftp")
	b := GenerateID("sftp")
	if a == b {
		t.Fatalf("expected unique ids, got %q == %q", a, b)
	}
	if !strings.HasPrefix(a, "sftp-") {
		t.Fatalf("expected prefix sftp-, got %q", a)
	}
	if z := GenerateID("zmodem"); !strings.HasPrefix(z, "zmodem-") {
		t.Fatalf("expected prefix zmodem-, got %q", z)
	}
}

func TestReporterThrottleAndSpeed(t *testing.T) {
	cur := time.Unix(0, 0)
	clock := func() time.Time { return cur }
	var got []Progress
	r := newReporter(func(p Progress) { got = append(got, p) }, clock)

	// t=0: 首条 progress 立即放行（此时 elapsed=0，速率仍为 0）。
	r.Report(Progress{Status: "progress", BytesDone: 100})
	// t=50ms: 距上次不足 100ms，被节流丢弃。
	cur = cur.Add(50 * time.Millisecond)
	r.Report(Progress{Status: "progress", BytesDone: 200})
	// t=150ms: 放行，平均速率 = 300 bytes / 0.15s = 2000 bytes/s。
	cur = cur.Add(100 * time.Millisecond)
	r.Report(Progress{Status: "progress", BytesDone: 300})

	if len(got) != 2 {
		t.Fatalf("want 2 progress emits (throttled), got %d", len(got))
	}
	if got[0].Speed != 0 {
		t.Fatalf("want first speed 0, got %d", got[0].Speed)
	}
	if got[1].Speed != 2000 {
		t.Fatalf("want speed 2000, got %d", got[1].Speed)
	}
}

func TestReporterEmitsTerminalImmediately(t *testing.T) {
	cur := time.Unix(0, 0)
	clock := func() time.Time { return cur }
	var statuses []string
	r := newReporter(func(p Progress) { statuses = append(statuses, p.Status) }, clock)

	r.Report(Progress{Status: "progress", BytesDone: 1}) // 立即
	r.Report(Progress{Status: "progress", BytesDone: 2}) // 同一时刻，被节流
	r.Report(Progress{Status: "done"})                   // 终态，不受节流，立即

	if len(statuses) != 2 || statuses[0] != "progress" || statuses[1] != "done" {
		t.Fatalf("want [progress done], got %v", statuses)
	}
}

func TestTrackerKeepsTransferTotalAcrossFiles(t *testing.T) {
	// 3 个 100 字节的文件：无论传到第几个，BytesTotal 都必须是整次传输的 300，
	// BytesDone 必须单调递增且不越过总量。回归 #272：目录上传曾把"当前文件大小"
	// 当成 bytesTotal 传下去，导致进度条走到 250%。
	cur := time.Unix(0, 0)
	clock := func() time.Time { return cur }
	var got []Progress
	tr := newTracker("sftp-1", 3, 300, func(p Progress) { got = append(got, p) }, clock)

	for i := range 3 {
		r := tr.Reader(context.Background(), bytes.NewReader(bytes.Repeat([]byte("x"), 100)), fmt.Sprintf("f%d.bin", i), 100)
		for {
			cur = cur.Add(200 * time.Millisecond) // 越过节流窗口，保证每次读都上报
			if _, err := r.Read(make([]byte, 50)); err != nil {
				break
			}
		}
		tr.FileDone(100)
	}

	require.Len(t, got, 6)
	var prev int64
	for _, p := range got {
		assert.Equal(t, int64(300), p.BytesTotal, "BytesTotal 必须是整次传输总量")
		assert.Equal(t, 3, p.FilesTotal)
		assert.GreaterOrEqual(t, p.BytesDone, prev, "BytesDone 必须单调递增")
		assert.LessOrEqual(t, p.BytesDone, p.BytesTotal, "BytesDone 不得越过总量")
		prev = p.BytesDone
	}
	assert.Equal(t, int64(300), got[len(got)-1].BytesDone)
	assert.Equal(t, "f2.bin", got[len(got)-1].CurrentFile)
	assert.Equal(t, 2, got[len(got)-1].FilesCompleted)
}

func TestTrackerReaderSizeIsCurrentFileNotTransferTotal(t *testing.T) {
	// Size() 是 pkg/sftp ReadFrom 判断并发窗口的依据，必须是当前文件大小；
	// 若误报成整次传输总量，ReadFrom 会按错误的长度切分并发写。
	tr := NewTracker("sftp-1", 3, 300, func(Progress) {})
	r := tr.Reader(context.Background(), bytes.NewReader(make([]byte, 100)), "f0.bin", 100)
	assert.Equal(t, int64(100), r.Size())
}

func TestTrackerSpeedSpansWholeTransfer(t *testing.T) {
	// 整次传输共用一个 Reporter：换文件不重置计时起点。
	// 回归 #272：原实现每个文件 new 一个 Reporter，第 N 个文件的首条上报会用
	// "累计字节 / 刚过去的几毫秒"算出 TB/s 级速率。
	cur := time.Unix(0, 0)
	clock := func() time.Time { return cur }
	var got []Progress
	tr := newTracker("sftp-1", 2, 1000, func(p Progress) { got = append(got, p) }, clock)

	r0 := tr.Reader(context.Background(), bytes.NewReader(make([]byte, 500)), "a.bin", 500)
	cur = cur.Add(5 * time.Second)
	_, err := r0.Read(make([]byte, 500))
	require.NoError(t, err)
	tr.FileDone(500)

	// 第二个文件只花 1ms —— 若各文件独立计时，速率会是 500B/0.001s = 500KB/s。
	r1 := tr.Reader(context.Background(), bytes.NewReader(make([]byte, 500)), "b.bin", 500)
	cur = cur.Add(5 * time.Second)
	_, err = r1.Read(make([]byte, 500))
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, int64(100), got[len(got)-1].Speed, "1000 字节 / 10 秒 = 100 B/s")
}

func TestTrackerWriterReportsAgainstTransferTotal(t *testing.T) {
	// 下载侧同理：downloadFileAtomic 原先根本没有整次总量这个参数，
	// 每个文件都把 BytesTotal 算成 "已完成 + 当前文件"，进度条每个文件都跑满 100%。
	cur := time.Unix(0, 0)
	clock := func() time.Time { return cur }
	var got []Progress
	tr := newTracker("sftp-2", 2, 200, func(p Progress) { got = append(got, p) }, clock)

	tr.FileDone(100) // 第一个文件已完成
	w := tr.Writer(context.Background(), io.Discard, "b.bin")
	cur = cur.Add(200 * time.Millisecond)
	_, err := w.Write(make([]byte, 60))
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Equal(t, int64(200), got[0].BytesTotal)
	assert.Equal(t, int64(160), got[0].BytesDone)
	assert.Equal(t, 1, got[0].FilesCompleted)
}

func TestTrackerWriterAbortsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := NewTracker("sftp-2", 1, 4, func(Progress) {}).Writer(ctx, io.Discard, "b.bin")

	_, err := w.Write([]byte("data"))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestProgressReaderReportsCumulativeBytes(t *testing.T) {
	var got []Progress
	src := bytes.NewReader(bytes.Repeat([]byte("x"), 100))
	pr := NewProgressReader(context.Background(), "oss-1", "hero.jpg", src, 100, func(p Progress) {
		got = append(got, p)
	})

	buf := make([]byte, 40)
	n, err := pr.Read(buf) // 首条 progress 立即放行（lastEmit 零值）
	require.NoError(t, err)
	require.Equal(t, 40, n)
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, "oss-1", last.TransferID)
	assert.Equal(t, StatusProgress, last.Status)
	assert.Equal(t, "hero.jpg", last.CurrentFile)
	assert.Equal(t, int64(40), last.BytesDone)
	assert.Equal(t, int64(100), last.BytesTotal)
}

func TestProgressReaderAbortsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr := NewProgressReader(ctx, "oss-1", "hero.jpg", bytes.NewReader([]byte("data")), 4, func(Progress) {})

	_, err := pr.Read(make([]byte, 4))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestCopyStreamsAllBytesAndReports(t *testing.T) {
	src := bytes.NewReader(bytes.Repeat([]byte("y"), 70*1024)) // >2 个 32KB 分片
	var dst bytes.Buffer
	var got []Progress
	err := Copy(context.Background(), "oss-2", &dst, src, int64(70*1024), "big.bin", func(p Progress) {
		got = append(got, p)
	})
	require.NoError(t, err)
	assert.Equal(t, 70*1024, dst.Len())
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, StatusProgress, last.Status)
	assert.Equal(t, int64(70*1024), last.BytesTotal)
}

func TestCopyAbortsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Copy(ctx, "oss-2", &bytes.Buffer{}, bytes.NewReader([]byte("data")), 4, "x", func(Progress) {})
	assert.ErrorIs(t, err, context.Canceled)
}
