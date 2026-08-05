package sshagent

import (
	"context"
	"slices"

	"golang.org/x/crypto/ssh"
)

// MFAChallenge is the structured metadata of one keyboard-interactive
// challenge, in the exact order the server presented it. It never carries
// answers.
type MFAChallenge struct {
	// Name is the challenge name sent by the server (often empty).
	Name string
	// Instruction is the server-provided instruction text.
	Instruction string
	// Prompts are the per-prompt question strings in server order.
	Prompts []string
	// Echo reports whether each prompt's input may be echoed.
	Echo []bool
}

// InteractiveCaller receives structured MFA challenges and submits answers.
//
// SubmitChallenge is called once per keyboard-interactive round. It must
// return exactly len(ch.Prompts) answers in the same order as ch.Prompts, or
// an error to abort the handshake. Answers exist only for the current request:
// they are sent immediately and are never retained by this package, so the
// implementation must not store them. Implementations should return promptly
// when ctx is canceled, and may signal an explicit cancel by returning the
// CodeCancelled typed error.
type InteractiveCaller interface {
	SubmitChallenge(ctx context.Context, ch MFAChallenge) ([]string, error)
}

// DialOptions configures how the Agent handshake completes MFA.
type DialOptions struct {
	// MFA routes keyboard-interactive challenges to an interactive caller.
	// When nil, a connection that requires MFA fails with ssh_agent_mfa_required
	// instead of prompting.
	MFA InteractiveCaller
}

// mfaController owns the authentication sequence for one handshake: the
// precise public key is the only first factor, and keyboard-interactive is
// offered at most once, only after the public key partially succeeded. It is
// driven entirely on the caller's goroutine by the auth loop inside
// ssh.NewClientConn, so it needs no locking.
type mfaController struct {
	publicKey ssh.AuthMethod
	// interactive routes challenges; nil means the caller cannot interact.
	interactive InteractiveCaller
	// ctx is the handshake context captured from Dial, used to cancel a
	// blocked MFA wait.
	ctx context.Context
	// terminal is set by the challenge adapter when it aborts (non-interactive
	// mfa_required, caller cancel, or a challenge input error), so the final
	// error preserves the reason instead of collapsing into mfa_failed.
	terminal string
}

// choose implements ssh.ClientAuthCallback. It returns the next auth method or
// aborts the handshake with a typed error. Returning (nil, nil) stops offering
// methods: the auth loop then surfaces any pending signer error, and a bare
// server rejection maps to publickey_failed in Dial.
func (m *mfaController) choose(ctx *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
	partial := ctx.PartialSuccessMethods
	tried := ctx.TriedMethods

	switch {
	case len(partial) == 0:
		// No partial success yet: the precise public key is the only first
		// factor. If it was tried and rejected, keyboard-interactive is never
		// a fallback — stop offering methods.
		if slices.Contains(tried, "publickey") {
			return nil, nil
		}
		return m.publicKey, nil

	case len(partial) == 1 && partial[0] == "publickey":
		// The precise public key partially succeeded: allow exactly one
		// keyboard-interactive. A failed or repeated continuation closes the
		// handshake instead of negotiating more factors.
		if slices.Contains(tried, "keyboard-interactive") {
			if m.terminal != "" {
				return nil, newError(m.terminal, "keyboard-interactive did not complete")
			}
			return nil, newError(CodeMFAFailed, "server rejected the keyboard-interactive answers")
		}
		return m.keyboardInteractive(), nil

	default:
		// Repeated partial success, a third factor, or an unexpected method
		// sequence: close the handshake.
		return nil, newError(CodeAuthSequenceUnsupported, "unexpected authentication method sequence")
	}
}

// keyboardInteractive builds the single keyboard-interactive method. The
// challenge adapter captures ctx so a canceled wait stops the handshake; it
// never fabricates answers for zero-prompt or multi-prompt challenges and never
// retains the answers it passes through.
func (m *mfaController) keyboardInteractive() ssh.AuthMethod {
	ctx := m.ctx
	return ssh.KeyboardInteractive(func(name, instruction string, prompts []string, echos []bool) ([]string, error) {
		if m.interactive == nil {
			m.terminal = CodeMFARequired
			return nil, newError(CodeMFARequired, "the server requires keyboard-interactive but no interactive caller is available")
		}
		ch := MFAChallenge{Name: name, Instruction: instruction, Prompts: prompts, Echo: echos}
		type challengeResult struct {
			answers []string
			err     error
		}
		resCh := make(chan challengeResult, 1)
		go func() {
			answers, err := m.interactive.SubmitChallenge(ctx, ch)
			resCh <- challengeResult{answers: answers, err: err}
		}()
		select {
		case res := <-resCh:
			if res.err != nil {
				if code, ok := CodeOf(res.err); ok {
					m.terminal = code
					return nil, res.err
				}
				m.terminal = CodeMFAFailed
				return nil, newError(CodeMFAFailed, "challenge input or server verification failed")
			}
			return res.answers, nil
		case <-ctx.Done():
			m.terminal = CodeCancelled
			return nil, newError(CodeCancelled, "MFA challenge was canceled")
		}
	})
}
