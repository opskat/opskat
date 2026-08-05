package sshagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// mfaHandshakeServer builds an SSH server that accepts only the selected
// public key as partial success, then requires keyboard-interactive through
// kb. It reuses the controllable server (which records every offered key).
func mfaHandshakeServer(t *testing.T, selected ssh.PublicKey, kb func(ssh.ConnMetadata, ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error)) *controllableSSHServer {
	t.Helper()
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if FingerprintSHA256(key) != FingerprintSHA256(selected) {
				return nil, errors.New("public key rejected")
			}
			return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{KeyboardInteractiveCallback: kb}}
		},
		KeyboardInteractiveCallback: kb,
	}
	srv, _ := newControllableSSHServer(t, cfg)
	return srv
}

// recordingCaller records every structured challenge it receives and returns
// the configured answers. When blocked is non-nil it waits for ctx before
// answering, simulating a caller waiting on a UI.
type recordingCaller struct {
	mu      sync.Mutex
	calls   []MFAChallenge
	answers []string
	err     error
	blocked chan struct{}
	once    sync.Once
}

func (r *recordingCaller) SubmitChallenge(ctx context.Context, ch MFAChallenge) ([]string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, ch)
	answers := append([]string(nil), r.answers...)
	err := r.err
	r.mu.Unlock()
	if r.blocked != nil {
		r.once.Do(func() { close(r.blocked) })
		<-ctx.Done()
		return nil, newError(CodeCancelled, "challenge canceled by caller")
	}
	return answers, err
}

func (r *recordingCaller) challengeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *recordingCaller) first() MFAChallenge {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[0]
}

func TestMFAKeyboardInteractive(t *testing.T) {
	convey.Convey("MFA keyboard-interactive continuation after precise public key", t, func() {
		convey.Convey("a partial public key success allows exactly one keyboard-interactive", func() {
			priv, pub := newTestKey(t)
			agentSrv, agentClosed := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv := mfaHandshakeServer(t, pub, func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				answers, err := client("Verification", "Enter code", []string{"Code: ", "Second: "}, []bool{false, false})
				if err != nil {
					return nil, err
				}
				if len(answers) != 2 || answers[0] != "123456" || answers[1] != "ok" {
					return nil, errors.New("bad answers")
				}
				return nil, nil
			})
			caller := &recordingCaller{answers: []string{"123456", "ok"}}

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			client, err := ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa, DialOptions{MFA: caller})
			convey.So(err, convey.ShouldBeNil)
			convey.So(client, convey.ShouldNotBeNil)
			defer func() { _ = client.Close() }()

			convey.So(aa.Used(), convey.ShouldBeTrue)
			convey.So(caller.challengeCount(), convey.ShouldEqual, 1)
			ch := caller.first()
			convey.So(ch.Name, convey.ShouldEqual, "Verification")
			convey.So(ch.Instruction, convey.ShouldEqual, "Enter code")
			convey.So(ch.Prompts, convey.ShouldResemble, []string{"Code: ", "Second: "})
			convey.So(ch.Echo, convey.ShouldResemble, []bool{false, false})
			// the handshake owns the transport: released before the client is returned
			convey.So(waitClose(agentClosed, time.Second), convey.ShouldBeTrue)
		})

		convey.Convey("a rejected public key never opens keyboard-interactive", func() {
			priv, pub := newTestKey(t)
			agentSrv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			// The server rejects every public key outright; keyboard-interactive
			// must never be offered even though an interactive caller exists.
			sshSrv, _ := newControllableSSHServer(t, &ssh.ServerConfig{
				PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
					return nil, errors.New("public key rejected")
				},
			})
			caller := &recordingCaller{answers: []string{"123456"}}

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa, DialOptions{MFA: caller})
			convey.So(errCode(err), convey.ShouldEqual, CodePublicKeyFailed)
			convey.So(aa.Used(), convey.ShouldBeFalse)
			convey.So(caller.challengeCount(), convey.ShouldEqual, 0)
		})

		convey.Convey("repeated partial success closes the handshake", func() {
			priv, pub := newTestKey(t)
			agentSrv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			// The keyboard-interactive step also returns partial success. The Next
			// config must name at least one method so the server sends the
			// partial-success failure instead of aborting; the client aborts on
			// the repeated partial before ever offering another factor.
			sshSrv := mfaHandshakeServer(t, pub, func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				return nil, &ssh.PartialSuccessError{Next: ssh.ServerAuthCallbacks{
					PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
						return nil, errors.New("must not be reached")
					},
				}}
			})
			caller := &recordingCaller{answers: []string{"x"}}

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa, DialOptions{MFA: caller})
			convey.So(errCode(err), convey.ShouldEqual, CodeAuthSequenceUnsupported)
		})

		convey.Convey("a non-interactive caller gets mfa_required and never prompts", func() {
			priv, pub := newTestKey(t)
			agentSrv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv := mfaHandshakeServer(t, pub, func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				_, err := client("Verification", "Enter code", []string{"Code: "}, []bool{false})
				return nil, err
			})

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa)
			convey.So(errCode(err), convey.ShouldEqual, CodeMFARequired)
			convey.So(aa.Used(), convey.ShouldBeTrue)
		})

		convey.Convey("a zero-prompt challenge is routed to the caller, never fabricated", func() {
			priv, pub := newTestKey(t)
			agentSrv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv := mfaHandshakeServer(t, pub, func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				answers, err := client("Verification", "", nil, nil)
				if err != nil {
					return nil, err
				}
				if len(answers) != 0 {
					return nil, errors.New("expected no fabricated answers")
				}
				return nil, nil
			})
			caller := &recordingCaller{answers: []string{}}

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			client, err := ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa, DialOptions{MFA: caller})
			convey.So(err, convey.ShouldBeNil)
			defer func() { _ = client.Close() }()

			convey.So(caller.challengeCount(), convey.ShouldEqual, 1)
			convey.So(caller.first().Prompts, convey.ShouldHaveLength, 0)
		})

		convey.Convey("the client never pads a multi-prompt challenge with invented answers", func() {
			priv, pub := newTestKey(t)
			agentSrv, _ := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv := mfaHandshakeServer(t, pub, func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				answers, err := client("Verification", "", []string{"a: ", "b: "}, []bool{false, false})
				if err != nil {
					return nil, err
				}
				if len(answers) != 2 {
					return nil, errors.New("expected two answers")
				}
				return nil, nil
			})
			// The caller returns fewer answers than prompts; the handshake must
			// fail rather than invent the missing answer.
			caller := &recordingCaller{answers: []string{"only-one"}}

			ag, err := Open(context.Background(), Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(context.Background(), FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			_, err = ag.Dial(context.Background(), sshSrv.addr, agentClientConfig("alice"), aa, DialOptions{MFA: caller})
			convey.So(errCode(err), convey.ShouldEqual, CodeMFAFailed)
		})

		convey.Convey("cancel during the MFA wait closes the transport and stops waiting", func() {
			priv, pub := newTestKey(t)
			agentSrv, agentClosed := keyringServer(t, agent.AddedKey{PrivateKey: priv, Comment: "k"})
			sshSrv := mfaHandshakeServer(t, pub, func(c ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
				_, err := client("Verification", "Enter code", []string{"Code: "}, []bool{false})
				return nil, err
			})
			caller := &recordingCaller{blocked: make(chan struct{})}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			ag, err := Open(ctx, Source{Type: EndpointTypeUnixSocket, Value: agentSrv.path})
			convey.So(err, convey.ShouldBeNil)
			aa, err := ag.AuthMethod(ctx, FingerprintSHA256(pub))
			convey.So(err, convey.ShouldBeNil)

			done := make(chan error, 1)
			go func() {
				_, err := ag.Dial(ctx, sshSrv.addr, agentClientConfig("alice"), aa, DialOptions{MFA: caller})
				done <- err
			}()

			// Wait until the challenge is actually being awaited, then cancel.
			select {
			case <-caller.blocked:
			case <-time.After(2 * time.Second):
				convey.So("timed out waiting for the MFA wait to start", convey.ShouldBeNil)
			}
			cancel()

			select {
			case err := <-done:
				convey.So(errCode(err), convey.ShouldEqual, CodeCancelled)
			case <-time.After(2 * time.Second):
				convey.So("timed out waiting for the canceled dial", convey.ShouldBeNil)
			}
			convey.So(waitClose(agentClosed, time.Second), convey.ShouldBeTrue)
		})
	})
}

