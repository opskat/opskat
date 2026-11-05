package transfer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackerFlushesExactTotalsWhenLastFileCompletesInsideThrottleWindow(t *testing.T) {
	// 时钟不动：首条 progress 之后的所有 progress 都落在 100ms 节流窗口内被丢弃。
	// 最后一个文件完成时，前端收到的最后一条 progress 仍须带整次传输的精确总量。
	clock := func() time.Time { return time.Unix(0, 0) }
	var got []Progress
	tr := newTracker("t", 3, 30, func(p Progress) { got = append(got, p) }, clock)

	for i := range 3 {
		f := tr.File(fmt.Sprintf("f%d", i), 10)
		_, err := io.Copy(io.Discard, struct{ io.Reader }{f.Reader(context.Background(), bytes.NewReader(make([]byte, 10)))})
		require.NoError(t, err)
		f.Done()
	}

	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, StatusProgress, last.Status)
	assert.Equal(t, 3, last.FilesCompleted)
	assert.EqualValues(t, 30, last.BytesDone)
	assert.EqualValues(t, 30, last.BytesTotal)
}

func TestTrackerDoesNotFlushCompleteTotalsBeforeAllFilesDone(t *testing.T) {
	clock := func() time.Time { return time.Unix(0, 0) }
	var got []Progress
	tr := newTracker("t", 2, 20, func(p Progress) { got = append(got, p) }, clock)

	f := tr.File("a", 10)
	_, err := io.Copy(io.Discard, struct{ io.Reader }{f.Reader(context.Background(), bytes.NewReader(make([]byte, 10)))})
	require.NoError(t, err)
	f.Done()

	for _, p := range got {
		assert.Less(t, p.FilesCompleted, 2)
	}
}

func TestCopyFlushesExactTotalsInsideThrottleWindow(t *testing.T) {
	var got []Progress
	// 3 个分片落在首条 progress 的节流窗口内（真实时钟下毫秒级完成）。
	src := bytes.NewReader(make([]byte, 96*1024))
	require.NoError(t, Copy(context.Background(), "c", io.Discard, src, 96*1024, "f", func(p Progress) { got = append(got, p) }))
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.EqualValues(t, 96*1024, last.BytesDone)
	assert.Equal(t, 1, last.FilesCompleted)
}

func TestCopyErrorDoesNotReportComplete(t *testing.T) {
	var got []Progress
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, Copy(ctx, "c", io.Discard, bytes.NewReader(make([]byte, 10)), 10, "f", func(p Progress) { got = append(got, p) }))
	assert.Empty(t, got)
}
