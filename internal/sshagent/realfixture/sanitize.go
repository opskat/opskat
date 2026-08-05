package realfixture

import (
	"regexp"
	"strings"
)

// privateKeyPEM matches any PEM private-key armour, covering the common
// variants: OPENSSH, PRIVATE KEY (PKCS8), EC/RSA/DSA PRIVATE KEY and
// ENCRYPTED PRIVATE KEY.
var privateKeyPEM = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)

// leakKind classifies one forbidden item found in fixture artifacts.
type leakKind string

const (
	leakPrivateKey leakKind = "private_key"
	leakPublicKey  leakKind = "public_key_blob"
	leakSignature  leakKind = "signature"
	leakAnswer     leakKind = "challenge_answer"
)

// secretLeak is one forbidden item found in fixture artifacts. It never
// carries the leaked value itself, only where it was found and its kind, so
// the sanitizer cannot itself echo a secret back into the output it guards.
type secretLeak struct {
	Where string
	Kind  leakKind
}

// scanArtifacts checks the machine-readable report JSON and the runner log
// text for forbidden content: private key material, public key blobs (only
// fingerprints may be recorded), signature blobs and MFA challenge answers.
// private/answers/publicBlobs carry the exact values that must never appear
// (the private key file text and its base64, the challenge answers, the
// selected public key blob). It returns one entry per detected leak; the
// leaked value itself is never included in the result.
func scanArtifacts(reportJSON, logText string, private, answers, publicBlobs []string) []secretLeak {
	var leaks []secretLeak
	for _, src := range []struct {
		where string
		text  string
	}{{"report", reportJSON}, {"log", logText}} {
		if privateKeyPEM.MatchString(src.text) {
			leaks = append(leaks, secretLeak{Where: src.where, Kind: leakPrivateKey})
		}
		for _, v := range private {
			if v != "" && strings.Contains(src.text, v) {
				leaks = append(leaks, secretLeak{Where: src.where, Kind: leakPrivateKey})
			}
		}
		for _, v := range publicBlobs {
			if v != "" && strings.Contains(src.text, v) {
				leaks = append(leaks, secretLeak{Where: src.where, Kind: leakPublicKey})
			}
		}
		for _, v := range answers {
			if v != "" && strings.Contains(src.text, v) {
				leaks = append(leaks, secretLeak{Where: src.where, Kind: leakAnswer})
			}
		}
	}
	return leaks
}
