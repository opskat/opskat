package sshagent

import (
	"context"
	"io"
	"net"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
)

// AgentAuth is the result of precise signer selection: a locally-verifying
// signer and the single publickey auth method that uses only that signer. It
// never falls back to another key.
type AgentAuth struct {
	signer *VerifySigner
	method ssh.AuthMethod
}

// Method returns the single publickey auth method. It uses only the precisely
// selected signer, so no other key can be tried during the handshake.
func (aa *AgentAuth) Method() ssh.AuthMethod { return aa.method }

// Signer returns the locally-verifying signer wrapped around the agent's
// selected signer, preserving its real signing capability.
func (aa *AgentAuth) Signer() *VerifySigner { return aa.signer }

// Used reports whether the selected signer produced at least one locally
// verified signature during the handshake.
func (aa *AgentAuth) Used() bool { return aa.signer.Used() }

// SignCount returns how many signatures the signer produced and locally
// verified.
func (aa *AgentAuth) SignCount() int { return aa.signer.SignCount() }

// AuthMethod performs precise signer selection against an open agent: it lists
// and validates the identities, requires exactly one to match the saved
// fingerprint (zero → identity_missing, several → identity_duplicate; neither
// falls back to another key), wraps only that signer in a locally-verifying
// signer and builds the single publickey auth method. On selection failure the
// transport is closed; on success it stays open so the caller can complete the
// handshake and then close it (or hand it to Dial, which closes it on every
// path).
func (a *Agent) AuthMethod(ctx context.Context, fingerprint string) (*AgentAuth, error) {
	signer, err := a.selectSigner(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	return &AgentAuth{signer: signer, method: ssh.PublicKeys(signer)}, nil
}

// selectSigner lists the agent identities, requires exactly one match for the
// saved fingerprint, and wraps the matching signer in a VerifySigner.
func (a *Agent) selectSigner(ctx context.Context, fingerprint string) (*VerifySigner, error) {
	ids, err := a.ListIdentities(ctx)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, id := range ids {
		if id.Fingerprint != fingerprint {
			continue
		}
		if idx != -1 {
			a.Close()
			return nil, newError(CodeIdentityDuplicate, "more than one agent identity matches the saved fingerprint")
		}
		idx = i
	}
	if idx == -1 {
		a.Close()
		return nil, newError(CodeIdentityMissing, "no agent identity matches the saved fingerprint")
	}
	signers, err := a.client.Signers()
	if err != nil {
		a.Close()
		return nil, a.listError(ctx, err)
	}
	if len(signers) <= idx {
		a.Close()
		return nil, newError(CodeProtocolError, "agent identity list changed while selecting the signer")
	}
	// Guard against the agent's state changing between the listing and the
	// signer fetch: the selected signer must still match the saved fingerprint.
	if FingerprintSHA256(signers[idx].PublicKey()) != fingerprint {
		a.Close()
		return nil, newError(CodeIdentityMissing, "no agent identity matches the saved fingerprint")
	}
	return NewVerifySigner(signers[idx]), nil
}

// Dial authenticates to addr using only the single publickey method of aa. cfg
// supplies the user, host key verification and timeout; its Auth and
// AuthCallback are cleared so no inherited or temporary auth method can leak
// into the handshake. The agent transport is closed on every path before the
// client is returned. If the server accepts "none" before the signer is used,
// the connection is closed and ssh_agent_auth_not_used is returned; a handshake
// that fails to complete the precise publickey exchange maps to
// ssh_agent_publickey_failed unless a typed agent error (e.g. sign_failed or
// cancelled) is already in flight.
func (a *Agent) Dial(ctx context.Context, addr string, cfg *ssh.ClientConfig, aa *AgentAuth) (*ssh.Client, error) {
	if cfg == nil {
		a.Close()
		return nil, newError(CodeProtocolError, "ssh client config is required")
	}
	cc := *cfg
	cc.Auth = []ssh.AuthMethod{aa.method}
	cc.AuthCallback = nil

	dialCtx := ctx
	if cc.Timeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, cc.Timeout)
		defer cancel()
	}
	raw, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		a.Close()
		if ctx.Err() != nil {
			return nil, newError(CodeCancelled, "agent handshake was cancelled")
		}
		return nil, err
	}
	client, chans, reqs, err := ssh.NewClientConn(raw, addr, &cc)
	if err != nil {
		a.Close()
		if ctx.Err() != nil {
			return nil, newError(CodeCancelled, "agent handshake was cancelled")
		}
		if _, ok := CodeOf(err); ok {
			return nil, err
		}
		return nil, newError(CodePublicKeyFailed, "the precise public key exchange did not complete")
	}
	// The handshake owns the agent transport: release it before returning the
	// established client so no socket or pipe reference is held.
	a.Close()
	if !aa.Used() {
		_ = client.Close()
		return nil, newError(CodeAuthNotUsed, "server accepted authentication without using the selected agent signer")
	}
	return ssh.NewClient(client, chans, reqs), nil
}

// VerifySigner wraps an agent signer and locally verifies every signature it
// returns against the selected public key. Provider rejection, a malformed
// signature and local verification failure all map to ssh_agent_sign_failed. It
// keeps the signer's real signing capability (including algorithm-aware
// signing) and records whether any signature was produced and validated.
type VerifySigner struct {
	inner ssh.Signer
	used  atomic.Bool
	count atomic.Int32
}

// NewVerifySigner wraps inner so each returned signature is locally verified.
func NewVerifySigner(inner ssh.Signer) *VerifySigner {
	return &VerifySigner{inner: inner}
}

// PublicKey returns the selected signer's public key.
func (v *VerifySigner) PublicKey() ssh.PublicKey { return v.inner.PublicKey() }

// Sign implements ssh.Signer, delegating to the agent and locally verifying the
// returned signature.
func (v *VerifySigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return v.sign(rand, data, "")
}

// SignWithAlgorithm implements ssh.AlgorithmSigner so RSA-capable agents keep
// negotiating rsa-sha2-256/512 instead of being flattened to a single algorithm.
func (v *VerifySigner) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	return v.sign(rand, data, algorithm)
}

func (v *VerifySigner) sign(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	var (
		sig *ssh.Signature
		err error
	)
	if algorithm == "" {
		sig, err = v.inner.Sign(rand, data)
	} else if sa, ok := v.inner.(ssh.AlgorithmSigner); ok {
		sig, err = sa.SignWithAlgorithm(rand, data, algorithm)
	} else {
		sig, err = v.inner.Sign(rand, data)
	}
	if err != nil {
		// Preserve a typed agent failure (e.g. cancellation); otherwise the
		// provider refused to sign.
		if code, ok := CodeOf(err); ok {
			return nil, newError(code, "agent signing failed")
		}
		return nil, newError(CodeSignFailed, "agent provider refused to sign")
	}
	if err := v.inner.PublicKey().Verify(data, sig); err != nil {
		return nil, newError(CodeSignFailed, "agent returned a signature that failed local verification")
	}
	v.used.Store(true)
	v.count.Add(1)
	return sig, nil
}

// Used reports whether a locally verified signature was produced.
func (v *VerifySigner) Used() bool { return v.used.Load() }

// SignCount returns the number of locally verified signatures produced.
func (v *VerifySigner) SignCount() int { return int(v.count.Load()) }

var _ ssh.AlgorithmSigner = (*VerifySigner)(nil)
