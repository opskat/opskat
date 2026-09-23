package approval

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/localipc"
	"github.com/stretchr/testify/require"
)

// A request's context ends when the requesting process goes away, so a desktop
// dialog waiting on it can close instead of lingering for a caller that is gone.
func TestHandlerContextEndsWhenClientDisconnects(t *testing.T) {
	dir, err := os.MkdirTemp("", "approval-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := SocketPath(dir)

	started := make(chan struct{})
	ended := make(chan struct{})
	server := NewServer(func(ctx context.Context, _ ApprovalRequest) ApprovalResponse {
		close(started)
		select {
		case <-ctx.Done():
			close(ended)
		case <-time.After(5 * time.Second):
		}
		return ApprovalResponse{}
	}, "")
	require.NoError(t, server.Start(path))
	t.Cleanup(server.Stop)

	conn, err := localipc.Dial(path)
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(conn).Encode(ApprovalRequest{Type: "mfa"}))
	<-started
	require.NoError(t, conn.Close())

	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("handler context was not canceled after the client disconnected")
	}
}
