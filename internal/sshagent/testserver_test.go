package sshagent

import (
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// testAgentServer serves the ssh-agent protocol over a real unix socket in a
// temp dir, so peer-credential checks see a real peer.
type testAgentServer struct {
	ln   net.Listener
	path string
}

// newUnixAgentServer accepts connections on a unix socket and runs handler in
// a goroutine for each one. The listener is closed on test cleanup.
func newUnixAgentServer(t *testing.T, handler func(net.Conn)) *testAgentServer {
	t.Helper()
	// Keep the socket name short: macOS limits unix socket paths to 104 bytes
	// and t.TempDir() names can be long.
	path := filepath.Join(t.TempDir(), "a")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	s := &testAgentServer{ln: ln, path: path}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				handler(conn)
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

// keyringServer serves a well-behaved in-memory agent holding the given keys.
// connClosed fires when the client closes the transport (ServeAgent returns).
func keyringServer(t *testing.T, keys ...agent.AddedKey) (*testAgentServer, chan struct{}) {
	t.Helper()
	kr := agent.NewKeyring()
	for _, k := range keys {
		if err := kr.Add(k); err != nil {
			t.Fatalf("add key to keyring: %v", err)
		}
	}
	connClosed := make(chan struct{})
	srv := newUnixAgentServer(t, func(c net.Conn) {
		defer close(connClosed)
		_ = agent.ServeAgent(kr, c)
	})
	return srv, connClosed
}

// identitiesAnswer mirrors the wire layout of SSH_AGENT_IDENTITIES_ANSWER so
// tests can craft arbitrary (including malicious) replies.
type identitiesAnswer struct {
	NumKeys uint32 `sshtype:"12"`
	Keys    []byte `ssh:"rest"`
}

// identitiesResponse builds a raw SSH_AGENT_IDENTITIES_ANSWER packet body.
func identitiesResponse(numKeys uint32, records ...[]byte) []byte {
	return ssh.Marshal(&identitiesAnswer{NumKeys: numKeys, Keys: concatBytes(records)})
}

// keyRecord builds one identity record (blob + comment) as the agent puts it
// on the wire.
func keyRecord(blob []byte, comment string) []byte {
	return ssh.Marshal(&struct {
		Blob    []byte
		Comment string
	}{Blob: blob, Comment: comment})
}

// wireKeyBlob builds a public-key blob that the agent client can parse but
// that need not be a valid SSH public key (used to reach payload validation).
func wireKeyBlob(format string, rest []byte) []byte {
	return ssh.Marshal(&struct {
		Format string
		Rest   []byte `ssh:"rest"`
	}{Format: format, Rest: rest})
}

func concatBytes(parts [][]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// writeAgentResponse writes a length-prefixed agent protocol packet.
func writeAgentResponse(c net.Conn, body []byte) error {
	pkt := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(pkt, uint32(len(body)))
	copy(pkt[4:], body)
	_, err := c.Write(pkt)
	return err
}

// readAgentRequest reads one length-prefixed agent request.
func readAgentRequest(c net.Conn) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(c, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	req := make([]byte, n)
	if _, err := io.ReadFull(c, req); err != nil {
		return nil, err
	}
	return req, nil
}

// respondThenWaitClose writes a single crafted response and then blocks until
// the client closes the transport. Combined with a tracked close channel this
// observes whether the client's transport was closed.
func respondThenWaitClose(body []byte) func(net.Conn) {
	return func(c net.Conn) {
		if _, err := readAgentRequest(c); err != nil {
			return
		}
		if err := writeAgentResponse(c, body); err != nil {
			return
		}
		var b [1]byte
		_, _ = c.Read(b[:])
	}
}

// serveDelayed reads the request and never replies, holding the connection
// open until the client closes it.
func serveDelayed(c net.Conn) {
	if _, err := readAgentRequest(c); err != nil {
		return
	}
	var b [1]byte
	_, _ = c.Read(b[:])
}
