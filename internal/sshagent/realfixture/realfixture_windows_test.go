//go:build windows

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

// TestRealWindowsNamedPipeFixtures drives internal/sshagent through an
// OpenSSH-compatible named pipe (\\\\.\\pipe\\...) over the Windows transport.
// It is CI-only: it never runs on macOS/Linux hosts, and in CI it runs under
// the race detector (the workflow invokes `go test -race`). The "real" agent
// is a self-contained keyring served over the named pipe, so the run does not
// depend on the Windows OpenSSH agent service; the pipe's byte-mode protocol
// and \\\\.\\pipe\\ namespace are exactly what OpenSSH's ssh-agent uses.
func TestRealWindowsNamedPipeFixtures(t *testing.T) {
	if os.Getenv("OPSKAT_REAL_AGENT_FIXTURE") != "1" {
		t.Skip("real agent fixtures disabled; set OPSKAT_REAL_AGENT_FIXTURE=1 to run")
	}
	if testing.Short() {
		t.Skip("real agent fixtures skipped in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate fixture key: %v", err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatalf("fixture public key: %v", err)
	}
	src, cleanup, err := pipeSelfAgent(priv)
	if err != nil {
		t.Fatalf("self-served named pipe agent: %v", err)
	}
	t.Cleanup(cleanup)

	cfg := Config{
		Platform:    runtime.GOOS,
		SocketKind:  "windows_named_pipe",
		AgentSource: "go_keyring_fixture",
		SelectedPub: pub,
		MFAAnswer:   "fixture-answer-42",
		Real:        func() (sshagent.Source, error) { return src, nil },
		Rejecting:   pipeRejectingSource,
		Delayed:     pipeDelayedSource,
	}

	privateSecrets := []string{string(priv)}
	answerSecrets := []string{cfg.MFAAnswer}
	pubBlobs := []string{string(pub.Marshal())}

	if !runAndReport(t, ctx, cfg, privateSecrets, answerSecrets, pubBlobs) {
		t.Errorf("not all real named-pipe fixture scenarios passed")
	}
}
