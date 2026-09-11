//go:build windows

package localipc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWindowsDirectoryAliasesAndIsolation(t *testing.T) {
	dir := ipcTestDir(t)
	alias := filepath.Join(ipcTestDir(t), "junction")
	out, err := exec.Command("cmd", "/c", "mklink", "/J", alias, dir).CombinedOutput()
	require.NoError(t, err, string(out))
	name, sid, err := pipeIdentity(filepath.Join(dir, "approval.sock"))
	require.NoError(t, err)
	for _, equivalent := range []string{alias, strings.ToUpper(dir), filepath.Join(dir, ".")} {
		got, gotSID, err := pipeIdentity(filepath.Join(equivalent, "approval.sock"))
		require.NoError(t, err)
		require.Equal(t, name, got)
		require.Equal(t, sid, gotSID)
	}
	for _, distinct := range []string{filepath.Join(dir, "sshpool.sock"), filepath.Join(ipcTestDir(t), "approval.sock")} {
		got, _, err := pipeIdentity(distinct)
		require.NoError(t, err)
		require.NotEqual(t, name, got)
	}
	ln, err := Listen(filepath.Join(dir, "approval.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_, err = c.Write([]byte("alias\n"))
			_ = c.Close()
		}
		done <- err
	}()
	c, err := Dial(filepath.Join(alias, "approval.sock"))
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.NoError(t, c.SetReadDeadline(time.Now().Add(time.Second)))
	got, err := bufio.NewReader(c).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "alias\n", got)
	require.NoError(t, <-done)

	// Inspect the actual kernel object's DACL, not just a config string.
	fd, ok := c.(interface{ Fd() uintptr })
	require.True(t, ok)
	sd, err := windows.GetSecurityInfo(windows.Handle(fd.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	require.Equal(t, user.User.Sid.String(), sid)
	// Compare the actual DACL with one for the independently resolved process
	// user. Windows can render a local administrator SID as the SDDL alias LA.
	want, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	require.NoError(t, err)
	require.Equal(t, want.String(), sd.String())
}

func TestWindowsLegacyFileAndLongUnicodeDirectory(t *testing.T) {
	dir := filepath.Join(ipcTestDir(t), strings.Repeat("segment-", 16), "目录 with spaces")
	require.NoError(t, os.MkdirAll(dir, 0700))
	path := filepath.Join(dir, "approval.sock")
	require.NoError(t, os.WriteFile(path, []byte("legacy endpoint left untouched"), 0600))
	ln, err := Listen(path)
	require.NoError(t, err)
	require.NoError(t, ln.Close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "legacy endpoint left untouched", string(data))
}

func TestWindowsBusyDialDeadline(t *testing.T) {
	path := filepath.Join(ipcTestDir(t), "busy.sock")
	ln, err := Listen(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	// No Accept: the pipe exists but has no available instance.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = DialContext(ctx, path)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWindowsCrashReleasesPipe(t *testing.T) {
	const envKey = "OPSKAT_TEST_PIPE_CRASH"
	if path := os.Getenv(envKey); path != "" {
		ln, err := Listen(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		fmt.Println("ready")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	path := filepath.Join(ipcTestDir(t), "crash.sock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsCrashReleasesPipe$")
	cmd.Env = append(os.Environ(), envKey+"="+path)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	defer func() { _ = input.Close() }()
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(output).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		require.Equal(t, "ready\n", line)
	case <-time.After(10 * time.Second):
		t.Fatal("child did not start listening")
	}
	require.NoError(t, cmd.Process.Kill())
	require.Error(t, cmd.Wait())
	ln, err := Listen(path)
	require.NoError(t, err)
	require.NoError(t, ln.Close())
}
