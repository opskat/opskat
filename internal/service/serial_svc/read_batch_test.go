package serial_svc

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 端口读到一块数据后进入阻塞读（真实串口在 SetReadTimeout 到期前都不返回）：
// 已读到的数据必须在攒批窗口内送达，而不是等下一次 Read 返回后才 flush。
func TestReadOutputFlushesPendingWhileReadBlocks(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	first := true
	port := &fakePort{readFn: func(buf []byte) (int, error) {
		if first {
			first = false
			return copy(buf, "login: "), nil
		}
		select {
		case <-release:
			return 0, io.EOF
		case <-time.After(time.Second): // 模拟一次长读超时
			return 0, nil
		}
	}}
	got := make(chan string, 4)
	sess := &Session{ID: "serial-batch", port: port, onData: func(b []byte) { got <- string(b) }}
	mgr := NewManager()
	mgr.sessions.Store(sess.ID, sess)

	done := make(chan struct{})
	go func() {
		mgr.readOutput(sess)
		close(done)
	}()
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		<-done
	})

	select {
	case s := <-got:
		assert.Equal(t, "login: ", s)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("pending serial output was not flushed while the port read was blocked")
	}
}

// 大量小块读取被攒批，字节内容与顺序不变；EOF 时尾部在 onClosed 之前全部送达；
// AI 命令捕获仍逐块即时收到数据。
func TestReadOutputBatchesSmallReadsAndFlushesTailBeforeClose(t *testing.T) {
	const n = 500
	var want bytes.Buffer
	chunks := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		c := []byte(fmt.Sprintf("tty %03d\r\n", i))
		want.Write(c)
		chunks = append(chunks, c)
	}
	port := &fakePort{readFn: func(buf []byte) (int, error) {
		if len(chunks) == 0 {
			return 0, io.EOF
		}
		n := copy(buf, chunks[0])
		chunks = chunks[1:]
		return n, nil
	}}

	var mu sync.Mutex
	var calls int
	var got bytes.Buffer
	closed := make(chan []byte, 1)
	capture := newCommandCapture()
	sess := &Session{
		ID:   "serial-tail",
		port: port,
		onData: func(b []byte) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			got.Write(b)
		},
		onClosed: func(string) {
			mu.Lock()
			defer mu.Unlock()
			closed <- append([]byte(nil), got.Bytes()...)
		},
		cmdCapture: capture,
	}
	mgr := NewManager()
	mgr.sessions.Store(sess.ID, sess)
	mgr.readOutput(sess)

	select {
	case atClose := <-closed:
		assert.Equal(t, want.String(), string(atClose), "all bytes, in order, must be delivered before onClosed")
	case <-time.After(5 * time.Second):
		t.Fatal("EOF did not trigger onClosed")
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("onData calls for %d small reads: %d", n, calls)
	assert.Less(t, calls, n/10, "small serial reads must be coalesced")
	require.Equal(t, want.String(), string(capture.Drain()), "command capture still sees every chunk")
}
