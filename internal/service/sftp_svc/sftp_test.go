package sftp_svc

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
)

func TestListDirTreatsSymlinkToDirectoryAsDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	require.NoError(t, os.Symlink("target", filepath.Join(root, "data")))

	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn, sftp.WithServerWorkingDirectory(root))
	require.NoError(t, err)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Serve()
	}()

	client, err := sftp.NewClientPipe(clientConn, clientConn)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		require.NoError(t, server.Close())
		<-serverDone
	})

	service := NewService(nil)
	service.clients.Store("session", client)

	entries, err := service.ListDir("session", ".")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var dataEntry *FileEntry
	for i := range entries {
		if entries[i].Name == "data" {
			dataEntry = &entries[i]
			break
		}
	}
	require.NotNil(t, dataEntry)
	require.True(t, dataEntry.IsDir, "a symlink whose target is a directory must be navigable")
}
