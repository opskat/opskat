package approval

import (
	"os"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/localipc"
	"github.com/stretchr/testify/require"
)

func TestServerStopInterruptsConnectedClient(t *testing.T) {
	server := NewServer(func(ApprovalRequest) ApprovalResponse { return ApprovalResponse{} }, "")
	dir, err := os.MkdirTemp("", "opskat-approval-")
	if err != nil {
		t.Fatalf("create socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "server.sock")
	if err := server.Start(socketPath); err != nil {
		t.Fatalf("start server: %v", err)
	}

	conn, err := localipc.Dial(socketPath)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	require.Eventually(t, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.conns) == 1
	}, time.Second, time.Millisecond)

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
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	var b [1]byte
	_, readErr := conn.Read(b[:])
	require.Error(t, readErr)
	if netErr, ok := readErr.(net.Error); ok {
		require.False(t, netErr.Timeout(), "Stop must disconnect the accepted client")
	}
}
