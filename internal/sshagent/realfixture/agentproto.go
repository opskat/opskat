package realfixture

import (
	"encoding/binary"
	"io"

	"golang.org/x/crypto/ssh"
)

// This file reimplements the tiny ssh-agent wire helpers that the sshagent
// package keeps unexported, so the fixture harness can serve a controllable
// agent over a real transport (unix socket on macOS/Linux, named pipe on
// Windows) without reaching into the package under test.

// identitiesAnswer mirrors the wire layout of SSH_AGENT_IDENTITIES_ANSWER.
type identitiesAnswer struct {
	NumKeys uint32 `sshtype:"12"`
	Keys    []byte `ssh:"rest"`
}

// identitiesResponse builds a raw SSH_AGENT_IDENTITIES_ANSWER packet body.
func identitiesResponse(numKeys uint32, records ...[]byte) []byte {
	return ssh.Marshal(&identitiesAnswer{NumKeys: numKeys, Keys: concatBytes(records)})
}

// keyRecord builds one identity record (blob + comment) as an agent puts it
// on the wire.
func keyRecord(blob []byte, comment string) []byte {
	return ssh.Marshal(&struct {
		Blob    []byte
		Comment string
	}{Blob: blob, Comment: comment})
}

func concatBytes(parts [][]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// writeAgentResponse writes a length-prefixed agent protocol packet.
func writeAgentResponse(w io.Writer, body []byte) error {
	pkt := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(pkt, uint32(len(body)))
	copy(pkt[4:], body)
	_, err := w.Write(pkt)
	return err
}

// readAgentRequest reads one length-prefixed agent request.
func readAgentRequest(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	req := make([]byte, n)
	if _, err := io.ReadFull(r, req); err != nil {
		return nil, err
	}
	return req, nil
}
