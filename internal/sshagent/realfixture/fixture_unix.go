//go:build !windows

package realfixture

import (
	"crypto/ed25519"
	"io"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/opskat/opskat/internal/sshagent"
)

// unixSocketListener listens on a short unix socket path and runs handler per
// connection. macOS limits unix socket paths to 104 bytes, so the path must
// stay short. The returned cleanup removes the temp directory holding the
// socket.
func unixSocketListener(handler func(io.ReadWriteCloser)) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "opskat-agent-")
	if err != nil {
		return "", nil, err
	}
	path = filepath.Join(dir, "a")
	ln, err := net.Listen("unix", path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c io.ReadWriteCloser) {
				defer func() { _ = c.Close() }()
				handler(c)
			}(conn)
		}
	}()
	return path, func() {
		_ = ln.Close()
		_ = os.RemoveAll(dir)
	}, nil
}

// unixKeyringSource serves a well-behaved in-memory keyring over a real unix
// socket.
func unixKeyringSource(keys ...agent.AddedKey) (sshagent.Source, func(), error) {
	kr := agent.NewKeyring()
	for _, k := range keys {
		if err := kr.Add(k); err != nil {
			return sshagent.Source{}, nil, err
		}
	}
	path, cleanup, err := unixSocketListener(func(c io.ReadWriteCloser) { _ = agent.ServeAgent(kr, c) })
	if err != nil {
		return sshagent.Source{}, nil, err
	}
	return sshagent.Source{Type: sshagent.EndpointTypeUnixSocket, Value: path}, cleanup, nil
}

// unixRejectingSource lists pub as the only identity but answers every sign
// request with SSH_AGENT_FAILURE, over a real unix socket.
func unixRejectingSource(pub ssh.PublicKey) (sshagent.Source, func(), error) {
	path, cleanup, err := unixSocketListener(func(c io.ReadWriteCloser) {
		for {
			req, err := readAgentRequest(c)
			if err != nil || len(req) == 0 {
				return
			}
			switch req[0] {
			case 11: // SSH_AGENTC_REQUEST_IDENTITIES
				if err := writeAgentResponse(c, identitiesResponse(1, keyRecord(pub.Marshal(), "fixture"))); err != nil {
					return
				}
			case 13: // SSH_AGENTC_SIGN_REQUEST
				if err := writeAgentResponse(c, []byte{5}); err != nil { // SSH_AGENT_FAILURE
					return
				}
			default:
				return
			}
		}
	})
	if err != nil {
		return sshagent.Source{}, nil, err
	}
	return sshagent.Source{Type: sshagent.EndpointTypeUnixSocket, Value: path}, cleanup, nil
}

// unixDelayedSource accepts a connection over a real unix socket and never
// answers the first request; ready closes once a request is in flight.
func unixDelayedSource() (sshagent.Source, <-chan struct{}, func(), error) {
	ready := make(chan struct{})
	path, cleanup, err := unixSocketListener(func(c io.ReadWriteCloser) {
		if _, err := readAgentRequest(c); err != nil {
			return
		}
		close(ready)
		var b [1]byte
		_, _ = c.Read(b[:]) // hold until the client cancels and closes
	})
	if err != nil {
		return sshagent.Source{}, nil, nil, err
	}
	return sshagent.Source{Type: sshagent.EndpointTypeUnixSocket, Value: path}, ready, cleanup, nil
}

// unixSelfAgent serves a keyring holding priv over a real unix socket. It is
// the documented fallback when no system ssh-agent binary exists.
func unixSelfAgent(priv ed25519.PrivateKey) (sshagent.Source, func(), error) {
	return unixKeyringSource(agent.AddedKey{PrivateKey: priv, Comment: "fixture-self"})
}
