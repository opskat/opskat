package approval

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerStopInterruptsConnectedClient(t *testing.T) {
	server := NewServer(func(ApprovalRequest) ApprovalResponse { return ApprovalResponse{} }, "")
	dir, err := os.MkdirTemp("/tmp", "opskat-approval-")
	if err != nil {
		t.Fatalf("create socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "server.sock")
	if err := server.Start(socketPath); err != nil {
		t.Fatalf("start server: %v", err)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer conn.Close()

	stopped := make(chan struct{})
	go func() {
		server.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked on a connected client")
	}
}
