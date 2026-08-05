package realfixture

import (
	"strings"
	"testing"
)

func TestScanArtifactsDetectsPrivateKeyPEM(t *testing.T) {
	log := "ssh-keygen wrote -----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\n"
	leaks := scanArtifacts("{}", log, nil, nil, nil)
	if len(leaks) == 0 {
		t.Fatal("expected a private_key leak for PEM material in the log")
	}
	if leaks[0].Kind != leakPrivateKey {
		t.Fatalf("expected leak kind private_key, got %s", leaks[0].Kind)
	}
	if leaks[0].Where != "log" {
		t.Fatalf("expected the leak to be found in the log, got %s", leaks[0].Where)
	}
}

func TestScanArtifactsDetectsPrivateKeyLiteral(t *testing.T) {
	// A private-key body fragment (not PEM-armored) must still be detected
	// via the exact-value scan, not the PEM regex.
	secret := "b3BlbnNzaC1rZXktdjEAAAAA" //nolint:gosec // fixture key-body fragment, not a real credential
	leaks := scanArtifacts("{}", "embedded "+secret, []string{secret}, nil, nil)
	if len(leaks) != 1 || leaks[0].Kind != leakPrivateKey {
		t.Fatalf("expected one private_key leak for an exact private-key string, got %v", leaks)
	}
}

func TestScanArtifactsDetectsChallengeAnswer(t *testing.T) {
	report := `{"scenarios":[{"detail":"answered with 123456"}]}`
	leaks := scanArtifacts(report, "", nil, []string{"123456"}, nil)
	if len(leaks) == 0 {
		t.Fatal("expected a challenge_answer leak when the exact answer appears in the report")
	}
	if leaks[0].Kind != leakAnswer {
		t.Fatalf("expected leak kind challenge_answer, got %s", leaks[0].Kind)
	}
	if leaks[0].Where != "report" {
		t.Fatalf("expected the leak to be found in the report, got %s", leaks[0].Where)
	}
}

func TestScanArtifactsDetectsPublicKeyBlob(t *testing.T) {
	pubBlob := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGkFGXqN7kHdF5kQVWaU7kHdF5kQVWaU7kHdF5kQVWaU7"
	log := "recorded key " + pubBlob
	leaks := scanArtifacts("{}", log, nil, nil, []string{pubBlob})
	if len(leaks) == 0 {
		t.Fatal("expected a public_key_blob leak when the raw blob appears")
	}
	if leaks[0].Kind != leakPublicKey {
		t.Fatalf("expected leak kind public_key_blob, got %s", leaks[0].Kind)
	}
}

func TestScanArtifactsNeverEchoesSecretValue(t *testing.T) {
	answer := "super-secret-answer-987654"
	log := "answer=" + answer
	leaks := scanArtifacts("{}", log, nil, []string{answer}, nil)
	if len(leaks) != 1 {
		t.Fatalf("expected exactly one leak, got %v", leaks)
	}
	for _, l := range leaks {
		if strings.Contains(l.Where, answer) {
			t.Fatalf("leak where-field must never contain the secret value: %q", l.Where)
		}
		if strings.Contains(string(l.Kind), answer) {
			t.Fatalf("leak kind must never contain the secret value: %q", l.Kind)
		}
	}
}

func TestScanArtifactsCleanReportAndLog(t *testing.T) {
	report := `{"platform":"darwin","socket_kind":"unix_socket","scenarios":[{"name":"native_success","expected":"ok","got":"ok","pass":true}]}`
	log := "selected fingerprint SHA256:abc123; signer used; transport closed"
	leaks := scanArtifacts(report, log, []string{"-----BEGIN OPENSSH PRIVATE KEY-----"}, []string{"123456"}, []string{"ssh-ed25519 BLOB"})
	if len(leaks) != 0 {
		t.Fatalf("expected no leaks for a clean report and log, got %v", leaks)
	}
}
