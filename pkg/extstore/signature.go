package extstore

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
)

var (
	// ErrMissingSignature means there was no signature to check.
	ErrMissingSignature = errors.New("extension index signature is missing")
	// ErrInvalidSignature means the signature is malformed or no trusted key
	// produced it over these exact bytes.
	ErrInvalidSignature = errors.New("extension index signature is invalid")
)

// Sign signs the raw index bytes and returns the content of index.json.sig:
// standard base64 text of the ed25519 signature. Publisher side.
func Sign(raw []byte, key ed25519.PrivateKey) []byte {
	sig := ed25519.Sign(key, raw)
	out := make([]byte, base64.StdEncoding.EncodedLen(len(sig)))
	base64.StdEncoding.Encode(out, sig)
	return out
}

// Verify checks sig (the content of index.json.sig, as Sign writes it) against
// the raw index bytes. It accepts a signature from any one of keys, so the signing
// key can rotate while older apps still trust the previous one.
func Verify(raw, sig []byte, keys []ed25519.PublicKey) error {
	text := bytes.TrimSpace(sig)
	if len(text) == 0 {
		return ErrMissingSignature
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(text)))
	n, err := base64.StdEncoding.Decode(decoded, text)
	if err != nil || n != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	decoded = decoded[:n]
	for i, key := range keys {
		if len(key) != ed25519.PublicKeySize {
			return fmt.Errorf("trusted extension index key %d is %d bytes, want %d", i, len(key), ed25519.PublicKeySize)
		}
		if ed25519.Verify(key, raw, decoded) {
			return nil
		}
	}
	return ErrInvalidSignature
}
