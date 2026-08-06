package sshagent

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestHostKeyContract(t *testing.T) {
	convey.Convey("host key boundary contract", t, func() {
		convey.Convey("a missing verifier fails closed", func() {
			hk := NewHostKeyContract(nil)
			_, pub := newTestKey(t)
			err := hk.Callback()("host", &net.TCPAddr{}, pub)
			convey.So(errCode(err), convey.ShouldEqual, CodeHostKeyVerifierMissing)
		})

		convey.Convey("the first accepted key is pinned and a same-key rekey passes without re-verification", func() {
			calls := 0
			_, pub := newTestKey(t)
			hk := NewHostKeyContract(func(string, net.Addr, ssh.PublicKey) error {
				calls++
				return nil
			})
			cb := hk.Callback()
			convey.So(cb("host", &net.TCPAddr{}, pub), convey.ShouldBeNil)
			convey.So(calls, convey.ShouldEqual, 1)
			// rekey with the same key: no repository or UI round-trip
			convey.So(cb("host", &net.TCPAddr{}, pub), convey.ShouldBeNil)
			convey.So(calls, convey.ShouldEqual, 1)
		})

		convey.Convey("a changed key on rekey fails the connection", func() {
			_, pubA := newTestKey(t)
			_, pubB := newTestKey(t)
			hk := NewHostKeyContract(func(string, net.Addr, ssh.PublicKey) error { return nil })
			cb := hk.Callback()
			convey.So(cb("host", &net.TCPAddr{}, pubA), convey.ShouldBeNil)
			err := cb("host", &net.TCPAddr{}, pubB)
			convey.So(errCode(err), convey.ShouldEqual, CodeHostKeyChanged)
		})

		convey.Convey("a failing verifier maps to store_failed and pins nothing", func() {
			calls := 0
			_, pub := newTestKey(t)
			hk := NewHostKeyContract(func(string, net.Addr, ssh.PublicKey) error {
				calls++
				return errors.New("store query failed")
			})
			cb := hk.Callback()
			err := cb("host", &net.TCPAddr{}, pub)
			convey.So(errCode(err), convey.ShouldEqual, CodeHostKeyStoreFailed)

			// nothing was pinned: a later identical key still consults the verifier
			err = cb("host", &net.TCPAddr{}, pub)
			convey.So(errCode(err), convey.ShouldEqual, CodeHostKeyStoreFailed)
			convey.So(calls, convey.ShouldEqual, 2)
		})
	})
}

func TestDialHostKeyBoundary(t *testing.T) {
	convey.Convey("host key boundary in the agent handshake", t, func() {
		convey.Convey("a missing verifier closes the transport and fails before any attempt", func() {
			priv, pub := newTestKey(t)
			agentSrv, agentClosed := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			newControllableSSHServer(t, &ssh.ServerConfig{})

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			cfg := agentClientConfig("alice")
			cfg.HostKeyCallback = nil
			_, err = ag.Dial(context.Background(), "127.0.0.1:1", cfg, aa)
			convey.So(errCode(err), convey.ShouldEqual, CodeHostKeyVerifierMissing)
			convey.So(waitClose(agentClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("a failing verifier terminates before the agent signs", func() {
			priv, pub := newTestKey(t)
			agentSrv, agentClosed := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{})

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			cfg := agentClientConfig("alice")
			cfg.HostKeyCallback = func(string, net.Addr, ssh.PublicKey) error {
				return errors.New("store query failed")
			}
			_, err = ag.Dial(context.Background(), sshSrv.addr, cfg, aa)
			convey.So(errCode(err), convey.ShouldEqual, CodeHostKeyStoreFailed)
			convey.So(aa.Used(), convey.ShouldBeFalse)
			convey.So(waitClose(agentClosed, time.Second), convey.ShouldBeTrue)
		})
	})
}
