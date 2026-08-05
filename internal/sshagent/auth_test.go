package sshagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// controllableSSHServer is a minimal SSH server for authentication state machine
// tests. It records every public key blob offered during userauth so tests can
// assert only the precisely selected key is provided to the server.
type controllableSSHServer struct {
	t    *testing.T
	ln   net.Listener
	addr string
	cfg  *ssh.ServerConfig

	mu      sync.Mutex
	offered []string
}

func (s *controllableSSHServer) record(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.offered {
		if f == fp {
			return
		}
	}
	s.offered = append(s.offered, fp)
}

func (s *controllableSSHServer) offeredFingerprints() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.offered...)
}

// newControllableSSHServer runs an SSH server on 127.0.0.1:0. The caller's
// PublicKeyCallback (may be nil) decides key acceptance after the server has
// recorded the offered key; with a nil callback every key is accepted. A fresh
// host key is added to the supplied config, which must therefore not be reused.
// The returned channel fires when the accepted connection is closed by either
// side.
func newControllableSSHServer(t *testing.T, cfg *ssh.ServerConfig) (*controllableSSHServer, chan struct{}) {
	t.Helper()
	s := &controllableSSHServer{t: t}
	inner := cfg.PublicKeyCallback
	cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		s.record(FingerprintSHA256(key))
		if inner != nil {
			return inner(c, key)
		}
		return nil, nil
	}
	cfg.AddHostKey(newHostKey(t))
	s.cfg = cfg

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.ln = ln
	s.addr = ln.Addr().String()

	connClosed := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer close(connClosed)
				_, _, _, err := ssh.NewServerConn(conn, s.cfg)
				if err != nil {
					return
				}
				var b [1]byte
				_, _ = conn.Read(b[:])
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s, connClosed
}

func newHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new host signer: %v", err)
	}
	return s
}

func agentClientConfig(user string) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
}

