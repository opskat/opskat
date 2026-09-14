package localipc

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ipcTestDir(t *testing.T) string {
	t.Helper()
	// Keep Unix addresses below sun_path even on macOS's long temp root.
	dir, err := os.MkdirTemp("", "ipc-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	return dir
}

func TestStreamLifecycle(t *testing.T) {
	path := filepath.Join(ipcTestDir(t), "test.sock")
	ln, err := Listen(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	other, err := Listen(path)
	if other != nil {
		_ = other.Close()
	}
	require.Error(t, err, "a second listener must not replace the first")

	payload := bytes.Repeat([]byte("stream\x00\xff"), 20000)
	done := make(chan error, 1)
	go func() {
		// Unix duplicate-listener detection opens then closes a probe connection.
		for {
			conn, err := ln.Accept()
			if err != nil {
				done <- err
				return
			}
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				_ = conn.Close()
				done <- err
				return
			}
			buf := make([]byte, len(payload))
			_, err = io.ReadFull(conn, buf)
			if err == io.EOF {
				_ = conn.Close()
				continue
			}
			if err == nil {
				_, err = conn.Write(buf)
			}
			_ = conn.Close()
			done <- err
			return
		}
	}()
	conn, err := Dial(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = conn.Write(payload)
	require.NoError(t, err)
	got := make([]byte, len(payload))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	require.Equal(t, payload, got)
	require.NoError(t, <-done)

	acceptDone := make(chan error, 1)
	go func() { _, err := ln.Accept(); acceptDone <- err }()
	require.NoError(t, ln.Close())
	select {
	case err := <-acceptDone:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("closing listener did not interrupt Accept")
	}
	_, err = Dial(path)
	require.Error(t, err)
	ln2, err := Listen(path)
	require.NoError(t, err)
	require.NoError(t, ln2.Close())
}

func TestDialCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DialContext(ctx, filepath.Join(ipcTestDir(t), "absent.sock"))
	require.ErrorIs(t, err, context.Canceled)
}
