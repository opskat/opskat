package sshagent

import (
	"context"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// CopyPublicKey re-reads src, verifies the identity whose canonical SHA256
// fingerprint matches fingerprint, and returns the OpenSSH authorized-key line
// for it without any agent comment (ssh.MarshalAuthorizedKey on the parsed
// blob). The transport is opened and closed within this call on every path,
// and the key is never persisted or cached. Zero matches returns
// ssh_agent_identity_missing; several matches return
// ssh_agent_identity_duplicate.
func CopyPublicKey(ctx context.Context, src Source, fingerprint string) (string, error) {
	ag, err := Open(ctx, src)
	if err != nil {
		return "", err
	}
	defer func() { ag.closeLog(ctx) }()

	keys, err := ag.listRawIdentities(ctx)
	if err != nil {
		return "", err
	}

	var match ssh.PublicKey
	matches := 0
	for _, k := range keys {
		pub, perr := ssh.ParsePublicKey(k.Blob)
		if perr != nil {
			continue // listRawIdentities already rejected unparseable blobs
		}
		if FingerprintSHA256(pub) == fingerprint {
			matches++
			match = pub
		}
	}
	switch matches {
	case 0:
		return "", newError(CodeIdentityMissing, "saved fingerprint matches no identity in the agent")
	case 1:
		return string(ssh.MarshalAuthorizedKey(match)), nil
	default:
		return "", newError(CodeIdentityDuplicate, "saved fingerprint is ambiguous in the agent")
	}
}

// listRawIdentities lists the agent's raw keys under the same bounded limits as
// ListIdentities (256 identities, 64 KiB blob, 1 KiB comment, all blobs must
// parse as SSH public keys). On any error the transport is closed; on success
// it stays open for the caller to close. Raw key blobs never leave this
// package — CopyPublicKey converts them straight to an authorized-key line.
func (a *Agent) listRawIdentities(ctx context.Context) ([]*agent.Key, error) {
	if err := ctx.Err(); err != nil {
		a.closeLog(ctx)
		return nil, newError(CodeCancelled, "agent operation was canceled")
	}

	deadline := time.Now().Add(a.listTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = a.conn.SetDeadline(deadline)

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			a.closeLog(ctx)
		case <-done:
		}
	}()

	keys, err := a.client.List()
	close(done)
	if err != nil {
		a.closeLog(ctx)
		return nil, a.listError(ctx, err)
	}
	_ = a.conn.SetDeadline(time.Time{})
	if cerr := ctx.Err(); cerr != nil {
		a.closeLog(ctx)
		return nil, newError(CodeCancelled, "agent operation was canceled")
	}

	if len(keys) > MaxIdentities {
		a.closeLog(ctx)
		return nil, newError(CodePayloadInvalid, "agent returned too many identities")
	}
	if len(keys) == 0 {
		a.closeLog(ctx)
		return nil, newError(CodeEmpty, "agent is reachable but holds no identities")
	}
	for _, k := range keys {
		if len(k.Blob) > MaxKeyBlobBytes {
			a.closeLog(ctx)
			return nil, newError(CodePayloadInvalid, "agent returned an oversized key blob")
		}
		if len(k.Comment) > MaxCommentBytes {
			a.closeLog(ctx)
			return nil, newError(CodePayloadInvalid, "agent returned an oversized key comment")
		}
		if _, err := ssh.ParsePublicKey(k.Blob); err != nil {
			a.closeLog(ctx)
			return nil, newError(CodePayloadInvalid, "agent returned a key blob that is not a valid SSH public key")
		}
	}
	return keys, nil
}
