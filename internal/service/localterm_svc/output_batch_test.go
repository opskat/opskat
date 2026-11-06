package localterm_svc

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PTY 连续吐出大量小块输出时，onData（→ 一次 EventsEmit）应被攒成少数几批，
// 字节内容与顺序不变，且 EOF 前的尾部在 onClosed 之前全部送达。
func TestReadOutputBatchesSmallReadsAndFlushesTailBeforeClose(t *testing.T) {
	const n = 500
	proc := &fakeProc{readCh: make(chan []byte, n)}
	withFakeStartPTY(t, proc)

	var want bytes.Buffer
	for i := 0; i < n; i++ {
		chunk := []byte(fmt.Sprintf("out %03d\n", i))
		want.Write(chunk)
		proc.readCh <- chunk
	}
	// 关闭后 Read 先读完缓冲中的全部块再返回 EOF：模拟 shell 输出一大串后退出。
	require.NoError(t, proc.Close())

	mgr := NewManager()
	sid, err := mgr.Connect(ConnectConfig{AssetID: 3})
	require.NoError(t, err)

	var mu sync.Mutex
	var calls int
	var got bytes.Buffer
	closed := make(chan []byte, 1)
	mgr.SetCallbacks(sid, func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		got.Write(b)
	}, func(string) {
		mu.Lock()
		defer mu.Unlock()
		closed <- append([]byte(nil), got.Bytes()...)
	})

	select {
	case atClose := <-closed:
		assert.Equal(t, want.String(), string(atClose), "all bytes, in order, must be delivered before onClosed")
	case <-time.After(5 * time.Second):
		t.Fatal("EOF did not trigger onClosed")
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("onData calls for %d small reads: %d", n, calls)
	assert.Less(t, calls, n/10, "small PTY reads must be coalesced, not emitted one by one")
}
