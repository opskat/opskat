// Package sshagent is the self-contained SSH Agent transport layer: endpoint
// config types, runtime endpoint validation, opening/closing the agent
// transport, listing identities under bounded limits, canonical SHA256
// fingerprint computation and typed error codes.
//
// It has no product coupling: no repositories, services, app layers or
// persistence. It never stores identities, public keys, signatures or private
// keys, and its error messages never leak endpoint values, comments or raw
// agent payloads.
package sshagent

import (
	"errors"
	"fmt"
)

// Stable error codes for Agent failures. They match the codes in the SSH Agent
// design spec (docs/superpowers/specs/2026-08-04-ssh-agent-auth-design.md).
const (
	// CodePlatformUnsupported: the current platform cannot serve this endpoint type.
	CodePlatformUnsupported = "ssh_agent_platform_unsupported"
	// CodeEnvUnset: a saved environment variable has no usable value.
	CodeEnvUnset = "ssh_agent_env_unset"
	// CodeEndpointUnavailable: the endpoint cannot be opened or its value is unusable on this platform.
	CodeEndpointUnavailable = "ssh_agent_endpoint_unavailable"
	// CodeProtocolError: the agent protocol or identity listing failed.
	CodeProtocolError = "ssh_agent_protocol_error"
	// CodePayloadInvalid: identity count, key blob or comment is malformed or over a limit.
	CodePayloadInvalid = "ssh_agent_payload_invalid"
	// CodeEmpty: the agent is reachable but holds no identities.
	CodeEmpty = "ssh_agent_empty"
	// CodeCancelled: the caller stopped waiting or the operation context ended.
	CodeCancelled = "ssh_agent_cancelled" //nolint:misspell // spec-pinned wire code from the SSH Agent design spec; British spelling is intentional
	// CodeIdentityMissing: the saved fingerprint currently matches no identity.
	CodeIdentityMissing = "ssh_agent_identity_missing"
	// CodeIdentityDuplicate: the fingerprint cannot uniquely select one signer.
	CodeIdentityDuplicate = "ssh_agent_identity_duplicate"
	// CodeSignFailed: the provider refused to sign, returned a malformed
	// signature, or the signature failed local verification.
	CodeSignFailed = "ssh_agent_sign_failed"
	// CodePublicKeyFailed: the precise public key exchange did not complete.
	CodePublicKeyFailed = "ssh_agent_publickey_failed"
	// CodeAuthNotUsed: SSH succeeded without ever using the selected signer
	// (the server accepted "none").
	CodeAuthNotUsed = "ssh_agent_auth_not_used"
	// CodeAuthSequenceUnsupported: the MFA or authentication method sequence
	// (repeated partial success, a third factor, or an unexpected method) is
	// not supported.
	CodeAuthSequenceUnsupported = "ssh_agent_auth_sequence_unsupported"
	// CodeMFARequired: a new connection requires interaction the caller
	// cannot provide (no hidden prompts are shown).
	CodeMFARequired = "ssh_agent_mfa_required"
	// CodeMFAFailed: challenge input or server verification failed.
	CodeMFAFailed = "ssh_agent_mfa_failed"
	// CodeHostKeyVerifierMissing: the product connection lacks an explicit
	// host key verifier.
	CodeHostKeyVerifierMissing = "ssh_host_key_verifier_missing"
	// CodeHostKeyStoreFailed: host key persistence failed before Agent signing.
	CodeHostKeyStoreFailed = "ssh_host_key_store_failed"
	// CodeHostKeyChanged: a rekey delivered a different key than the one
	// accepted at the start of the connection.
	CodeHostKeyChanged = "ssh_host_key_changed"
)

// Error is a typed Agent failure carrying a stable code and a clean message.
// The message never contains endpoint values, identity comments, public key
// blobs, signatures or raw agent payload.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// CodeOf reports whether err is a typed Agent error and returns its code.
func CodeOf(err error) (string, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, true
	}
	return "", false
}
