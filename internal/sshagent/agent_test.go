package sshagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestValidateEndpoint(t *testing.T) {
	convey.Convey("Source.Validate", t, func() {
		convey.Convey("environment requires a syntactically valid variable name", func() {
			convey.So((Source{Type: EndpointTypeEnvironment, Value: "SSH_AUTH_SOCK"}).Validate(), convey.ShouldBeNil)
			convey.So((Source{Type: EndpointTypeEnvironment, Value: "_MY_SOCK_1"}).Validate(), convey.ShouldBeNil)
			convey.So((Source{Type: EndpointTypeEnvironment, Value: ""}).Validate(), convey.ShouldNotBeNil)
			convey.So((Source{Type: EndpointTypeEnvironment, Value: "1FOO"}).Validate(), convey.ShouldNotBeNil)
			convey.So((Source{Type: EndpointTypeEnvironment, Value: "FOO=BAR"}).Validate(), convey.ShouldNotBeNil)
			convey.So((Source{Type: EndpointTypeEnvironment, Value: "FOO BAR"}).Validate(), convey.ShouldNotBeNil)
		})

		convey.Convey("unix_socket must be absolute after expansion and lexical cleaning", func() {
			convey.So((Source{Type: EndpointTypeUnixSocket, Value: "/tmp/agent.sock"}).Validate(), convey.ShouldBeNil)
			convey.So((Source{Type: EndpointTypeUnixSocket, Value: "~/agent.sock"}).Validate(), convey.ShouldBeNil)
			convey.So((Source{Type: EndpointTypeUnixSocket, Value: "agent.sock"}).Validate(), convey.ShouldNotBeNil)
			convey.So((Source{Type: EndpointTypeUnixSocket, Value: ""}).Validate(), convey.ShouldNotBeNil)
		})

		convey.Convey("windows_named_pipe accepts local pipes and rejects UNC", func() {
			convey.So((Source{Type: EndpointTypeWindowsNamedPipe, Value: `\\.\pipe\openssh-ssh-agent`}).Validate(), convey.ShouldBeNil)
			convey.So((Source{Type: EndpointTypeWindowsNamedPipe, Value: `\\.\PIPE\openssh-ssh-agent`}).Validate(), convey.ShouldBeNil)
			convey.So((Source{Type: EndpointTypeWindowsNamedPipe, Value: `\\server\pipe\ssh-agent`}).Validate(), convey.ShouldNotBeNil)
			convey.So((Source{Type: EndpointTypeWindowsNamedPipe, Value: `\\.\foo`}).Validate(), convey.ShouldNotBeNil)
			convey.So((Source{Type: EndpointTypeWindowsNamedPipe, Value: `\\.\pipe\`}).Validate(), convey.ShouldNotBeNil)
			convey.So((Source{Type: EndpointTypeWindowsNamedPipe, Value: ""}).Validate(), convey.ShouldNotBeNil)
		})

		convey.Convey("unknown endpoint type is rejected", func() {
			convey.So((Source{Type: EndpointType("bogus"), Value: "x"}).Validate(), convey.ShouldNotBeNil)
		})

		convey.Convey("platform support is not part of structural validation", func() {
			// A Windows pipe stays valid on unix so imports can keep it as
			// "unsupported" rather than discarding it.
			if runtime.GOOS != "windows" {
				convey.So((Source{Type: EndpointTypeWindowsNamedPipe, Value: `\\.\pipe\openssh-ssh-agent`}).Validate(), convey.ShouldBeNil)
			}
		})
	})
}

func TestUnixPathCleaning(t *testing.T) {
	convey.Convey("expandAndCleanUnixPath", t, func() {
		convey.Convey("lexically cleans and keeps absolute", func() {
			p, err := expandAndCleanUnixPath("/tmp/../tmp/agent.sock")
			convey.So(err, convey.ShouldBeNil)
			convey.So(p, convey.ShouldEqual, "/tmp/agent.sock")
		})
		convey.Convey("expands a leading tilde", func() {
			home, err := os.UserHomeDir()
			convey.So(err, convey.ShouldBeNil)
			p, err := expandAndCleanUnixPath("~/agent.sock")
			convey.So(err, convey.ShouldBeNil)
			convey.So(p, convey.ShouldEqual, home+"/agent.sock")
			p, err = expandAndCleanUnixPath("~")
			convey.So(err, convey.ShouldBeNil)
			convey.So(p, convey.ShouldEqual, home)
		})
		convey.Convey("rejects relative paths (never interpreted against the workdir)", func() {
			_, err := expandAndCleanUnixPath("agent.sock")
			convey.So(err, convey.ShouldNotBeNil)
			_, err = expandAndCleanUnixPath("./agent.sock")
			convey.So(err, convey.ShouldNotBeNil)
			_, err = expandAndCleanUnixPath("")
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}

func TestPlatformUnsupported(t *testing.T) {
	convey.Convey("Open rejects endpoint types the current platform cannot serve", t, func() {
		if runtime.GOOS == "windows" {
			_, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: `C:\agent.sock`})
			convey.So(errCode(err), convey.ShouldEqual, CodePlatformUnsupported)
		} else {
			_, err := Open(context.Background(), Source{Type: EndpointTypeWindowsNamedPipe, Value: `\\.\pipe\openssh-ssh-agent`})
			convey.So(errCode(err), convey.ShouldEqual, CodePlatformUnsupported)
		}
	})
}

func TestOpenEnvironmentReResolves(t *testing.T) {
	convey.Convey("environment endpoint re-reads the variable on every open", t, func() {
		priv, _ := newTestKey(t)
		srv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "env key"})
		src := Source{Type: EndpointTypeEnvironment, Value: "SSH_AUTH_SOCK"}

		convey.Convey("connects when the variable points at a live socket", func() {
			t.Setenv("SSH_AUTH_SOCK", srv.path)
			ag, err := Open(context.Background(), src)
			convey.So(err, convey.ShouldBeNil)
			convey.So(ag, convey.ShouldNotBeNil)
			convey.So(ag.Close(), convey.ShouldBeNil)
		})

		convey.Convey("returns env_unset when the variable has no value", func() {
			t.Setenv("SSH_AUTH_SOCK", "")
			_, err := Open(context.Background(), src)
			convey.So(errCode(err), convey.ShouldEqual, CodeEnvUnset)
		})

		convey.Convey("returns endpoint_unavailable when the value is a dead path", func() {
			t.Setenv("SSH_AUTH_SOCK", srv.path+".dead")
			_, err := Open(context.Background(), src)
			convey.So(errCode(err), convey.ShouldEqual, CodeEndpointUnavailable)
		})

		convey.Convey("rejects a relative resolved value instead of resolving against the workdir", func() {
			t.Setenv("SSH_AUTH_SOCK", "relative/agent.sock")
			_, err := Open(context.Background(), src)
			convey.So(errCode(err), convey.ShouldEqual, CodeEndpointUnavailable)
		})

		convey.Convey("works again after the value is restored", func() {
			t.Setenv("SSH_AUTH_SOCK", "")
			if _, err := Open(context.Background(), src); err == nil {
				t.Fatal("expected env_unset before restore")
			}
			t.Setenv("SSH_AUTH_SOCK", srv.path)
			ag, err := Open(context.Background(), src)
			convey.So(err, convey.ShouldBeNil)
			convey.So(ag.Close(), convey.ShouldBeNil)
		})
	})
}

func TestOpenUnixSocket(t *testing.T) {
	convey.Convey("unix_socket endpoint", t, func() {
		priv, _ := newTestKey(t)
		srv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "sock key"})

		convey.Convey("opens a live absolute path", func() {
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)
			convey.So(ag.Close(), convey.ShouldBeNil)
		})

		convey.Convey("returns endpoint_unavailable for a missing socket", func() {
			_, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path + ".missing"})
			convey.So(errCode(err), convey.ShouldEqual, CodeEndpointUnavailable)
		})

		convey.Convey("rejects a relative path instead of resolving against the workdir", func() {
			_, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: "relative/agent.sock"})
			convey.So(errCode(err), convey.ShouldEqual, CodeEndpointUnavailable)
		})
	})
}

func TestListIdentitiesHappyPath(t *testing.T) {
	convey.Convey("listing a well-behaved agent", t, func() {
		priv1, pub1 := newTestKey(t)
		priv2, pub2 := newTestKey(t)
		srv, _ := keyringServer(t,
			agent.AddedKey{PrivateKey: priv1, Comment: "first key"},
			agent.AddedKey{PrivateKey: priv2, Comment: "second key"},
		)
		ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
		convey.So(err, convey.ShouldBeNil)
		defer func() { _ = ag.Close() }()

		ids, err := ag.ListIdentities(context.Background())
		convey.So(err, convey.ShouldBeNil)
		convey.So(ids, convey.ShouldHaveLength, 2)
		convey.So(ids[0].Fingerprint, convey.ShouldEqual, ssh.FingerprintSHA256(pub1))
		convey.So(ids[1].Fingerprint, convey.ShouldEqual, ssh.FingerprintSHA256(pub2))
		convey.So(ids[0].Type, convey.ShouldEqual, "ssh-ed25519")
		convey.So(ids[0].Comment, convey.ShouldEqual, "first key")
	})
}

func TestListIdentitiesCommentCleaning(t *testing.T) {
	convey.Convey("comments are cleaned of control characters", t, func() {
		priv, _ := newTestKey(t)
		srv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "line1\n\x01line2\x7f"})
		ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
		convey.So(err, convey.ShouldBeNil)
		defer func() { _ = ag.Close() }()

		ids, err := ag.ListIdentities(context.Background())
		convey.So(err, convey.ShouldBeNil)
		convey.So(ids, convey.ShouldHaveLength, 1)
		convey.So(ids[0].Comment, convey.ShouldEqual, "line1line2")
	})
}

func TestListIdentitiesEmpty(t *testing.T) {
	convey.Convey("an agent with zero identities", t, func() {
		connClosed := make(chan struct{})
		srv := newUnixAgentServer(t, trackClose(connClosed, respondThenWaitClose(identitiesResponse(0))))

		ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
		convey.So(err, convey.ShouldBeNil)

		_, err = ag.ListIdentities(context.Background())
		convey.So(errCode(err), convey.ShouldEqual, CodeEmpty)

		convey.Convey("and the transport is closed on the error path", func() {
			convey.So(waitClose(connClosed, time.Second), convey.ShouldBeTrue)
		})
	})
}

func TestListIdentitiesPayloadLimits(t *testing.T) {
	convey.Convey("payload limits return typed errors", t, func() {
		_, pub := newTestKey(t)
		pubBlob := pub.Marshal()

		convey.Convey("single blob over 64 KiB", func() {
			body := identitiesResponse(1, keyRecord(wireKeyBlob("ssh-ed25519", make([]byte, 70*1024)), "big"))
			_, err := listAgainst(t, body)
			convey.So(errCode(err), convey.ShouldEqual, CodePayloadInvalid)
		})

		convey.Convey("single comment over 1 KiB", func() {
			body := identitiesResponse(1, keyRecord(pubBlob, strings.Repeat("c", 1100)))
			_, err := listAgainst(t, body)
			convey.So(errCode(err), convey.ShouldEqual, CodePayloadInvalid)
		})

		convey.Convey("more than 256 identities", func() {
			records := make([][]byte, 0, 300)
			for i := 0; i < 300; i++ {
				records = append(records, keyRecord(pubBlob, "k"))
			}
			body := identitiesResponse(300, records...)
			_, err := listAgainst(t, body)
			convey.So(errCode(err), convey.ShouldEqual, CodePayloadInvalid)
		})

		convey.Convey("blob that is not a valid SSH public key", func() {
			body := identitiesResponse(1, keyRecord(wireKeyBlob("ssh-ed25519", []byte("bad-length")), "nope"))
			_, err := listAgainst(t, body)
			convey.So(errCode(err), convey.ShouldEqual, CodePayloadInvalid)
		})

		convey.Convey("duplicate identities are listed and validated without crashing", func() {
			body := identitiesResponse(2, keyRecord(pubBlob, "a"), keyRecord(pubBlob, "b"))
			ag := openAgainst(t, body)
			defer func() { _ = ag.Close() }()
			ids, err := ag.ListIdentities(context.Background())
			convey.So(err, convey.ShouldBeNil)
			convey.So(ids, convey.ShouldHaveLength, 2)
			convey.So(ids[0].Fingerprint, convey.ShouldEqual, ids[1].Fingerprint)
			convey.So(ids[0].Fingerprint, convey.ShouldEqual, ssh.FingerprintSHA256(pub))
		})
	})
}

func TestListIdentitiesProtocolErrors(t *testing.T) {
	convey.Convey("malformed or unexpected agent replies return protocol_error", t, func() {
		convey.Convey("garbage response bytes", func() {
			_, err := listAgainst(t, []byte{0xFF, 0x00, 0x01})
			convey.So(errCode(err), convey.ShouldEqual, CodeProtocolError)
		})

		convey.Convey("wrong reply type for an identities request", func() {
			body := ssh.Marshal(&struct {
				Success bool `sshtype:"6"`
			}{Success: true})
			_, err := listAgainst(t, body)
			convey.So(errCode(err), convey.ShouldEqual, CodeProtocolError)
		})
	})
}

func TestListIdentitiesDelayed(t *testing.T) {
	convey.Convey("an agent that never replies times out as protocol_error", t, func() {
		srv := newUnixAgentServer(t, serveDelayed)
		ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
		convey.So(err, convey.ShouldBeNil)
		ag.listTimeout = 150 * time.Millisecond

		_, err = ag.ListIdentities(context.Background())
		convey.So(errCode(err), convey.ShouldEqual, CodeProtocolError)
	})
}

func TestListIdentitiesCancelled(t *testing.T) {
	convey.Convey("context cancellation aborts listing", t, func() {
		convey.Convey("canceled before the call", func() {
			priv, _ := newTestKey(t)
			srv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)
			defer func() { _ = ag.Close() }()

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = ag.ListIdentities(ctx)
			convey.So(errCode(err), convey.ShouldEqual, CodeCancelled)
		})

		convey.Convey("canceled while waiting on a silent agent", func() {
			srv := newUnixAgentServer(t, serveDelayed)
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)
			defer func() { _ = ag.Close() }()

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()
			_, err = ag.ListIdentities(ctx)
			convey.So(errCode(err), convey.ShouldEqual, CodeCancelled)
		})
	})
}

func TestTransportLifecycle(t *testing.T) {
	convey.Convey("transport ownership", t, func() {
		convey.Convey("stays open after a successful list until Close", func() {
			priv, _ := newTestKey(t)
			srv, connClosed := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)

			if _, err := ag.ListIdentities(context.Background()); err != nil {
				t.Fatalf("list: %v", err)
			}
			select {
			case <-connClosed:
				t.Fatal("transport closed before Close was called")
			case <-time.After(100 * time.Millisecond):
			}
			convey.So(ag.Close(), convey.ShouldBeNil)
			convey.So(waitClose(connClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("closes the transport automatically on an error path", func() {
			connClosed := make(chan struct{})
			srv := newUnixAgentServer(t, trackClose(connClosed, respondThenWaitClose(identitiesResponse(0))))
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)

			if _, err := ag.ListIdentities(context.Background()); err == nil {
				t.Fatal("expected empty error")
			}
			convey.So(waitClose(connClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("closes the transport automatically on cancellation", func() {
			connClosed := make(chan struct{})
			srv := newUnixAgentServer(t, trackClose(connClosed, serveDelayed))
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()
			if _, err := ag.ListIdentities(ctx); err == nil {
				t.Fatal("expected canceled error")
			}
			convey.So(waitClose(connClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("Close is idempotent", func() {
			priv, _ := newTestKey(t)
			srv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)
			convey.So(ag.Close(), convey.ShouldBeNil)
			convey.So(ag.Close(), convey.ShouldBeNil)
		})
	})
}

func TestPeerUID(t *testing.T) {
	convey.Convey("peer UID on a unix transport", t, func() {
		if runtime.GOOS == "windows" {
			t.Skip("unix transports are not used on windows")
		}
		convey.Convey("matches the current process UID", func() {
			priv, _ := newTestKey(t)
			srv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
			convey.So(err, convey.ShouldBeNil)
			defer func() { _ = ag.Close() }()

			uid, ok := peerEUID(ag.conn.(*net.UnixConn))
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(uid, convey.ShouldEqual, os.Getuid())
		})
	})
}

// --- small helpers ---

func newTestKey(t *testing.T) (ed25519.PrivateKey, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return priv, s.PublicKey()
}

func errCode(err error) string {
	if err == nil {
		return ""
	}
	code, _ := CodeOf(err)
	return code
}

// openAgainst opens a transport against a server that answers every identities
// request with the given raw body.
func openAgainst(t *testing.T, body []byte) *Agent {
	t.Helper()
	srv := newUnixAgentServer(t, respondThenWaitClose(body))
	ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: srv.path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return ag
}

// listAgainst opens a transport against a crafted server, lists identities,
// and returns only the error (the transport is closed by the error path).
func listAgainst(t *testing.T, body []byte) ([]Identity, error) {
	t.Helper()
	ag := openAgainst(t, body)
	defer func() { _ = ag.Close() }()
	return ag.ListIdentities(context.Background())
}

func trackClose(ch chan struct{}, h func(net.Conn)) func(net.Conn) {
	return func(c net.Conn) {
		defer close(ch)
		h(c)
	}
}

func waitClose(ch chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}