func TestMFASequenceGuard(t *testing.T) {
	convey.Convey("MFA continuation state machine", t, func() {
		// The controller is exercised with an opaque stand-in for the precise
		// public key method; the integration tests pin that public key is used
		// first and keyboard-interactive second.
		mk := func() *mfaController {
			return &mfaController{publicKey: ssh.Password("x")}
		}
		authCtx := func(partial, tried []string) *ssh.ClientAuthContext {
			return &ssh.ClientAuthContext{PartialSuccessMethods: partial, TriedMethods: tried}
		}

		convey.Convey("before any attempt the first factor is offered", func() {
			m := mk()
			method, err := m.choose(authCtx(nil, nil))
			convey.So(err, convey.ShouldBeNil)
			convey.So(method, convey.ShouldNotBeNil)
		})

		convey.Convey("a rejected public key never offers keyboard-interactive", func() {
			m := mk()
			method, err := m.choose(authCtx(nil, []string{"publickey"}))
			convey.So(method, convey.ShouldBeNil)
			convey.So(err, convey.ShouldBeNil)
		})

		convey.Convey("after public key partial success exactly one keyboard-interactive is offered", func() {
			m := mk()
			method, err := m.choose(authCtx([]string{"publickey"}, nil))
			convey.So(err, convey.ShouldBeNil)
			convey.So(method, convey.ShouldNotBeNil)
		})

		convey.Convey("a failed keyboard-interactive with a recorded terminal error surfaces that code", func() {
			m := mk()
			m.terminal = CodeMFARequired
			method, err := m.choose(authCtx([]string{"publickey"}, []string{"keyboard-interactive"}))
			convey.So(method, convey.ShouldBeNil)
			convey.So(errCode(err), convey.ShouldEqual, CodeMFARequired)
		})

		convey.Convey("a failed keyboard-interactive with no terminal error is mfa_failed", func() {
			m := mk()
			method, err := m.choose(authCtx([]string{"publickey"}, []string{"keyboard-interactive"}))
			convey.So(method, convey.ShouldBeNil)
			convey.So(errCode(err), convey.ShouldEqual, CodeMFAFailed)
		})

		convey.Convey("repeated partial success closes the handshake", func() {
			m := mk()
			method, err := m.choose(authCtx([]string{"publickey", "keyboard-interactive"}, nil))
			convey.So(method, convey.ShouldBeNil)
			convey.So(errCode(err), convey.ShouldEqual, CodeAuthSequenceUnsupported)
		})

		convey.Convey("an unexpected first partial success closes the handshake", func() {
			m := mk()
			method, err := m.choose(authCtx([]string{"password"}, nil))
			convey.So(method, convey.ShouldBeNil)
			convey.So(errCode(err), convey.ShouldEqual, CodeAuthSequenceUnsupported)
		})
	})
}
