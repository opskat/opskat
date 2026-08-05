package realfixture

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/sshagent"
)

// sshServer is a minimal controllable SSH server for the real-fixture
// scenarios. It generates a fresh host key (exposed to the client so the
// fixture verifies a real host key instead of InsecureIgnoreHostKey) and
// records every public key offered during userauth, so a scenario can assert
// only the precisely selected key reached the server.
type sshServer struct {
	addr      string
	hostKey   ssh.PublicKey
	cfg       *ssh.ServerConfig
	ln        net.Listener
	mu        sync.Mutex
	offered   []string
	closeOnce sync.Once
}

// newSSHServer runs an SSH server on 127.0.0.1:0. The supplied config's
// PublicKeyCallback (may be nil) decides acceptance after the server records
// the offered key; with a nil callback every key is accepted. A fresh host key
// is added to the supplied config, which must therefore not be reused.
func newSSHServer(cfg *ssh.ServerConfig) (*sshServer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	hostSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	cfg.AddHostKey(hostSigner)
	s := &sshServer{hostKey: hostSigner.PublicKey(), cfg: cfg}
	inner := cfg.PublicKeyCallback
	cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		s.record(sshagent.FingerprintSHA256(key))
		if inner != nil {
			return inner(c, key)
		}
		return nil, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.ln = ln
	s.addr = ln.Addr().String()
	go s.acceptLoop()
	return s, nil
}

func (s *sshServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			_, _, _, err := ssh.NewServerConn(c, s.cfg)
			if err != nil {
				return
			}
			var b [1]byte
			_, _ = c.Read(b[:])
		}(conn)
	}
}

func (s *sshServer) record(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.offered {
		if f == fp {
			return
		}
	}
	s.offered = append(s.offered, fp)
}

// offeredFingerprints returns the distinct public key fingerprints offered
// during userauth, in first-seen order.
func (s *sshServer) offeredFingerprints() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.offered...)
}

// Close stops accepting new connections.
func (s *sshServer) Close() error {
	s.closeOnce.Do(func() { _ = s.ln.Close() })
	return nil
}

// fixtureClientConfig builds a client config that verifies the server host key
// explicitly (a real verification, never InsecureIgnoreHostKey), which is what
// the Agent handshake's host key boundary requires.
func fixtureClientConfig(user string, hostKey ssh.PublicKey) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.FixedHostKey(hostKey),
		Timeout:         10 * time.Second,
	}
}
