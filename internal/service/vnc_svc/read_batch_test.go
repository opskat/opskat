package vnc_svc

import (
	"bytes"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// 服务器连续写出大量小块 RFB 数据时，onData（→ 一次 vnc:data 事件）应被攒成少数几批，
// 字节内容与顺序不变；连接断开时尾部在 onClose 之前全部送达。
func TestReadPumpBatchesSmallReadsAndFlushesTailBeforeClose(t *testing.T) {
	const n = 500
	client, server := net.Pipe()

	var want bytes.Buffer
	for i := 0; i < n; i++ {
		_, _ = fmt.Fprintf(&want, "rect %03d;", i)
	}

	var mu sync.Mutex
	var calls int
	var got bytes.Buffer
	closed := make(chan []byte, 1)
	s := &Session{ID: "batch", conn: client}
	s.start(func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		got.Write(b)
	}, func() {
		mu.Lock()
		defer mu.Unlock()
		closed <- append([]byte(nil), got.Bytes()...)
	}, nil)

	go func() {
		for i := 0; i < n; i++ {
			if _, err := fmt.Fprintf(server, "rect %03d;", i); err != nil {
				return
			}
		}
		_ = server.Close()
	}()

	select {
	case atClose := <-closed:
		assert.Equal(t, want.String(), string(atClose), "all bytes, in order, must be delivered before onClose")
	case <-time.After(5 * time.Second):
		t.Fatal("server close did not trigger onClose")
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("onData calls for %d small writes: %d", n, calls)
	assert.Less(t, calls, n/10, "small RFB reads must be coalesced, not emitted one by one")
}
