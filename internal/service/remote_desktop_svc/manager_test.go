package remote_desktop_svc

import (
	"io"
	"net"
	"testing"
	"time"
)

// 用 net.Pipe 注入假 conn(client 端给 Session,server 端扮演 VNC 服务器),
// 验证:server 写入 → onData 收到;Session.write → server 读到;close → onClose。
func TestSessionPumpForwardsAndWrites(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = server.Close() }()

	s := &Session{ID: "t1", conn: client}
	got := make(chan []byte, 1)
	closed := make(chan struct{})
	s.start(func(b []byte) { got <- b }, func() { close(closed) })

	// server → client(session):onData 应收到相同字节
	go func() { _, _ = server.Write([]byte("RFB 003.008\n")) }()
	select {
	case b := <-got:
		if string(b) != "RFB 003.008\n" {
			t.Fatalf("onData = %q, want RFB greeting", b)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onData")
	}

	// session.write → server 应读到相同字节(net.Pipe 同步,需并发读)
	readBack := make(chan string, 1)
	go func() {
		buf := make([]byte, 5)
		n, _ := io.ReadFull(server, buf)
		readBack <- string(buf[:n])
	}()
	if err := s.write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if v := <-readBack; v != "hello" {
		t.Fatalf("server read = %q, want hello", v)
	}

	// close 关闭 conn → 读 pump 退出 → onClose 触发一次
	s.close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for onClose after close")
	}
}

func TestManagerWriteUnknownSession(t *testing.T) {
	m := &Manager{sessions: map[string]*Session{}}
	if err := m.Write("nope", []byte("x")); err == nil {
		t.Fatal("expected error for unknown session")
	}
}
