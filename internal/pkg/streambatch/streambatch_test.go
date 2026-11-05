package streambatch

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder 收集 flush 回调拿到的每一批数据。
type recorder struct {
	mu      sync.Mutex
	batches [][]byte
}

func (r *recorder) flush(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, p)
}

func (r *recorder) snapshot() ([][]byte, []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.batches...), bytes.Join(r.batches, nil)
}

// countTimerArms 包一层 afterFunc，统计计时器被启动的次数——空闲时它是唯一的唤醒来源。
func countTimerArms(t *testing.T) *atomic.Int64 {
	t.Helper()
	var arms atomic.Int64
	orig := afterFunc
	afterFunc = func(d time.Duration, f func()) *time.Timer {
		arms.Add(1)
		return orig(d, f)
	}
	t.Cleanup(func() { afterFunc = orig })
	return &arms
}

func TestManySmallAddsWithinOneWindowFlushOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		b := New(10*time.Millisecond, 32*1024, rec.flush)

		var want bytes.Buffer
		for i := 0; i < 1000; i++ {
			line := fmt.Sprintf("line %04d\n", i)
			want.WriteString(line)
			b.Add([]byte(line))
		}
		batches, _ := rec.snapshot()
		assert.Empty(t, batches, "flush must wait for the window, not fire per Add")

		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		batches, got := rec.snapshot()
		assert.Len(t, batches, 1, "1000 small adds in one window must coalesce into one flush")
		assert.Equal(t, want.String(), string(got))
	})
}

func TestAddsSpreadOverTimeKeepBytesAndOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		b := New(10*time.Millisecond, 32*1024, rec.flush)

		var want bytes.Buffer
		for i := 0; i < 100; i++ {
			line := fmt.Sprintf("%d,", i)
			want.WriteString(line)
			b.Add([]byte(line))
			time.Sleep(time.Millisecond)
		}
		b.Close()

		batches, got := rec.snapshot()
		assert.Equal(t, want.String(), string(got))
		// 100 次写入跨 100ms，10ms 窗口 → 约 10 批（不会是 100 批）。
		assert.LessOrEqual(t, len(batches), 11)
		assert.GreaterOrEqual(t, len(batches), 9)
	})
}

func TestReachingByteCapFlushesImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		b := New(10*time.Millisecond, 4096, rec.flush)

		chunk := bytes.Repeat([]byte("x"), 1024)
		for i := 0; i < 4; i++ {
			b.Add(chunk)
		}
		batches, _ := rec.snapshot()
		require.Len(t, batches, 1, "reaching the byte cap must flush synchronously, before the window")
		assert.Len(t, batches[0], 4096)

		b.Add([]byte("tail"))
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		batches, got := rec.snapshot()
		assert.Len(t, batches, 2)
		assert.Equal(t, append(bytes.Repeat([]byte("x"), 4096), "tail"...), got)
	})
}

func TestIdleBatcherArmsNoTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		arms := countTimerArms(t)
		rec := &recorder{}
		b := New(10*time.Millisecond, 32*1024, rec.flush)

		// 从未写入：一小时里不应启动任何计时器。
		time.Sleep(time.Hour)
		assert.Equal(t, int64(0), arms.Load(), "a batcher that never received data must not arm a timer")

		for i := 0; i < 1000; i++ {
			b.Add([]byte("a"))
		}
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()
		assert.Equal(t, int64(1), arms.Load(), "one burst arms exactly one timer")

		// flush 之后再空闲一小时：计时器已停，不再启动，也不再回调。
		time.Sleep(time.Hour)
		synctest.Wait()
		assert.Equal(t, int64(1), arms.Load(), "no timer may run while idle after a flush")
		batches, _ := rec.snapshot()
		assert.Len(t, batches, 1)
		assert.False(t, b.timerPending())
	})
}

func TestCloseFlushesTailAndStopsTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		b := New(10*time.Millisecond, 32*1024, rec.flush)

		b.Add([]byte("partial "))
		b.Add([]byte("tail"))
		b.Close()

		batches, got := rec.snapshot()
		assert.Len(t, batches, 1, "Close must deliver pending bytes synchronously")
		assert.Equal(t, "partial tail", string(got))
		assert.False(t, b.timerPending())

		// 计时器已停：窗口过后不会再有空回调或重复投递。
		time.Sleep(time.Hour)
		synctest.Wait()
		batches, _ = rec.snapshot()
		assert.Len(t, batches, 1)
	})
}

func TestCloseWithoutDataDoesNotFlush(t *testing.T) {
	rec := &recorder{}
	b := New(10*time.Millisecond, 32*1024, rec.flush)
	b.Close()
	batches, _ := rec.snapshot()
	assert.Empty(t, batches)
}

// 真实时钟下并发：计时器回调与写入方的封顶 flush 交错，投递顺序必须与 Add 顺序一致。
func TestConcurrentTimerAndCapFlushPreserveOrder(t *testing.T) {
	rec := &recorder{}
	b := New(time.Millisecond, 64, rec.flush)

	var want bytes.Buffer
	for i := 0; i < 5000; i++ {
		s := fmt.Sprintf("%d;", i)
		want.WriteString(s)
		b.Add([]byte(s))
		if i%97 == 0 {
			time.Sleep(200 * time.Microsecond)
		}
	}
	b.Close()
	_, got := rec.snapshot()
	assert.Equal(t, want.String(), string(got))
}
