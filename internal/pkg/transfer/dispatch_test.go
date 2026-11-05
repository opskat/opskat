package transfer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dispatchEntry struct {
	name  string
	isDir bool
}

func isDispatchDir(e dispatchEntry) bool { return e.isDir }

// Dispatch 同时在传 Workers 个条目，且不超过这个数：每个 send 都要等到这么多个同时在传才
// 放行，并发度不足时卡到超时失败，与机器快慢无关。
func TestDispatchRunsWorkersConcurrently(t *testing.T) {
	entries := make([]dispatchEntry, 0, 1+Workers*3)
	entries = append(entries, dispatchEntry{name: "d", isDir: true})
	for i := range Workers * 3 {
		entries = append(entries, dispatchEntry{name: fmt.Sprintf("d/f%02d", i)})
	}
	var inFlight, peak, sent atomic.Int32
	full := make(chan struct{})
	var fullOnce sync.Once
	send := func(ctx context.Context, _ dispatchEntry) error {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		if n == Workers {
			fullOnce.Do(func() { close(full) })
		}
		select {
		case <-full:
			sent.Add(1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, Dispatch(ctx, entries, isDispatchDir, func(dispatchEntry) error { return nil }, send))
	assert.EqualValues(t, Workers, peak.Load())
	assert.EqualValues(t, Workers*3, sent.Load())
}

// 目录建不出来时，排在它后面的条目一个都不派发，返回的是 mkdir 的错误。
func TestDispatchStopsAtFailedMkdir(t *testing.T) {
	boom := errors.New("mkdir failed")
	entries := []dispatchEntry{{name: "a"}, {name: "d", isDir: true}, {name: "d/b"}}
	var sent []string
	var mu sync.Mutex
	err := Dispatch(context.Background(), entries, isDispatchDir,
		func(dispatchEntry) error { return boom },
		func(_ context.Context, e dispatchEntry) error {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, e.name)
			return nil
		})
	require.ErrorIs(t, err, boom)
	assert.NotContains(t, sent, "d/b")
}
