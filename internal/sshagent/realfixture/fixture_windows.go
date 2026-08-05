//go:build windows

// Windows named-pipe fixtures. They serve the agent protocol over a local
// \\\\.\\pipe\\ instance in the same namespace OpenSSH's ssh-agent uses
// (OpenSSH-compatible named pipe), and are CI-only: they run under the race
// detector in the GitHub Actions windows job and are never exercised on
// macOS/Linux hosts.
package realfixture

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/sys/windows"

	"github.com/opskat/opskat/internal/sshagent"
)

var pipeSeq uint32

// pipeName returns a unique local named pipe in OpenSSH's \\\\.\\pipe\\
// namespace, so concurrent fixture runs never collide and the sshagent
// windows-named-pipe validation accepts it.
func pipeName() string {
	n := atomic.AddUint32(&pipeSeq, 1)
	return fmt.Sprintf(`\\.\pipe\opskat-realfixture-%d-%d`, os.Getpid(), n)
}

// pipeServer serves one agent protocol connection over a Windows named pipe.
// It creates the instance and issues an overlapped ConnectNamedPipe so the
// instance is listening before the client dials — a created-but-not-listening
// instance would otherwise surface as ERROR_PIPE_BUSY to the single-shot
// client dial in internal/sshagent. listening closes once the instance is
// accepting connections.
func pipeServer(name string, handler func(io.ReadWriteCloser)) (<-chan struct{}, error) {
	ready := make(chan struct{})
	go func() {
		p, perr := windows.UTF16PtrFromString(name)
		if perr != nil {
			return
		}
		h, herr := windows.CreateNamedPipe(p,
			windows.PIPE_ACCESS_DUPLEX,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			1, 4096, 4096, 0, nil)
		if herr != nil {
			return
		}
		ev, eerr := windows.CreateEvent(nil, 0, 0, nil)
		if eerr != nil {
			_ = windows.CloseHandle(h)
			return
		}
		ov := windows.Overlapped{HEvent: ev}
		if cerr := windows.ConnectNamedPipe(h, &ov); cerr == nil || cerr == windows.ERROR_PIPE_CONNECTED {
			// The client already connected.
		} else if cerr == windows.ERROR_IO_PENDING {
			s, werr := windows.WaitForSingleObject(ev, windows.INFINITE)
			if werr != nil || s != windows.WAIT_OBJECT_0 {
				_ = windows.CloseHandle(ev)
				_ = windows.CloseHandle(h)
				return
			}
		} else {
			_ = windows.CloseHandle(ev)
			_ = windows.CloseHandle(h)
			return
		}
		_ = windows.CloseHandle(ev)
		close(ready)
		f := os.NewFile(uintptr(h), name)
		defer func() { _ = f.Close() }()
		handler(f)
	}()
	return ready, nil
}

// waitListening blocks until the pipe instance is accepting connections.
func waitListening(listening <-chan struct{}) error {
	select {
	case <-listening:
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("named pipe never started listening")
	}
}

// pipeKeyringSource serves a well-behaved in-memory keyring over an
// OpenSSH-compatible named pipe.
func pipeKeyringSource(keys ...agent.AddedKey) (sshagent.Source, func(), error) {
	kr := agent.NewKeyring()
	for _, k := range keys {
		if err := kr.Add(k); err != nil {
			return sshagent.Source{}, nil, err
		}
	}
	name := pipeName()
	listening, err := pipeServer(name, func(c io.ReadWriteCloser) { _ = agent.ServeAgent(kr, c) })
	if err != nil {
		return sshagent.Source{}, nil, err
	}
	if err := waitListening(listening); err != nil {
		return sshagent.Source{}, nil, err
	}
	return sshagent.Source{Type: sshagent.EndpointTypeWindowsNamedPipe, Value: name}, func() {}, nil
}

// pipeRejectingSource lists pub as the only identity but answers every sign
// request with SSH_AGENT_FAILURE, over a named pipe.
func pipeRejectingSource(pub ssh.PublicKey) (sshagent.Source, func(), error) {
	name := pipeName()
	listening, err := pipeServer(name, func(c io.ReadWriteCloser) {
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
	if err := waitListening(listening); err != nil {
		return sshagent.Source{}, nil, err
	}
	return sshagent.Source{Type: sshagent.EndpointTypeWindowsNamedPipe, Value: name}, func() {}, nil
}

// pipeDelayedSource accepts a connection over a named pipe and never answers
// the first request; reqInFlight closes once a request is in flight.
func pipeDelayedSource() (sshagent.Source, <-chan struct{}, func(), error) {
	name := pipeName()
	reqInFlight := make(chan struct{})
	listening, err := pipeServer(name, func(c io.ReadWriteCloser) {
		if _, err := readAgentRequest(c); err != nil {
			return
		}
		close(reqInFlight)
		var b [1]byte
		_, _ = c.Read(b[:]) // hold until the client cancels and closes
	})
	if err != nil {
		return sshagent.Source{}, nil, nil, err
	}
	if err := waitListening(listening); err != nil {
		return sshagent.Source{}, nil, nil, err
	}
	return sshagent.Source{Type: sshagent.EndpointTypeWindowsNamedPipe, Value: name}, reqInFlight, func() {}, nil
}

// pipeSelfAgent serves a keyring holding priv over a named pipe. It is the
// self-contained "real" agent for the Windows CI run (no dependency on the
// OpenSSH agent service).
func pipeSelfAgent(priv ed25519.PrivateKey) (sshagent.Source, func(), error) {
	return pipeKeyringSource(agent.AddedKey{PrivateKey: priv, Comment: "fixture-self"})
}
