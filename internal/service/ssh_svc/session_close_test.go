package ssh_svc

import (
	"bytes"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// freezableConn models a remote that stops draining the TCP connection (process
// SIGSTOPped, send buffer full): once frozen, every Write blocks until Close.
type freezableConn struct {
	net.Conn
	frozen    atomic.Bool
	blocked   chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *freezableConn) Write(p []byte) (int, error) {
	if c.frozen.Load() {
		select {
		case c.blocked <- struct{}{}:
		default:
		}
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *freezableConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// startEchoShellServer runs an in-process sshd that accepts pty-req/shell and
// echoes each session's stdin back to its stdout.
func startEchoShellServer(t *testing.T) string {
	t.Helper()
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(newTestHostKey(t))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(raw, cfg)
				if err != nil {
					_ = raw.Close()
					return
				}
				defer func() { _ = sc.Close() }()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					ch, chReqs, err := nc.Accept()
					if err != nil {
						continue
					}
					go func() {
						for req := range chReqs {
							if req.WantReply {
								_ = req.Reply(req.Type == "pty-req" || req.Type == "shell", nil)
							}
						}
					}()
					go func() {
						defer func() { _ = ch.Close() }()
						_, _ = io.Copy(ch, ch)
					}()
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func dialFreezableClient(t *testing.T, addr string) (*ssh.Client, *freezableConn) {
	t.Helper()
	raw, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	conn := &freezableConn{Conn: raw, blocked: make(chan struct{}, 1), closed: make(chan struct{})}
	t.Cleanup(func() { _ = conn.Close() })
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User:            "u",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	require.NoError(t, err)
	return ssh.NewClient(c, chans, reqs), conn
}

// outputSink collects terminal output delivered through the session callback.
type outputSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *outputSink) onData(_ string, data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf.Write(data)
}

func (o *outputSink) contains(s string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return bytes.Contains(o.buf.Bytes(), []byte(s))
}

func clientDone(client *ssh.Client) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		_ = client.Wait()
		close(done)
	}()
	return done
}

func TestSession_CloseWithWriteBlockedOnFrozenTransport(t *testing.T) {
	client, conn := dialFreezableClient(t, startEchoShellServer(t))
	m := NewManager()
	shared := newSharedClient(client, nil, 0)
	sess, err := m.createSession(shared, 1, 80, 24, func(string, []byte) {}, nil, nil, "")
	require.NoError(t, err)
	siblingID, err := m.NewSessionFrom(sess.ID, 80, 24, func(string, []byte) {}, nil, nil)
	require.NoError(t, err)
	sibling, ok := m.GetSession(siblingID)
	require.True(t, ok)

	conn.frozen.Store(true)
	writeDone := make(chan error, 1)
	// Several SSH packets, like a large paste: x/crypto reports the transport
	// failure from the packet after the one stuck on the wire.
	go func() { writeDone <- sess.Write(bytes.Repeat([]byte("x"), 256<<10)) }()
	select {
	case <-conn.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("Write never reached the transport")
	}

	start := time.Now()
	closed := make(chan struct{})
	go func() {
		m.Disconnect(sess.ID)
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(sessionCloseTimeout + time.Second):
		t.Fatalf("Disconnect still blocked after %s while a Write is stuck on the transport", time.Since(start))
	}

	select {
	case err := <-writeDone:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("blocked Write was not released by closing the connection")
	}
	select {
	case <-clientDone(client):
	case <-time.After(time.Second):
		t.Fatal("wedged connection was not closed")
	}
	// Accepted cost: the sibling pane on the wedged connection ends through its
	// read loop, and its release must not close the client a second time.
	// readOutput deregisters the sibling only after its Close (and release) returned.
	require.Eventually(t, func() bool {
		_, ok := m.GetSession(siblingID)
		return !ok
	}, 2*time.Second, 10*time.Millisecond)
	assert.True(t, sibling.IsClosed())
	shared.mu.Lock()
	defer shared.mu.Unlock()
	assert.True(t, shared.closed)
	assert.Equal(t, 0, shared.refCount)
}

func TestSession_HealthyCloseKeepsSharedClientForSibling(t *testing.T) {
	client, _ := dialFreezableClient(t, startEchoShellServer(t))
	m := NewManager()
	shared := newSharedClient(client, nil, 0)
	sess, err := m.createSession(shared, 1, 80, 24, func(string, []byte) {}, nil, nil, "")
	require.NoError(t, err)
	out := &outputSink{}
	siblingID, err := m.NewSessionFrom(sess.ID, 80, 24, out.onData, nil, nil)
	require.NoError(t, err)
	sibling, ok := m.GetSession(siblingID)
	require.True(t, ok)

	start := time.Now()
	m.Disconnect(sess.ID)
	assert.Less(t, time.Since(start), sessionCloseTimeout, "healthy close must not wait for the force-close timeout")
	assert.True(t, sess.IsClosed())

	require.NoError(t, sibling.Write([]byte("still-alive")))
	require.Eventually(t, func() bool { return out.contains("still-alive") }, 2*time.Second, 10*time.Millisecond,
		"sibling pane must keep working on the shared client")
	assert.False(t, sibling.IsClosed())

	done := clientDone(client)
	select {
	case <-done:
		t.Fatal("shared client closed while a sibling still references it")
	default:
	}

	m.Disconnect(siblingID)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("last release did not close the shared client")
	}
}
