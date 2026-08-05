//go:build !windows

package realfixture

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"runtime"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/sshagent"
)

// TestRealUnixSocketFixtures drives internal/sshagent through a REAL agent
// over a real unix socket. It is skipped unless OPSKAT_REAL_AGENT_FIXTURE=1 is
// set (the scripts/ssh-agent-fixtures/run.sh orchestrator sets it), so
// `go test ./...` stays fast and CI-safe.
//
// When a system ssh-agent binary exists, run.sh starts it, pre-loads an
// ed25519 key with ssh-add and points SSH_AUTH_SOCK at it; the test then
// resolves the agent through the environment endpoint type, exercising env
// re-resolution, real unix-socket dialing and the same-user peer check.
//
// When OPSKAT_FIXTURE_PUBKEY is unset (no ssh-agent binary), the test serves
// its own keyring agent over a real unix socket as the documented fallback;
// the native success / identity-missing / MFA scenarios then use that socket.
func TestRealUnixSocketFixtures(t *testing.T) {
	if os.Getenv("OPSKAT_REAL_AGENT_FIXTURE") != "1" {
		t.Skip("real agent fixtures disabled; set OPSKAT_REAL_AGENT_FIXTURE=1 to run")
	}
	if testing.Short() {
		t.Skip("real agent fixtures skipped in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cfg := Config{
		Platform:   runtime.GOOS,
		SocketKind: "unix_socket",
		MFAAnswer:  "fixture-answer-42",
		Rejecting:  unixRejectingSource,
		Delayed:    unixDelayedSource,
	}
	var privateSecrets, pubBlobs []string
	answerSecrets := []string{cfg.MFAAnswer}

	if pubPath := os.Getenv("OPSKAT_FIXTURE_PUBKEY"); pubPath != "" {
		// System ssh-agent mode: run.sh already loaded the key.
		pub, err := parseAuthorizedKeyFile(pubPath)
		if err != nil {
			t.Fatalf("parse fixture public key: %v", err)
		}
		cfg.SelectedPub = pub
		cfg.AgentSource = "system_ssh_agent"
		cfg.Real = func() (sshagent.Source, error) {
			return sshagent.Source{Type: sshagent.EndpointTypeEnvironment, Value: "SSH_AUTH_SOCK"}, nil
		}
		pubBlobs = append(pubBlobs, string(pub.Marshal()))
		// The script may also pass the private key file so the sanitizer can
		// assert the key material never reaches the artifacts.
		if privPath := os.Getenv("OPSKAT_FIXTURE_PRIVKEY"); privPath != "" {
			if b, err := os.ReadFile(privPath); err == nil { //nolint:gosec // path from runner script env
				privateSecrets = append(privateSecrets, string(b))
			}
		}
	} else {
		// Documented fallback: serve our own keyring agent over a real socket.
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate fixture key: %v", err)
		}
		pub, err := ssh.NewPublicKey(priv.Public())
		if err != nil {
			t.Fatalf("fixture public key: %v", err)
		}
		cfg.SelectedPub = pub
		cfg.AgentSource = "go_keyring_fixture"
		src, cleanup, err := unixSelfAgent(priv)
		if err != nil {
			t.Fatalf("self-served agent: %v", err)
		}
		t.Cleanup(cleanup)
		cfg.Real = func() (sshagent.Source, error) { return src, nil }
		privateSecrets = append(privateSecrets, string(priv))
		pubBlobs = append(pubBlobs, string(pub.Marshal()))
	}

	if !runAndReport(t, ctx, cfg, privateSecrets, answerSecrets, pubBlobs) {
		t.Errorf("not all real unix-socket fixture scenarios passed")
	}
}
