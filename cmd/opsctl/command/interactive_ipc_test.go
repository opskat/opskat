package command

import (
	"os"
	"testing"

	"github.com/opskat/opskat/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestChooseApproverWithRealIPC(t *testing.T) {
	dir, err := os.MkdirTemp("", "approver-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := approval.SocketPath(dir)
	server := approval.NewServer(func(approval.ApprovalRequest) approval.ApprovalResponse {
		return approval.ApprovalResponse{}
	}, "test-token")
	require.NoError(t, server.Start(path))
	t.Cleanup(server.Stop)
	dial := func() error { return dialApprovalSocket(path) }
	require.Equal(t, approverDesktop, chooseApprover(false, dial))
	server.Stop()
	require.Equal(t, approverRefusal, chooseApprover(false, dial))
	require.Equal(t, approverTerminal, chooseApprover(true, dial))
}
