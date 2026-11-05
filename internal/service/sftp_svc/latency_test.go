package sftp_svc

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
)

const fxpRealpath = 16

// probeConn counts client->server REALPATH requests and delays every write by rtt.
type probeConn struct {
	net.Conn
	rtt      time.Duration
	mu       sync.Mutex
	buf      []byte
	realpath atomic.Int64
	pending  chan delayed
}

func (c *probeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.buf = append(c.buf, p...)
	for len(c.buf) >= 5 {
		n := int(binary.BigEndian.Uint32(c.buf))
		if len(c.buf) < 4+n {
			break
		}
		if c.buf[4] == fxpRealpath {
			c.realpath.Add(1)
		}
		c.buf = c.buf[4+n:]
	}
	c.mu.Unlock()
	if c.rtt <= 0 {
		return c.Conn.Write(p)
	}
	// Delay delivery without blocking the sender, like a real link with latency.
	c.pending <- delayed{data: append([]byte(nil), p...), at: time.Now().Add(c.rtt)}
	return len(p), nil
}

type delayed struct {
	data []byte
	at   time.Time
}

func (c *probeConn) forward() {
	for d := range c.pending {
		time.Sleep(time.Until(d.at))
		_, _ = c.Conn.Write(d.data)
	}
}

func newProbeClient(t *testing.T, root string, rtt time.Duration) (*sftp.Client, *probeConn, func()) {
	t.Helper()
	cc, sc := net.Pipe()
	server, err := sftp.NewServer(sc, sftp.WithServerWorkingDirectory(root))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	pc := &probeConn{Conn: cc, pending: make(chan delayed, 1024)}
	go pc.forward()
	client, err := sftp.NewClientPipe(pc, pc)
	require.NoError(t, err)
	pc.rtt = rtt
	return client, pc, func() { _ = server.Close(); <-done }
}

func TestListDirDoesNotProbeWithRealpath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o600))
	client, pc, closeServer := newProbeClient(t, root, 0)
	t.Cleanup(func() { _ = client.Close(); closeServer() })
	svc := NewService(nil)
	svc.clients.Store("s", client)

	before := pc.realpath.Load()
	_, err := svc.ListDir("s", ".")
	require.NoError(t, err)
	require.Equal(t, int64(0), pc.realpath.Load()-before)
}

func TestListDirResolvesSymlinksConcurrentlyInOrder(t *testing.T) {
	root := t.TempDir()
	const n = 48
	require.NoError(t, os.Mkdir(filepath.Join(root, "real"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0o600))
	wantDir := map[string]bool{"real": true, "file": false, "broken": false}
	for i := 0; i < n; i++ {
		target := "real"
		if i%2 == 1 {
			target = "file"
		}
		name := fmt.Sprintf("l%02d", i)
		require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
		wantDir[name] = i%2 == 0
	}
	require.NoError(t, os.Symlink("missing", filepath.Join(root, "broken")))

	rtt := 10 * time.Millisecond
	client, _, closeServer := newProbeClient(t, root, rtt)
	t.Cleanup(func() { _ = client.Close(); closeServer() })
	svc := NewService(nil)
	svc.clients.Store("s", client)

	start := time.Now()
	entries, err := svc.ListDir("s", ".")
	elapsed := time.Since(start)
	require.NoError(t, err)
	t.Logf("elapsed=%s for %d symlinks at rtt=%s", elapsed, n+1, rtt)
	require.Less(t, elapsed, time.Duration(n)*rtt/3, "symlink stats must overlap")

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, fmt.Sprintf("%s:%v", e.Name, e.IsDir))
	}
	// Dirs first then files; within each group keep the server's ReadDir order.
	raw, err := client.ReadDir(".")
	require.NoError(t, err)
	var wantNames []string
	for _, group := range []bool{true, false} {
		for _, fi := range raw {
			if wantDir[fi.Name()] == group {
				wantNames = append(wantNames, fmt.Sprintf("%s:%v", fi.Name(), group))
			}
		}
	}
	require.Equal(t, wantNames, names)
}

func TestClientEvictedWhenConnectionEnds(t *testing.T) {
	client, _, closeServer := newProbeClient(t, t.TempDir(), 0)
	svc := NewService(nil)
	svc.cacheClient("s", client)

	closeServer() // server side ends -> client.Wait returns
	require.Eventually(t, func() bool {
		_, ok := svc.clients.Load("s")
		return !ok
	}, 2*time.Second, 5*time.Millisecond)
	_ = client.Close()
}

func TestEvictionDoesNotRemoveNewerClient(t *testing.T) {
	old, _, closeOld := newProbeClient(t, t.TempDir(), 0)
	svc := NewService(nil)
	svc.cacheClient("s", old)
	newer, _, closeNew := newProbeClient(t, t.TempDir(), 0)
	t.Cleanup(func() { _ = newer.Close(); closeNew() })
	svc.cacheClient("s", newer)

	closeOld()
	_ = old.Close()
	time.Sleep(100 * time.Millisecond)
	v, ok := svc.clients.Load("s")
	require.True(t, ok)
	require.Same(t, newer, v.(*sftp.Client))
}
