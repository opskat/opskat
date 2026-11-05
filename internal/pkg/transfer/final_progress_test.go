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
	// 非最后一个文件完成时仍受节流约束：时钟不动，Done 不得越过 100ms 窗口多发一条 progress。
	clock := func() time.Time { return time.Unix(0, 0) }
	var got []Progress
	tr := newTracker("t", 2, 20, func(p Progress) { got = append(got, p) }, clock)

	f := tr.File("a", 10)
	_, err := io.Copy(io.Discard, struct{ io.Reader }{f.Reader(context.Background(), bytes.NewReader(make([]byte, 10)))})
	require.NoError(t, err)
	before := len(got)
	f.Done()

	assert.Len(t, got, before, "未传完全部文件时，Done 不应绕过节流补发 progress")
}