// signRejectingAgentServer serves a valid identity listing but answers every
// sign request with SSH_AGENT_FAILURE, simulating a provider that refuses to
// sign.
func signRejectingAgentServer(t *testing.T, pubBlob []byte, comment string) *testAgentServer {
	t.Helper()
	return newUnixAgentServer(t, func(c net.Conn) {
		for {
			req, err := readAgentRequest(c)
			if err != nil {
				return
			}
			if len(req) == 0 {
				return
			}
			switch req[0] {
			case 11: // SSH_AGENTC_REQUEST_IDENTITIES
				if err := writeAgentResponse(c, identitiesResponse(1, keyRecord(pubBlob, comment))); err != nil {
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
}

// fakeSigner lets tests exercise VerifySigner with a controllable provider.
type fakeSigner struct {
	pub  ssh.PublicKey
	sign func(data []byte) (*ssh.Signature, error)
}

func (f *fakeSigner) PublicKey() ssh.PublicKey { return f.pub }

func (f *fakeSigner) Sign(_ io.Reader, data []byte) (*ssh.Signature, error) {
	return f.sign(data)
}

func TestVerifySigner(t *testing.T) {
	convey.Convey("VerifySigner locally validates every returned signature", t, func() {
		convey.Convey("provider rejection maps to sign_failed and is not counted", func() {
			_, pub := newTestKey(t)
			v := NewVerifySigner(&fakeSigner{pub: pub, sign: func([]byte) (*ssh.Signature, error) {
				return nil, errors.New("agent: failure")
			}})
			_, err := v.Sign(rand.Reader, []byte("data"))
			convey.So(errCode(err), convey.ShouldEqual, CodeSignFailed)
			convey.So(v.Used(), convey.ShouldBeFalse)
			convey.So(v.SignCount(), convey.ShouldEqual, 0)
		})

		convey.Convey("a signature from the wrong key fails local verification", func() {
			_, pub := newTestKey(t)
			_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
			convey.So(err, convey.ShouldBeNil)
			other, err := ssh.NewSignerFromKey(otherPriv)
			convey.So(err, convey.ShouldBeNil)
			v := NewVerifySigner(&fakeSigner{pub: pub, sign: func(data []byte) (*ssh.Signature, error) {
				return other.Sign(rand.Reader, data)
			}})
			_, err = v.Sign(rand.Reader, []byte("data"))
			convey.So(errCode(err), convey.ShouldEqual, CodeSignFailed)
			convey.So(v.Used(), convey.ShouldBeFalse)
		})

		convey.Convey("a malformed signature fails local verification", func() {
			_, pub := newTestKey(t)
			v := NewVerifySigner(&fakeSigner{pub: pub, sign: func([]byte) (*ssh.Signature, error) {
				return &ssh.Signature{Format: "bogus", Blob: []byte("junk")}, nil
			}})
			_, err := v.Sign(rand.Reader, []byte("data"))
			convey.So(errCode(err), convey.ShouldEqual, CodeSignFailed)
			convey.So(v.Used(), convey.ShouldBeFalse)
		})

		convey.Convey("a valid signature is locally verified and counted", func() {
			priv, _ := newTestKey(t)
			inner, err := ssh.NewSignerFromKey(priv)
			convey.So(err, convey.ShouldBeNil)
			v := NewVerifySigner(inner)
			sig, err := v.Sign(rand.Reader, []byte("data"))
			convey.So(err, convey.ShouldBeNil)
			convey.So(sig, convey.ShouldNotBeNil)
			convey.So(v.Used(), convey.ShouldBeTrue)
			convey.So(v.SignCount(), convey.ShouldEqual, 1)
		})
	})
}

func TestAuthMethodSelection(t *testing.T) {
	convey.Convey("precise signer selection", t, func() {
		convey.Convey("zero matching identities returns identity_missing and closes the transport", func() {
			priv, _ := newTestKey(t)
			_, other := newTestKey(t)
			srv, connClosed := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.AuthMethod(context.Background(), FingerprintSHA256(other))
			convey.So(errCode(err), convey.ShouldEqual, CodeIdentityMissing)
			convey.So(waitClose(connClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("two matching identities return identity_duplicate and close the transport", func() {
			_, pub := newTestKey(t)
			// The real keyring dedupes by public key, so craft a malicious
			// agent that lists the same identity twice.
			body := identitiesResponse(2, keyRecord(pub.Marshal(), "a"), keyRecord(pub.Marshal(), "b"))
			connClosed := make(chan struct{})
			srv := newUnixAgentServer(t, trackClose(connClosed, respondThenWaitClose(body)))
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(errCode(err), convey.ShouldEqual, CodeIdentityDuplicate)
			convey.So(waitClose(connClosed, time.Second), convey.ShouldBeTrue)
		})
	})
}

// mustPub returns the public key for a raw ed25519 private key.
func mustPub(t *testing.T, priv ed25519.PrivateKey) ssh.PublicKey {
	t.Helper()
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return s.PublicKey()
}

func TestDialSingleKeyHandshake(t *testing.T) {
	convey.Convey("Agent handshake with precise signer selection", t, func() {
		convey.Convey("offers only the selected key and uses the signer", func() {
			privA, pubA := newTestKey(t)
			privB, pubB := newTestKey(t)
			agentSrv, agentClosed := keyringServer(t,
				agent.AddedKey{PrivateKey: privA, Comment: "key a"},
				agent.AddedKey{PrivateKey: privB, Comment: "key b"},
			)
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{})

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pubB))
			convey.So(err, convey.ShouldBeNil)
			convey.So(aa.Used(), convey.ShouldBeFalse)

			client, err := ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa)
			convey.So(err, convey.ShouldBeNil)
			convey.So(client, convey.ShouldNotBeNil)
			defer func() { _ = client.Close() }()

			convey.So(aa.Used(), convey.ShouldBeTrue)
			convey.So(aa.SignCount(), convey.ShouldBeGreaterThan, 0)
			convey.So(sshSrv.offeredFingerprints(), convey.ShouldResemble, []string{FingerprintSHA256(pubB)})
			convey.So(sshSrv.offeredFingerprints(), convey.ShouldNotContain, FingerprintSHA256(pubA))
			convey.So(waitClose(agentClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("never falls back to another key", func() {
			privA, pubA := newTestKey(t)
			privB, pubB := newTestKey(t)
			agentSrv, _ := keyringServer(t,
				agent.AddedKey{PrivateKey: privA, Comment: "key a"},
				agent.AddedKey{PrivateKey: privB, Comment: "key b"},
			)
			// The server accepts only key A; the saved fingerprint selects B.
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
				if FingerprintSHA256(key) == FingerprintSHA256(pubA) {
					return nil, nil
				}
				return nil, errors.New("key rejected")
			}})

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pubB))
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa)
			convey.So(errCode(err), convey.ShouldEqual, CodePublicKeyFailed)
			convey.So(aa.Used(), convey.ShouldBeFalse)
			// The unselected key A was never offered to the server.
			convey.So(sshSrv.offeredFingerprints(), convey.ShouldResemble, []string{FingerprintSHA256(pubB)})
		})

		convey.Convey("clears inherited auth methods before the handshake", func() {
			priv, pub := newTestKey(t)
			agentSrv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			// The server also accepts password; if a leaked inherited password
			// method survived into the handshake it would win before the signer.
			var pwMu sync.Mutex
			pwAttempts := 0
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
					pwMu.Lock()
					pwAttempts++
					pwMu.Unlock()
					return nil, nil
				},
			})

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			cfg := agentClientConfig("alice")
			cfg.Auth = []ssh.AuthMethod{ssh.Password("hunter2")}
			client, err := ag.Dial(context.Background(), sshSrv.addr, cfg, aa)
			convey.So(err, convey.ShouldBeNil)
			defer func() { _ = client.Close() }()

			convey.So(pwAttempts, convey.ShouldEqual, 0)
			convey.So(sshSrv.offeredFingerprints(), convey.ShouldResemble, []string{FingerprintSHA256(pub)})
			convey.So(aa.Used(), convey.ShouldBeTrue)
		})

		convey.Convey("agent refusing to sign surfaces sign_failed", func() {
			_, pub := newTestKey(t)
			agentSrv := signRejectingAgentServer(t, pub.Marshal(), "k")
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{})

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa)
			convey.So(errCode(err), convey.ShouldEqual, CodeSignFailed)
			convey.So(aa.Used(), convey.ShouldBeFalse)
		})

		convey.Convey("server accepting none returns auth_not_used and closes the connection", func() {
			priv, _ := newTestKey(t)
			agentSrv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv, sshClosed := newControllableSSHServer(t, &ssh.ServerConfig{NoClientAuth: true})

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(mustPub(t, priv)))
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa)
			convey.So(errCode(err), convey.ShouldEqual, CodeAuthNotUsed)
			convey.So(aa.Used(), convey.ShouldBeFalse)
			convey.So(waitClose(sshClosed, time.Second), convey.ShouldBeTrue)
		})
	})
}
