package sshagent

import (
	"bytes"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

// HostKeyContract enforces the host key boundary contract for one connection:
// an explicit verifier is required (there is no InsecureIgnoreHostKey fallback),
// the first accepted key is pinned in connection memory, rekeys with the same
// key pass without re-invoking the verifier, and a changed key fails the
// connection. It never persists keys. Host key verification precedes userauth,
// so a verifier failure terminates the connection before any Agent signing.
type HostKeyContract struct {
	mu     sync.Mutex
	verify ssh.HostKeyCallback
	pinned ssh.PublicKey
}

// NewHostKeyContract wraps verify (the product's repository query/save) so its
// callback upholds the contract. verify may be nil; the resulting callback then
// fails closed.
func NewHostKeyContract(verify ssh.HostKeyCallback) *HostKeyContract {
	return &HostKeyContract{verify: verify}
}

// Callback returns the ssh.HostKeyCallback implementing the contract. It is
// safe for concurrent use because rekeys are verified on a different goroutine
// than the initial handshake.
func (h *HostKeyContract) Callback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if h.verify == nil {
			return newError(CodeHostKeyVerifierMissing, "an explicit host key verifier is required")
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.pinned != nil {
			if bytes.Equal(key.Marshal(), h.pinned.Marshal()) {
				return nil
			}
			return newError(CodeHostKeyChanged, "server host key changed after it was accepted")
		}
		if err := h.verify(hostname, remote, key); err != nil {
			if _, ok := CodeOf(err); ok {
				return err
			}
			return newError(CodeHostKeyStoreFailed, "host key verification failed before agent signing")
		}
		h.pinned = key
		return nil
	}
}
