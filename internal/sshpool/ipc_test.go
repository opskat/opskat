package sshpool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/opskat/opskat/internal/localipc"
	"github.com/stretchr/testify/require"
)

func poolTestPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pool-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return SocketPath(dir)
}

func TestIPCProxyAuthentication(t *testing.T) {
	path := poolTestPath(t)
	// An unknown operation exercises the real handshake without dialing SSH.
	server := NewServer(nil, "test-token")
	require.NoError(t, server.Start(path))
	t.Cleanup(server.Stop)
	for _, token := range []string{"", "wrong", "test-token"} {
		client := NewClientWithToken(path, token)
		require.True(t, client.IsAvailable())
		_, _, err := client.handshake(ProxyRequest{Op: "probe"})
		if token == "test-token" {
			require.ErrorContains(t, err, "unknown op: probe")
		} else {
			require.ErrorContains(t, err, "authentication failed")
		}
	}
	server.Stop()
	require.False(t, NewClient(path).IsAvailable())
}

func TestIPCProxyFrames(t *testing.T) {
	path := poolTestPath(t)
	ln, err := localipc.Listen(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	// A protocol peer replaces remote SSH only; the production client handshake,
	// stream framing and local OS transport are exercised unmodified.
	request := make(chan ProxyRequest, 1)
	done := make(chan error, 1)
	payload := bytes.Repeat([]byte("output\x00\xff"), 6000)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			done <- err
			return
		}
		var req ProxyRequest
		if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
			done <- err
			return
		}
		request <- req
		if err := json.NewEncoder(conn).Encode(ProxyResponse{OK: true}); err != nil {
			done <- err
			return
		}
		if err := WriteFrame(conn, FrameStdout, payload); err != nil {
			done <- err
			return
		}
		if err := WriteFrame(conn, FrameStderr, []byte("stderr")); err != nil {
			done <- err
			return
		}
		done <- WriteExitCode(conn, 7)
	}()
	var stdout, stderr bytes.Buffer
	code, err := NewClientWithToken(path, "test-token").Exec(ProxyRequest{AssetID: 42, Command: "probe"}, nil, &stdout, &stderr)
	require.NoError(t, err)
	require.Equal(t, 7, code)
	require.Equal(t, payload, stdout.Bytes())
	require.Equal(t, "stderr", stderr.String())
	require.Equal(t, ProxyRequest{Token: "test-token", Op: "exec", AssetID: 42, Command: "probe"}, <-request)
	require.NoError(t, <-done)
}
