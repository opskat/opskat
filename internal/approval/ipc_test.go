package approval

import (
	"path/filepath"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the real transport and token boundary without assets or credentials.
func TestIPCApprovalAuthenticationAndDecision(t *testing.T) {
	dir, err := os.MkdirTemp("", "approval-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := SocketPath(dir)
	var calls atomic.Int32
	server := NewServer(func(req ApprovalRequest) ApprovalResponse {
		calls.Add(1)
		return ApprovalResponse{Approved: req.Command == "allow", Reason: req.Command, SessionID: req.SessionID}
	}, "test-token")
	require.NoError(t, server.Start(path))
	t.Cleanup(server.Stop)

	for _, token := range []string{"", "wrong-token"} {
		resp, err := RequestApprovalWithToken(path, token, ApprovalRequest{Command: "allow"})
		require.NoError(t, err)
		require.False(t, resp.Approved)
		require.Equal(t, "authentication failed", resp.Reason)
	}
	require.Zero(t, calls.Load())
	for _, command := range []string{"deny", "allow"} {
		resp, err := RequestApprovalWithToken(path, "test-token", ApprovalRequest{Command: command, SessionID: "session"})
		require.NoError(t, err)
		require.Equal(t, command == "allow", resp.Approved)
		require.Equal(t, "session", resp.SessionID)
	}
	require.EqualValues(t, 2, calls.Load())

	other := NewServer(func(ApprovalRequest) ApprovalResponse { return ApprovalResponse{} }, "test-token")
	require.Error(t, other.Start(path))
	server.Stop()
	_, err := RequestApprovalWithToken(path, "test-token", ApprovalRequest{})
	require.Error(t, err)
	// A fresh server can reuse the endpoint after shutdown.
	require.NoError(t, other.Start(path))
	t.Cleanup(other.Stop)
	_, err = RequestApproval(filepath.Join(dir, "absent.sock"), ApprovalRequest{})
	require.Error(t, err)
}
