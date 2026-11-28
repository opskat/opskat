//go:build !windows

package localipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The socket's owner and root may connect, as they always could through its 0600 mode;
// any other user is turned away.
func TestPeerUIDAllowedAdmitsTheOwnerAndRootOnly(t *testing.T) {
	require.True(t, peerUIDAllowed(501, 501))
	require.True(t, peerUIDAllowed(0, 501), "sudo opsctl reaching the user's desktop app")
	require.False(t, peerUIDAllowed(502, 501))
}

func TestUnixStaleSocketAndPermissions(t *testing.T) {
	path := filepath.Join(ipcTestDir(t), "stale.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	stale.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())
	ln, err := Listen(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.NoError(t, ln.Close())
	_, err = os.Lstat(path)
	require.True(t, os.IsNotExist(err))
}

func TestUnixPreservesNonSocketPaths(t *testing.T) {
	dir := ipcTestDir(t)
	file := filepath.Join(dir, "ordinary.sock")
	require.NoError(t, os.WriteFile(file, []byte("keep"), 0600))
	link := filepath.Join(dir, "linked.sock")
	require.NoError(t, os.Symlink(file, link))
	for _, path := range []string{file, link} {
		_, err := Listen(path)
		require.Error(t, err)
		data, err := os.ReadFile(path) //nolint:gosec // Both paths were created in this test's temporary directory.
		require.NoError(t, err)
		require.Equal(t, "keep", string(data))
	}
}
