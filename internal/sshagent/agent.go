package sshagent

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Bounded identity limits from the design spec.
const (
	// MaxIdentities caps the number of identities a listing may return.
	MaxIdentities = 256
	// MaxKeyBlobBytes caps a single key blob.
	MaxKeyBlobBytes = 64 * 1024
	// MaxCommentBytes caps a single identity comment.
	MaxCommentBytes = 1024
)

// defaultListTimeout bounds a single identities listing so a delayed or stuck
// agent turns into a typed protocol error instead of hanging forever.
const defaultListTimeout = 30 * time.Second

// transport is the minimal connection surface the Agent needs: read/write,
// close and deadline support (for cancellation and timeouts).
type transport interface {
	io.ReadWriteCloser
	SetDeadline(t time.Time) error
}

// Identity is a bounded identity summary: canonical fingerprint, key type and
// cleaned comment. It never carries the public key blob.
type Identity struct {
	Fingerprint string
	Type        string
	Comment     string
}

// Agent is an open SSH Agent transport. It owns its connection: after a failed
// or cancelled operation it closes itself, and the caller closes it after a
// successful one.
type Agent struct {
	conn        transport
	client      agent.ExtendedAgent
	listTimeout time.Duration
	closeOnce   sync.Once
	closeErr    error
}

// Open establishes a transport to the endpoint described by src. It validates
// the endpoint for the current platform, dials with ctx, and verifies the peer
// is owned by the same user when the OS provides peer credentials. On any
// failure the transport is closed before the error is returned.
func Open(ctx context.Context, src Source) (*Agent, error) {
	if !src.Type.platformSupported() {
		return nil, newError(CodePlatformUnsupported, "agent endpoint type is not supported on this platform")
	}
	if err := src.Validate(); err != nil {
		return nil, err
	}
	kind, value, err := src.resolveEndpoint()
	if err != nil {
		return nil, err
	}
	conn, err := dialEndpoint(ctx, kind, value)
	if err != nil {
		if ctx.Err() != nil {
			return nil, newError(CodeCancelled, "agent open was cancelled")
		}
		return nil, newError(CodeEndpointUnavailable, "agent endpoint is unavailable")
	}
	if kind == kindUnix {
		if sc, ok := conn.(syscall.Conn); ok {
			if uid, ok := peerEUID(sc); ok && uid != os.Getuid() {
				_ = conn.Close()
				return nil, newError(CodeEndpointUnavailable, "agent socket is owned by a different user")
			}
		}
	}
	return &Agent{
		conn:        conn,
		client:      agent.NewClient(conn),
		listTimeout: defaultListTimeout,
	}, nil
}

// Close closes the transport. It is idempotent.
func (a *Agent) Close() error {
	a.closeOnce.Do(func() {
		a.closeErr = a.conn.Close()
	})
	return a.closeErr
}

// ListIdentities lists the agent's identities under the bounded limits. On any
// error (including cancellation, empty or malformed payloads) the transport is
// closed; on success it stays open for the caller (e.g. signing).
func (a *Agent) ListIdentities(ctx context.Context) ([]Identity, error) {
	if err := ctx.Err(); err != nil {
		a.Close()
		return nil, newError(CodeCancelled, "agent operation was cancelled")
	}

	deadline := time.Now().Add(a.listTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = a.conn.SetDeadline(deadline)

	// Watcher: a blocked agent read cannot be interrupted by context alone, so
	// closing the transport is the prompt cancellation mechanism (and works
	// even where deadlines are not enforced).
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			a.Close()
		case <-done:
		}
	}()

	keys, err := a.client.List()
	close(done)
	if err != nil {
		a.Close()
		return nil, a.listError(ctx, err)
	}
	// Success: drop the listing deadline so later operations (e.g. signing)
	// are not bounded by it.
	_ = a.conn.SetDeadline(time.Time{})
	if cerr := ctx.Err(); cerr != nil {
		a.Close()
		return nil, newError(CodeCancelled, "agent operation was cancelled")
	}

	if len(keys) > MaxIdentities {
		a.Close()
		return nil, newError(CodePayloadInvalid, "agent returned too many identities")
	}
	if len(keys) == 0 {
		a.Close()
		return nil, newError(CodeEmpty, "agent is reachable but holds no identities")
	}

	ids := make([]Identity, 0, len(keys))
	for _, k := range keys {
		if len(k.Blob) > MaxKeyBlobBytes {
			a.Close()
			return nil, newError(CodePayloadInvalid, "agent returned an oversized key blob")
		}
		if len(k.Comment) > MaxCommentBytes {
			a.Close()
			return nil, newError(CodePayloadInvalid, "agent returned an oversized key comment")
		}
		pub, err := ssh.ParsePublicKey(k.Blob)
		if err != nil {
			a.Close()
			return nil, newError(CodePayloadInvalid, "agent returned a key blob that is not a valid SSH public key")
		}
		ids = append(ids, Identity{
			Fingerprint: FingerprintSHA256(pub),
			Type:        pub.Type(),
			Comment:     cleanComment(k.Comment),
		})
	}
	return ids, nil
}

// listError maps an agent client failure to a typed error, distinguishing a
// caller cancellation, an unresponsive agent (timeout) and a broken protocol.
func (a *Agent) listError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return newError(CodeCancelled, "agent operation was cancelled")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return newError(CodeProtocolError, "agent did not respond in time")
	}
	return newError(CodeProtocolError, "agent protocol or identity listing failed")
}

// dialEndpoint dials the resolved endpoint, honoring ctx for cancellation.
func dialEndpoint(ctx context.Context, kind transportKind, value string) (transport, error) {
	switch kind {
	case kindUnix:
		return (&net.Dialer{}).DialContext(ctx, "unix", value)
	case kindPipe:
		return dialNamedPipe(ctx, value)
	}
	return nil, errors.New("sshagent: unknown transport kind")
}

// FingerprintSHA256 returns the canonical SHA256 fingerprint of a public key.
func FingerprintSHA256(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

// cleanComment strips control characters and surrounding whitespace so a
// comment can never smuggle formatting into logs or UI.
func cleanComment(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(cleaned)
}
