//go:build !windows

package localipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
